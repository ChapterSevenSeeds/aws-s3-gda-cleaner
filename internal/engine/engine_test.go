package engine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/balli/aws-s3-gda-cleaner/internal/approval"
	"github.com/balli/aws-s3-gda-cleaner/internal/config"
	"github.com/balli/aws-s3-gda-cleaner/internal/deleter"
	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

func TestRunAutoDeletesAndReports(t *testing.T) {
	localRoot := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)

	scannerClient := &S3ClientMock{
		ListObjectsV2Func: func(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents: []types.Object{{
					Key:          aws.String("missing-old.txt"),
					Size:         aws.Int64(25),
					LastModified: aws.Time(old),
				}},
			}, nil
		},
	}
	deleterClient := &S3DeleterMock{
		DeleteObjectsFunc: func(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			return &s3.DeleteObjectsOutput{Deleted: []types.DeletedObject{{Key: aws.String("missing-old.txt")}}}, nil
		},
	}
	n := &NotifierMock{
		SendDeletionReportFunc: func([]scanner.S3Object, *deleter.DeletionResult) error { return nil },
	}

	scnr := scanner.New(scannerClient, "bucket", "")
	del := deleter.New(deleterClient, "bucket")

	cfg := &config.Config{
		LocalPath:        localRoot,
		StaleTime:        24 * time.Hour,
		DeletionBehavior: config.DeletionAuto,
	}

	eng := New(cfg, scnr, del, n)
	eng.Run(context.Background())

	if len(deleterClient.DeleteObjectsCalls()) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(deleterClient.DeleteObjectsCalls()))
	}
	reportCalls := n.SendDeletionReportCalls()
	if len(reportCalls) != 1 {
		t.Fatalf("report calls = %d, want 1", len(reportCalls))
	}
	if len(n.SendDeletionSummaryCalls()) != 0 {
		t.Fatalf("summary calls = %d, want 0", len(n.SendDeletionSummaryCalls()))
	}
	if len(reportCalls[0].Candidates) != 1 || reportCalls[0].Candidates[0].Key != "missing-old.txt" {
		t.Fatalf("unexpected report candidates: %+v", reportCalls[0].Candidates)
	}
}

func TestRunPromptCreatesApprovalAndSendsSummary(t *testing.T) {
	localRoot := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)

	scannerClient := &S3ClientMock{
		ListObjectsV2Func: func(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents: []types.Object{{
					Key:          aws.String("missing-old.txt"),
					Size:         aws.Int64(25),
					LastModified: aws.Time(old),
				}},
			}, nil
		},
	}
	deleterClient := &S3DeleterMock{
		DeleteObjectsFunc: func(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			return &s3.DeleteObjectsOutput{}, nil
		},
	}
	n := &NotifierMock{
		SendDeletionSummaryFunc: func([]scanner.S3Object, string) error { return nil },
	}

	scnr := scanner.New(scannerClient, "bucket", "")
	del := deleter.New(deleterClient, "bucket")

	cfg := &config.Config{
		LocalPath:        localRoot,
		StaleTime:        24 * time.Hour,
		DeletionBehavior: config.DeletionPrompt,
		ListenPort:       8080,
	}

	eng := New(cfg, scnr, del, n)
	appr := approval.NewServer(0, eng.ExecuteApprovedDeletion)
	eng.SetApprovalServer(appr)

	eng.Run(context.Background())

	if len(deleterClient.DeleteObjectsCalls()) != 0 {
		t.Fatalf("delete calls = %d, want 0", len(deleterClient.DeleteObjectsCalls()))
	}
	summaryCalls := n.SendDeletionSummaryCalls()
	if len(summaryCalls) != 1 {
		t.Fatalf("summary calls = %d, want 1", len(summaryCalls))
	}
	if len(n.SendDeletionReportCalls()) != 0 {
		t.Fatalf("report calls = %d, want 0", len(n.SendDeletionReportCalls()))
	}
	if !strings.Contains(summaryCalls[0].ApprovalURL, "/approve/") {
		t.Fatalf("approval URL %q does not contain /approve/", summaryCalls[0].ApprovalURL)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	appr.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "\"pending_approvals\":1") {
		t.Fatalf("expected one pending approval, got body: %s", w.Body.String())
	}
}

func TestRunSkipsOverlap(t *testing.T) {
	localRoot := t.TempDir()
	enteredList := make(chan struct{})
	blockList := make(chan struct{})

	scannerClient := &S3ClientMock{
		ListObjectsV2Func: func(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			select {
			case <-enteredList:
				// already closed
			default:
				close(enteredList)
			}
			<-blockList
			return &s3.ListObjectsV2Output{}, nil
		},
	}
	deleterClient := &S3DeleterMock{
		DeleteObjectsFunc: func(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			return &s3.DeleteObjectsOutput{}, nil
		},
	}
	n := &NotifierMock{
		SendDeletionReportFunc:  func([]scanner.S3Object, *deleter.DeletionResult) error { return nil },
		SendDeletionSummaryFunc: func([]scanner.S3Object, string) error { return nil },
	}

	scnr := scanner.New(scannerClient, "bucket", "")
	del := deleter.New(deleterClient, "bucket")
	cfg := &config.Config{
		LocalPath:        localRoot,
		StaleTime:        24 * time.Hour,
		DeletionBehavior: config.DeletionAuto,
	}

	eng := New(cfg, scnr, del, n)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		eng.Run(context.Background())
	}()

	select {
	case <-enteredList:
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not reach list call")
	}

	eng.Run(context.Background())
	close(blockList)
	wg.Wait()

	if len(scannerClient.ListObjectsV2Calls()) != 1 {
		t.Fatalf("list calls = %d, want 1", len(scannerClient.ListObjectsV2Calls()))
	}
}

func TestExecuteApprovedDeletionErrorPath(t *testing.T) {
	scannerClient := &S3ClientMock{
		ListObjectsV2Func: func(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{}, nil
		},
	}
	deleterClient := &S3DeleterMock{
		DeleteObjectsFunc: func(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			return nil, errors.New("boom")
		},
	}
	n := &NotifierMock{
		SendDeletionReportFunc: func([]scanner.S3Object, *deleter.DeletionResult) error { return nil },
	}
	scnr := scanner.New(scannerClient, "bucket", "")
	del := deleter.New(deleterClient, "bucket")
	cfg := &config.Config{DeletionBehavior: config.DeletionAuto}

	eng := New(cfg, scnr, del, n)
	err := eng.ExecuteApprovedDeletion(context.Background(), &approval.PendingApproval{
		Candidates: []scanner.S3Object{{Key: "a.txt", Size: 1, LastModified: time.Now().Add(-48 * time.Hour)}},
	})
	if err == nil {
		t.Fatal("expected error from ExecuteApprovedDeletion")
	}
	if len(n.SendDeletionReportCalls()) != 0 {
		t.Fatalf("report calls = %d, want 0", len(n.SendDeletionReportCalls()))
	}
}
