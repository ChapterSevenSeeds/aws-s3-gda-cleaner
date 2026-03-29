package approval

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

// PendingApproval represents a set of candidates awaiting user approval.
type PendingApproval struct {
	Token      string
	Candidates []scanner.S3Object
	CreatedAt  time.Time
	ExpiresAt  time.Time // zero value means no expiry
}

// ApprovalHandler is called when a user approves a pending deletion.
type ApprovalHandler func(ctx context.Context, approval *PendingApproval) error

// Server manages approval tokens and serves the HTTP approval endpoint.
type Server struct {
	mu            sync.Mutex
	pending       map[string]*PendingApproval
	tokenLifetime time.Duration
	onApprove     ApprovalHandler
	mux           *http.ServeMux
}

// NewServer creates a new approval Server.
func NewServer(tokenLifetime time.Duration, onApprove ApprovalHandler) *Server {
	s := &Server{
		pending:       make(map[string]*PendingApproval),
		tokenLifetime: tokenLifetime,
		onApprove:     onApprove,
		mux:           http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /approve/{token}", s.handleApprove)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	return s
}

// Handler returns the HTTP handler for this server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// CreateApproval generates a token and stores the pending approval.
// Returns the token string.
func (s *Server) CreateApproval(candidates []scanner.S3Object) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}

	approval := &PendingApproval{
		Token:      token,
		Candidates: candidates,
		CreatedAt:  time.Now(),
	}
	if s.tokenLifetime > 0 {
		approval.ExpiresAt = approval.CreatedAt.Add(s.tokenLifetime)
	}

	s.mu.Lock()
	s.pending[token] = approval
	s.mu.Unlock()

	slog.Info("created approval token", "token_prefix", token[:8]+"...", "candidates", len(candidates))
	return token, nil
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "Missing token", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	approval, exists := s.pending[token]
	if exists {
		// Remove immediately to prevent double-use
		delete(s.pending, token)
	}
	s.mu.Unlock()

	if !exists {
		slog.Warn("approval attempt with unknown/used token", "token_prefix", safePrefix(token))
		http.Error(w, "Invalid or already-used approval token.", http.StatusNotFound)
		return
	}

	// Check expiry
	if !approval.ExpiresAt.IsZero() && time.Now().After(approval.ExpiresAt) {
		slog.Warn("approval token expired", "token_prefix", safePrefix(token), "expired_at", approval.ExpiresAt)
		http.Error(w, "This approval token has expired.", http.StatusGone)
		return
	}

	slog.Info("approval granted", "token_prefix", safePrefix(token), "candidates", len(approval.Candidates))

	// Execute deletion in background so we can respond to the user immediately
	go func() {
		if err := s.onApprove(context.Background(), approval); err != nil {
			slog.Error("approval handler failed", "error", err)
		}
	}()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, approvalResponseHTML)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	s.mu.Lock()
	pendingCount := len(s.pending)
	s.mu.Unlock()

	fmt.Fprintf(w, `{"status":"ok","pending_approvals":%d}`, pendingCount)
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

func safePrefix(token string) string {
	if len(token) > 8 {
		return token[:8] + "..."
	}
	return token
}

var approvalResponseHTML = `<!DOCTYPE html>
<html>
<head>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; display: flex; justify-content: center; align-items: center; min-height: 100vh; margin: 0; background: #f5f5f5; }
  .card { background: white; border-radius: 12px; padding: 40px; text-align: center; box-shadow: 0 4px 6px rgba(0,0,0,0.1); max-width: 500px; }
  .icon { font-size: 64px; margin-bottom: 16px; }
  h1 { color: #059669; margin: 0 0 12px; }
  p { color: #6b7280; line-height: 1.6; }
</style>
</head>
<body>
<div class="card">
  <div class="icon">✓</div>
  <h1>Deletion Approved</h1>
  <p>The stale files are being deleted from S3. You will receive an email summary once the process completes.</p>
</div>
</body>
</html>`
