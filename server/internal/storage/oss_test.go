package storage

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestOSSStorageKeyFromURL_VirtualHostedStyle(t *testing.T) {
	s := &OSSStorage{
		bucket:   "my-bucket",
		endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
	}

	rawURL := "https://my-bucket.oss-cn-hangzhou.aliyuncs.com/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestOSSStorageKeyFromURL_S3Endpoint(t *testing.T) {
	s := &OSSStorage{
		bucket:   "my-bucket",
		endpoint: "https://s3.oss-ap-southeast-1.aliyuncs.com",
	}

	rawURL := "https://my-bucket.s3.oss-ap-southeast-1.aliyuncs.com/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestOSSStorageKeyFromURL_CDN(t *testing.T) {
	s := &OSSStorage{
		bucket:    "my-bucket",
		endpoint:  "https://oss-cn-hangzhou.aliyuncs.com",
		cdnDomain: "cdn.example.com",
	}

	rawURL := "https://cdn.example.com/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestOSSStorageKeyFromURL_PathStyleLegacy(t *testing.T) {
	s := &OSSStorage{
		bucket:   "my-bucket",
		endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
	}

	rawURL := "https://oss-cn-hangzhou.aliyuncs.com/my-bucket/uploads/abc/file.png"

	if got := s.KeyFromURL(rawURL); got != "uploads/abc/file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "uploads/abc/file.png")
	}
}

func TestOSSStorageKeyFromURL_FallbackLastSegment(t *testing.T) {
	s := &OSSStorage{
		bucket:   "my-bucket",
		endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
	}

	// Unknown URL shape — falls back to last segment
	rawURL := "https://unknown.example.com/some/path/file.png"

	if got := s.KeyFromURL(rawURL); got != "file.png" {
		t.Fatalf("KeyFromURL(%q) = %q, want %q", rawURL, got, "file.png")
	}
}

func TestOSSStoragePresignGet(t *testing.T) {
	store := &OSSStorage{
		client: s3.New(s3.Options{
			Region:       "oss-cn-hangzhou",
			BaseEndpoint: aws.String("https://oss-cn-hangzhou.aliyuncs.com"),
			Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
		}),
		bucket:   "test-bucket",
		endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
	}

	got, err := store.PresignGet(context.Background(), "uploads/abc/file.txt", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	for _, want := range []string{
		"https://test-bucket.oss-cn-hangzhou.aliyuncs.com/uploads/abc/file.txt",
		"X-Amz-Signature=",
		"X-Amz-Expires=300",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("presigned URL %q does not contain %q", got, want)
		}
	}
}

func TestOSSStoragePresignGetWithContentDisposition(t *testing.T) {
	store := &OSSStorage{
		client: s3.New(s3.Options{
			Region:       "oss-cn-hangzhou",
			BaseEndpoint: aws.String("https://oss-cn-hangzhou.aliyuncs.com"),
			Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
		}),
		bucket:   "test-bucket",
		endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
	}

	got, err := store.PresignGetWithContentDisposition(
		context.Background(),
		"uploads/abc/file.txt",
		5*time.Minute,
		`attachment; filename="report.txt"`,
	)
	if err != nil {
		t.Fatalf("PresignGetWithContentDisposition: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	if got := u.Query().Get("response-content-disposition"); got != `attachment; filename="report.txt"` {
		t.Fatalf("response-content-disposition = %q", got)
	}
	if sig := u.Query().Get("X-Amz-Signature"); sig == "" {
		t.Fatalf("missing X-Amz-Signature in %q", got)
	}
}

func TestOSSStorageUploadedURL(t *testing.T) {
	const key = "uploads/abc/file.png"

	cases := []struct {
		name      string
		bucket    string
		endpoint  string
		cdnDomain string
		want      string
	}{
		{
			name:     "oss virtual hosted style",
			bucket:   "my-bucket",
			endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
			want:     "https://my-bucket.oss-cn-hangzhou.aliyuncs.com/uploads/abc/file.png",
		},
		{
			name:     "oss s3 endpoint",
			bucket:   "my-bucket",
			endpoint: "https://s3.oss-ap-southeast-1.aliyuncs.com",
			want:     "https://my-bucket.s3.oss-ap-southeast-1.aliyuncs.com/uploads/abc/file.png",
		},
		{
			name:      "cdn takes priority",
			bucket:    "my-bucket",
			endpoint:  "https://oss-cn-hangzhou.aliyuncs.com",
			cdnDomain: "cdn.example.com",
			want:      "https://cdn.example.com/uploads/abc/file.png",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &OSSStorage{
				bucket:    tc.bucket,
				endpoint:  tc.endpoint,
				cdnDomain: tc.cdnDomain,
			}
			if got := s.uploadedURL(key); got != tc.want {
				t.Fatalf("uploadedURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
