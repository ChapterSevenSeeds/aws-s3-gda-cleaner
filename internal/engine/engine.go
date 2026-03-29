package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/balli/aws-s3-gda-cleaner/internal/approval"
	"github.com/balli/aws-s3-gda-cleaner/internal/config"
	"github.com/balli/aws-s3-gda-cleaner/internal/deleter"
	"github.com/balli/aws-s3-gda-cleaner/internal/notifier"
	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

// Engine orchestrates the scan → notify → delete flow.
// Only one Run may execute at a time; overlapping cron invocations are skipped.
type Engine struct {
	mu             sync.Mutex
	running        bool
	cfg            *config.Config
	scanner        *scanner.Scanner
	deleter        *deleter.Deleter
	notifier       notifier.Notifier
	approvalServer *approval.Server
}

// New creates a new Engine.
func New(
	cfg *config.Config,
	scnr *scanner.Scanner,
	del *deleter.Deleter,
	ntf notifier.Notifier,
) *Engine {
	return &Engine{
		cfg:      cfg,
		scanner:  scnr,
		deleter:  del,
		notifier: ntf,
	}
}

// SetApprovalServer sets the approval server (resolves circular init).
func (e *Engine) SetApprovalServer(srv *approval.Server) {
	e.approvalServer = srv
}

// Run executes a single stale file detection cycle.
// If a previous run is still in progress, this call is skipped.
func (e *Engine) Run(ctx context.Context) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		slog.Warn("skipping scan: previous run still in progress")
		return
	}
	e.running = true
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
	}()

	slog.Info("starting stale file scan")

	// Step 1: List S3 objects
	s3Objects, err := e.scanner.ListS3Objects(ctx)
	if err != nil {
		slog.Error("failed to list S3 objects", "error", err)
		return
	}

	// Step 2: Check each S3 key against local filesystem
	candidates := scanner.FindStaleCandidates(s3Objects, e.cfg.LocalPath, e.cfg.StaleTime)

	if len(candidates) == 0 {
		slog.Info("no stale candidates found")
		return
	}

	slog.Info("stale candidates identified", "count", len(candidates))

	// Step 4: Execute deletion behavior
	switch e.cfg.DeletionBehavior {
	case config.DeletionAuto:
		e.handleAutoDeletion(ctx, candidates)
	case config.DeletionPrompt:
		e.handlePromptDeletion(candidates)
	}
}

// ExecuteApprovedDeletion is called by the approval server when the user approves.
func (e *Engine) ExecuteApprovedDeletion(ctx context.Context, pending *approval.PendingApproval) error {
	slog.Info("executing approved deletion", "candidates", len(pending.Candidates))

	result, err := e.deleter.DeleteObjects(ctx, pending.Candidates)
	if err != nil {
		slog.Error("deletion failed after approval", "error", err)
		return fmt.Errorf("deletion failed: %w", err)
	}

	if err := e.notifier.SendDeletionReport(pending.Candidates, result); err != nil {
		slog.Error("failed to send deletion report", "error", err)
		return fmt.Errorf("sending report: %w", err)
	}

	return nil
}

func (e *Engine) handleAutoDeletion(ctx context.Context, candidates []scanner.S3Object) {
	// Delete immediately
	result, err := e.deleter.DeleteObjects(ctx, candidates)
	if err != nil {
		slog.Error("auto deletion failed", "error", err)
		return
	}

	// Send report
	if err := e.notifier.SendDeletionReport(candidates, result); err != nil {
		slog.Error("failed to send deletion report", "error", err)
	}
}

func (e *Engine) handlePromptDeletion(candidates []scanner.S3Object) {
	// Create approval token
	token, err := e.approvalServer.CreateApproval(candidates)
	if err != nil {
		slog.Error("failed to create approval", "error", err)
		return
	}

	// Build approval URL
	approvalURL := fmt.Sprintf("%s/approve/%s", e.cfg.ApprovalBaseURL(), token)

	// Send summary email with approval link
	if err := e.notifier.SendDeletionSummary(candidates, approvalURL); err != nil {
		slog.Error("failed to send deletion summary", "error", err)
	}
}
