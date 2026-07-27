package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// OSSStorage is an Alibaba Cloud OSS storage backend.
// OSS provides an S3-compatible API but has two key differences from AWS S3:
//  1. Virtual-hosted-style is required (path-style is rejected).
//  2. AWS chunked transfer encoding (STREAMING-UNSIGNED-PAYLOAD-TRAILER) is
//     not supported; we pre-compute the SHA-256 payload hash so the SDK
//     signs with a concrete hash instead of unsigned-payload.
type OSSStorage struct {
	client    *s3.Client
	bucket    string
	region    string
	endpoint  string
	cdnDomain string
}

// NewOSSStorageFromEnv creates an OSSStorage from environment variables.
// Returns nil if OSS_BUCKET is not set.
//
// Environment variables:
//   - OSS_BUCKET (required)
//   - OSS_REGION (default: oss-cn-hangzhou)
//   - OSS_ENDPOINT (default: https://oss-{region}.aliyuncs.com)
//   - OSS_ACCESS_KEY_ID / OSS_SECRET_ACCESS_KEY (optional; falls back to default credential chain)
//   - OSS_CDN_DOMAIN (optional; CDN domain for returned URLs)
func NewOSSStorageFromEnv() *OSSStorage {
	bucket := os.Getenv("OSS_BUCKET")
	if bucket == "" {
		slog.Info("OSS_BUCKET not set, OSS upload disabled")
		return nil
	}

	region := os.Getenv("OSS_REGION")
	if region == "" {
		region = "oss-cn-hangzhou"
	}

	endpoint := os.Getenv("OSS_ENDPOINT")
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://oss-%s.aliyuncs.com", region)
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}

	accessKey := os.Getenv("OSS_ACCESS_KEY_ID")
	secretKey := os.Getenv("OSS_SECRET_ACCESS_KEY")
	if accessKey != "" && secretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		slog.Error("failed to load OSS config", "error", err)
		return nil
	}

	cdnDomain := os.Getenv("OSS_CDN_DOMAIN")

	s3Opts := []func(*s3.Options){
		func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			// OSS requires virtual-hosted-style; do NOT set UsePathStyle.
			// Disable automatic trailing checksums which trigger aws-chunked
			// encoding that OSS does not support.
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		},
		// Pre-compute payload SHA-256 before the UnsignedPayload middleware
		// runs, so the SDK signs with a concrete hash instead of using
		// STREAMING-UNSIGNED-PAYLOAD-TRAILER chunked encoding.
		func(o *s3.Options) {
			o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
				return stack.Finalize.Add(middleware.FinalizeMiddlewareFunc(
					"PreComputePayloadHashForOSS",
					preComputePayloadHash,
				), middleware.Before)
			})
		},
	}

	slog.Info("OSS storage initialized", "bucket", bucket, "region", region, "endpoint", endpoint, "cdn_domain", cdnDomain)
	return &OSSStorage{
		client:    s3.NewFromConfig(cfg, s3Opts...),
		bucket:    bucket,
		region:    region,
		endpoint:  endpoint,
		cdnDomain: cdnDomain,
	}
}

// preComputePayloadHash reads the request body, computes its SHA-256, and
// stores it in the context so the UnsignedPayload middleware (which sets
// UNSIGNED-PAYLOAD to trigger chunked encoding) is skipped.
func preComputePayloadHash(
	ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler,
) (middleware.FinalizeOutput, middleware.Metadata, error) {
	req, ok := in.Request.(*smithyhttp.Request)
	if !ok || req.Body == nil {
		return next.HandleFinalize(ctx, in)
	}
	// Only intervene when no payload hash has been set yet — if the caller
	// already computed one, respect it.
	if v4.GetPayloadHash(ctx) != "" {
		return next.HandleFinalize(ctx, in)
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return next.HandleFinalize(ctx, in)
	}
	hash := sha256.Sum256(bodyBytes)
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	ctx = v4.SetPayloadHash(ctx, fmt.Sprintf("%x", hash[:]))
	return next.HandleFinalize(ctx, in)
}

func (s *OSSStorage) CdnDomain() string {
	return s.cdnDomain
}

// KeyFromURL extracts the OSS object key from a CDN or OSS endpoint URL.
// OSS virtual-hosted-style: https://<bucket>.<endpoint-host>/<key>
// OSS CDN:                 https://<cdn-domain>/<key>
// Path-style (legacy):     https://<endpoint>/<bucket>/<key>
func (s *OSSStorage) KeyFromURL(rawURL string) string {
	// Try CDN prefix first.
	if s.cdnDomain != "" {
		prefix := "https://" + s.cdnDomain + "/"
		if strings.HasPrefix(rawURL, prefix) {
			return strings.TrimPrefix(rawURL, prefix)
		}
	}

	// Virtual-hosted-style: https://<bucket>.<endpoint-host>/<key>
	host := strings.TrimPrefix(strings.TrimPrefix(s.endpoint, "https://"), "http://")
	vhPrefix := fmt.Sprintf("https://%s.%s/", s.bucket, host)
	if strings.HasPrefix(rawURL, vhPrefix) {
		return strings.TrimPrefix(rawURL, vhPrefix)
	}

	// Path-style (legacy): https://<endpoint>/<bucket>/<key>
	psPrefix := strings.TrimRight(s.endpoint, "/") + "/" + s.bucket + "/"
	if strings.HasPrefix(rawURL, psPrefix) {
		return strings.TrimPrefix(rawURL, psPrefix)
	}

	// Fallback: take everything after the last "/".
	if i := strings.LastIndex(rawURL, "/"); i >= 0 {
		return rawURL[i+1:]
	}
	return rawURL
}

// GetReader streams the object body from OSS. The returned ReadCloser must
// be closed by the caller.
func (s *OSSStorage) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == "" {
		return nil, fmt.Errorf("oss GetReader: empty key")
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("oss GetObject: %w", err)
	}
	return out.Body, nil
}

func (s *OSSStorage) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return s.PresignGetWithContentDisposition(ctx, key, ttl, "")
}

func (s *OSSStorage) PresignGetWithContentDisposition(ctx context.Context, key string, ttl time.Duration, contentDisposition string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("oss PresignGet: empty key")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if contentDisposition != "" {
		input.ResponseContentDisposition = aws.String(contentDisposition)
	}
	out, err := s3.NewPresignClient(s.client).PresignGetObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("oss PresignGetObject: %w", err)
	}
	return out.URL, nil
}

// Delete removes an object from OSS. Errors are logged but not fatal.
func (s *OSSStorage) Delete(ctx context.Context, key string) {
	if key == "" {
		return
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		slog.Error("oss DeleteObject failed", "key", key, "error", err)
	}
}

// DeleteObject is Delete with the error surfaced — the media reconciler needs
// it to keep the ledger row and schedule a retry instead of assuming success.
func (s *OSSStorage) DeleteObject(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

// ObjectURL is the URL a successful Upload of key would return — a pure
// function of configuration, so the media intent ledger can persist it
// BEFORE the upload.
func (s *OSSStorage) ObjectURL(key string) string {
	return s.uploadedURL(key)
}

// DeleteKeys removes multiple objects from OSS. Best-effort, errors are logged.
func (s *OSSStorage) DeleteKeys(ctx context.Context, keys []string) {
	for _, key := range keys {
		s.Delete(ctx, key)
	}
}

func (s *OSSStorage) Upload(ctx context.Context, key string, data []byte, contentType string, filename string) (string, error) {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(s.bucket),
		Key:                aws.String(key),
		Body:               bytes.NewReader(data),
		ContentType:        aws.String(contentType),
		ContentDisposition: aws.String(ContentDisposition(contentType, filename)),
		CacheControl:       aws.String("max-age=432000,public"),
	})
	if err != nil {
		return "", fmt.Errorf("oss PutObject: %w", err)
	}
	return s.uploadedURL(key), nil
}

// uploadedURL returns the URL for client consumption after an upload.
// Priority: CDN domain > OSS virtual-hosted-style URL.
func (s *OSSStorage) uploadedURL(key string) string {
	if s.cdnDomain != "" {
		return fmt.Sprintf("https://%s/%s", s.cdnDomain, key)
	}
	// virtual-hosted-style: https://<bucket>.<endpoint-host>/<key>
	host := strings.TrimPrefix(strings.TrimPrefix(s.endpoint, "https://"), "http://")
	return fmt.Sprintf("https://%s.%s/%s", s.bucket, host, key)
}
