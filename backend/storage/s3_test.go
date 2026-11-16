package storage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"zhuapic/config"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func testS3Client(status int) *S3Client {
	client := NewS3Client(config.S3Config{
		Endpoint: "https://s3.example.test", Region: "auto", Bucket: "bucket", AccessKey: "key", SecretKey: "secret", ForcePathStyle: true,
	})
	client.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    r,
		}, nil
	})
	return client
}

func TestDeleteObjectTreatsMissingObjectAsDeleted(t *testing.T) {
	client := testS3Client(http.StatusNotFound)
	if err := client.DeleteObject(context.Background(), "images/20260804/a.png"); err != nil {
		t.Fatalf("delete should be idempotent: %v", err)
	}
}

func TestGetObjectStillReportsMissingObject(t *testing.T) {
	client := testS3Client(http.StatusNotFound)
	if _, err := client.GetObject(context.Background(), "images/missing.png"); err == nil {
		t.Fatal("missing object should remain an error on reads")
	}
}

func TestPutObjectReaderUsesKnownHashAndLength(t *testing.T) {
	body := "streamed-s3-payload"
	client := NewS3Client(config.S3Config{
		Endpoint: "https://s3.example.test", Region: "auto", Bucket: "bucket", AccessKey: "key", SecretKey: "secret", ForcePathStyle: true,
	})
	client.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.ContentLength != int64(len(body)) {
			t.Fatalf("unexpected content length: %d", r.ContentLength)
		}
		if r.Header.Get("X-Amz-Content-Sha256") != ChecksumSHA256([]byte(body)) {
			t.Fatal("payload hash was not signed")
		}
		read, err := io.ReadAll(r.Body)
		if err != nil || string(read) != body {
			t.Fatalf("unexpected streamed body: %q, %v", read, err)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	if err := client.PutObjectReader(context.Background(), "images/stream.bin", "application/octet-stream", int64(len(body)), strings.NewReader(body), ChecksumSHA256([]byte(body))); err != nil {
		t.Fatal(err)
	}
}
