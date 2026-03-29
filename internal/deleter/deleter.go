package deleter

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

const maxDeleteBatch = 1000

// DeletionResult summarizes the outcome of a batch deletion.
type DeletionResult struct {
	Deleted   int
	Failed    int
	TotalSize int64
	Errors    []string
}

// S3Deleter defines the S3 API surface needed for deletion.
type S3Deleter interface {
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

// Deleter handles batch deletion of S3 objects.
type Deleter struct {
	client S3Deleter
	bucket string
}

// New creates a new Deleter.
func New(client S3Deleter, bucket string) *Deleter {
	return &Deleter{
		client: client,
		bucket: bucket,
	}
}

// DeleteObjects deletes the given S3 objects in batches of 1000.
func (d *Deleter) DeleteObjects(ctx context.Context, candidates []scanner.S3Object) (*DeletionResult, error) {
	result := &DeletionResult{}

	for i := 0; i < len(candidates); i += maxDeleteBatch {
		end := i + maxDeleteBatch
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[i:end]

		identifiers := make([]types.ObjectIdentifier, len(batch))
		for j, obj := range batch {
			identifiers[j] = types.ObjectIdentifier{
				Key: aws.String(obj.Key),
			}
		}

		output, err := d.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(d.bucket),
			Delete: &types.Delete{
				Objects: identifiers,
				Quiet:   aws.Bool(false),
			},
		})
		if err != nil {
			return result, fmt.Errorf("deleting batch starting at index %d: %w", i, err)
		}

		result.Deleted += len(output.Deleted)
		for _, obj := range output.Deleted {
			key := aws.ToString(obj.Key)
			// Find the candidate to accumulate size
			for _, c := range batch {
				if c.Key == key {
					result.TotalSize += c.Size
					break
				}
			}
		}

		if len(output.Errors) > 0 {
			result.Failed += len(output.Errors)
			for _, e := range output.Errors {
				errMsg := fmt.Sprintf("%s: %s", aws.ToString(e.Key), aws.ToString(e.Message))
				result.Errors = append(result.Errors, errMsg)
				slog.Error("failed to delete object", "key", aws.ToString(e.Key), "error", aws.ToString(e.Message))
			}
		}

		slog.Info("deleted batch", "batch_start", i, "deleted", len(output.Deleted), "failed", len(output.Errors))
	}

	slog.Info("deletion complete", "total_deleted", result.Deleted, "total_failed", result.Failed, "size_freed", result.TotalSize)
	return result, nil
}
