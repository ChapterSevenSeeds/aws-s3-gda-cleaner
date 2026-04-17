package approval

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

func TestApprovalFlowSingleUse(t *testing.T) {
	approved := make(chan *PendingApproval, 1)

	srv := NewServer(0, func(ctx context.Context, approval *PendingApproval) error {
		approved <- approval
		return nil
	})

	token, err := srv.CreateApproval([]scanner.S3Object{{Key: "file-a.txt", Size: 10, LastModified: time.Now()}})
	if err != nil {
		t.Fatalf("CreateApproval() error = %v", err)
	}

	// First approval succeeds.
	req1 := httptest.NewRequest(http.MethodGet, "/approve/"+token, nil)
	w1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first approval status = %d, want %d", w1.Code, http.StatusOK)
	}

	select {
	case got := <-approved:
		if got.Token != token {
			t.Fatalf("approved token = %q, want %q", got.Token, token)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval handler was not called")
	}

	// Second approval with same token must fail.
	req2 := httptest.NewRequest(http.MethodGet, "/approve/"+token, nil)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("second approval status = %d, want %d", w2.Code, http.StatusNotFound)
	}
}

func TestApprovalTokenExpiry(t *testing.T) {
	var called atomic.Int32

	srv := NewServer(1*time.Millisecond, func(ctx context.Context, approval *PendingApproval) error {
		called.Add(1)
		return nil
	})

	token, err := srv.CreateApproval([]scanner.S3Object{{Key: "file-b.txt", Size: 20, LastModified: time.Now()}})
	if err != nil {
		t.Fatalf("CreateApproval() error = %v", err)
	}

	time.Sleep(15 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/approve/"+token, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expired approval status = %d, want %d", w.Code, http.StatusGone)
	}
	if called.Load() != 0 {
		t.Fatalf("approval handler called %d times, want 0", called.Load())
	}
}

func TestHealthEndpointPendingCount(t *testing.T) {
	srv := NewServer(0, func(ctx context.Context, approval *PendingApproval) error { return nil })

	_, err := srv.CreateApproval([]scanner.S3Object{{Key: "a.txt", LastModified: time.Now()}})
	if err != nil {
		t.Fatalf("CreateApproval() error = %v", err)
	}
	_, err = srv.CreateApproval([]scanner.S3Object{{Key: "b.txt", LastModified: time.Now()}})
	if err != nil {
		t.Fatalf("CreateApproval() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "\"pending_approvals\":2") {
		t.Fatalf("health body = %q, expected pending_approvals=2", body)
	}
}
