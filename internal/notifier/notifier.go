package notifier

import (
	"github.com/balli/aws-s3-gda-cleaner/internal/deleter"
	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

// Notifier defines the interface for sending notifications.
// Implementations can send via SMTP, Slack, webhooks, etc.
type Notifier interface {
	// SendDeletionSummary sends a summary of candidates pending deletion.
	// approvalURL is non-empty when deletion requires user approval.
	SendDeletionSummary(candidates []scanner.S3Object, approvalURL string) error

	// SendDeletionReport sends a report after deletion has been executed.
	SendDeletionReport(candidates []scanner.S3Object, result *deleter.DeletionResult) error
}
