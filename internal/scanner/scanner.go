package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Object represents a file stored in S3.
type S3Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// S3Client defines the S3 API surface needed by the scanner.
type S3Client interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// Scanner compares S3 bucket contents against the local filesystem.
type Scanner struct {
	s3Client S3Client
	bucket   string
	prefix   string
}

// New creates a new Scanner.
func New(client S3Client, bucket, prefix string) *Scanner {
	return &Scanner{
		s3Client: client,
		bucket:   bucket,
		prefix:   prefix,
	}
}

// ListS3Objects returns all objects in the configured bucket/prefix.
func (s *Scanner) ListS3Objects(ctx context.Context) (map[string]S3Object, error) {
	objects := make(map[string]S3Object)

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
	}
	if s.prefix != "" {
		input.Prefix = aws.String(s.prefix)
	}

	paginator := s3.NewListObjectsV2Paginator(s.s3Client, input)

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing S3 objects: %w", err)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)

			// Skip "directory" markers (keys ending in /)
			if strings.HasSuffix(key, "/") {
				continue
			}

			// Strip prefix for comparison with local paths
			relKey := key
			if s.prefix != "" {
				relKey = strings.TrimPrefix(key, s.prefix)
				relKey = strings.TrimPrefix(relKey, "/")
			}

			objects[relKey] = S3Object{
				Key:          key,
				Size:         aws.ToInt64(obj.Size),
				LastModified: aws.ToTime(obj.LastModified),
			}
		}
	}

	slog.Info("listed S3 objects", "count", len(objects), "bucket", s.bucket, "prefix", s.prefix)
	return objects, nil
}

// FindStaleCandidates iterates over S3 objects and checks whether each one
// still exists on the local filesystem. Only S3 is exhaustively listed;
// local existence is checked per-key via os.Stat.
func FindStaleCandidates(s3Objects map[string]S3Object, localPath string, staleTime time.Duration) []S3Object {
	now := time.Now()
	var candidates []S3Object
	var existCount int

	for relKey, obj := range s3Objects {
		// Convert S3 key (forward slashes) to OS path and join with local root
		localFile := filepath.Join(localPath, filepath.FromSlash(relKey))
		if _, err := os.Stat(localFile); err == nil {
			existCount++
			continue
		}
		age := now.Sub(obj.LastModified)
		if age >= staleTime {
			candidates = append(candidates, obj)
		}
	}

	slog.Info("found stale candidates",
		"candidates", len(candidates),
		"s3_total", len(s3Objects),
		"local_exists", existCount,
	)
	return candidates
}
