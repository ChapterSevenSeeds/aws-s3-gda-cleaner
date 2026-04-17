package deleter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

func TestDeleteObjectsBatchesAndTotals(t *testing.T) {
	candidates := make([]scanner.S3Object, 0, 1001)
	var wantTotal int64
	for i := 0; i < 1001; i++ {
		size := int64(i + 1)
		wantTotal += size
		candidates = append(candidates, scanner.S3Object{
			Key:          fmt.Sprintf("key-%d", i),
			Size:         size,
			LastModified: time.Now().Add(-400 * 24 * time.Hour),
		})
	}

	mock := &S3DeleterMock{
		DeleteObjectsFunc: func(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			deleted := make([]types.DeletedObject, 0, len(in.Delete.Objects))
			for _, obj := range in.Delete.Objects {
				deleted = append(deleted, types.DeletedObject{Key: obj.Key})
			}
			return &s3.DeleteObjectsOutput{Deleted: deleted}, nil
		},
	}

	d := New(mock, "bucket")
	result, err := d.DeleteObjects(context.Background(), candidates)
	if err != nil {
		t.Fatalf("DeleteObjects() unexpected error: %v", err)
	}

	calls := mock.DeleteObjectsCalls()
	if len(calls) != 2 {
		t.Fatalf("batch calls = %d, want 2", len(calls))
	}
	if len(calls[0].Params.Delete.Objects) != 1000 {
		t.Fatalf("first batch size = %d, want 1000", len(calls[0].Params.Delete.Objects))
	}
	if len(calls[1].Params.Delete.Objects) != 1 {
		t.Fatalf("second batch size = %d, want 1", len(calls[1].Params.Delete.Objects))
	}
	if result.Deleted != 1001 {
		t.Fatalf("Deleted = %d, want 1001", result.Deleted)
	}
	if result.Failed != 0 {
		t.Fatalf("Failed = %d, want 0", result.Failed)
	}
	if result.TotalSize != wantTotal {
		t.Fatalf("TotalSize = %d, want %d", result.TotalSize, wantTotal)
	}
}

func TestDeleteObjectsPartialFailures(t *testing.T) {
	candidates := []scanner.S3Object{
		{Key: "a.txt", Size: 10, LastModified: time.Now().Add(-400 * 24 * time.Hour)},
		{Key: "b.txt", Size: 20, LastModified: time.Now().Add(-400 * 24 * time.Hour)},
	}

	mock := &S3DeleterMock{
		DeleteObjectsFunc: func(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			return &s3.DeleteObjectsOutput{
				Deleted: []types.DeletedObject{{Key: aws.String("a.txt")}},
				Errors:  []types.Error{{Key: aws.String("b.txt"), Message: aws.String("denied")}},
			}, nil
		},
	}

	d := New(mock, "bucket")
	result, err := d.DeleteObjects(context.Background(), candidates)
	if err != nil {
		t.Fatalf("DeleteObjects() unexpected error: %v", err)
	}

	if result.Deleted != 1 {
		t.Fatalf("Deleted = %d, want 1", result.Deleted)
	}
	if result.Failed != 1 {
		t.Fatalf("Failed = %d, want 1", result.Failed)
	}
	if result.TotalSize != 10 {
		t.Fatalf("TotalSize = %d, want 10", result.TotalSize)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "b.txt: denied") {
		t.Fatalf("Errors = %+v, expected b.txt: denied", result.Errors)
	}
}

func TestDeleteObjectsBatchErrorStopsProcessing(t *testing.T) {
	candidates := []scanner.S3Object{
		{Key: "a.txt", Size: 10, LastModified: time.Now().Add(-400 * 24 * time.Hour)},
	}

	mock := &S3DeleterMock{
		DeleteObjectsFunc: func(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			return nil, errors.New("s3 unavailable")
		},
	}

	d := New(mock, "bucket")
	result, err := d.DeleteObjects(context.Background(), candidates)
	if err == nil {
		t.Fatal("expected error from DeleteObjects")
	}
	if result.Deleted != 0 || result.Failed != 0 || result.TotalSize != 0 {
		t.Fatalf("expected empty partial result, got %+v", result)
	}
}
