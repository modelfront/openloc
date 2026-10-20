package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type Storage struct {
	client *s3.Client
	bucket string
}

// NewStorage creates an S3 client.
// If endpoint is non-empty, uses static credentials (an S3-compatible store such as SeaweedFS).
// If endpoint is empty, uses the default AWS credential chain (IAM roles in prod).
func NewStorage(endpoint, accessKey, secretKey, bucket string) *Storage {
	if endpoint != "" {
		// S3-compatible store (SeaweedFS in dev) — static credentials + path-style
		client := s3.New(s3.Options{
			BaseEndpoint: aws.String(endpoint),
			Region:       "us-east-1",
			Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
			UsePathStyle: true,
		})
		return &Storage{client: client, bucket: bucket}
	}

	// AWS S3 — use default credential chain (IAM role, env vars, ~/.aws/credentials)
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"))
	if err != nil {
		// Fall back to anonymous — will fail on first request with a clear error
		client := s3.New(s3.Options{Region: "us-east-1"})
		return &Storage{client: client, bucket: bucket}
	}
	client := s3.NewFromConfig(cfg)
	return &Storage{client: client, bucket: bucket}
}

// EnsureBucket creates the bucket if it doesn't exist.
func (s *Storage) EnsureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err == nil {
		return nil
	}
	_, err = s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	return err
}

// Upload stores a project file under a generated key and returns that key.
func (s *Storage) Upload(ctx context.Context, projectID int64, filename string, body io.Reader) (string, error) {
	key := fmt.Sprintf("projects/%d/%d_%s", projectID, time.Now().UnixMilli(), filename)
	if err := s.Put(ctx, key, body); err != nil {
		return "", err
	}
	return key, nil
}

// Put stores an object under a caller-supplied key. Upload owns the
// `projects/<id>/` convention; anything storing objects outside that namespace
// (the LLM debug log) uses this.
func (s *Storage) Put(ctx context.Context, key string, body io.Reader) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   body,
	})
	if err != nil {
		return fmt.Errorf("upload to s3: %w", err)
	}
	return nil
}

// ListKeys returns every key under prefix, following pagination. ListObjectsV2
// caps a response at 1000 keys and sets a continuation token; stopping at the
// first page would silently under-report, which for a reaper means silently
// failing to delete.
func (s *Storage) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("list s3 prefix %q: %w", prefix, err)
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
		if out.NextContinuationToken == nil {
			return keys, nil
		}
		token = out.NextContinuationToken
	}
}

// DeleteKeys removes objects in batches. DeleteObjects accepts at most 1000
// keys per request, so longer slices are chunked.
func (s *Storage) DeleteKeys(ctx context.Context, keys []string) error {
	const maxPerRequest = 1000
	for batch := range slices.Chunk(keys, maxPerRequest) {
		ids := make([]types.ObjectIdentifier, 0, len(batch))
		for _, k := range batch {
			ids = append(ids, types.ObjectIdentifier{Key: aws.String(k)})
		}
		out, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("delete %d s3 objects: %w", len(batch), err)
		}
		// DeleteObjects reports per-key failures in the body with a 200 status,
		// so an unchecked error here would look like a successful prune. The
		// error leaves out the key: keys carry customers' file names, and the
		// error can reach error tracking.
		if len(out.Errors) > 0 {
			first := out.Errors[0]
			return fmt.Errorf("delete s3 objects: %d of %d failed, first %s: %s",
				len(out.Errors), len(batch), aws.ToString(first.Code), aws.ToString(first.Message))
		}
	}
	return nil
}

// Download retrieves a file from storage.
func (s *Storage) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var missing *types.NoSuchKey
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("download from s3: %w", ErrBlobNotFound)
		}
		return nil, fmt.Errorf("download from s3: %w", err)
	}
	return out.Body, nil
}
