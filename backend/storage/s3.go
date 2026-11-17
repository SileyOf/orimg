package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"zhuapic/config"
)

type S3Client struct {
	cfg        config.S3Config
	httpClient *http.Client
}

// remoteTransport is shared by S3 and WebDAV clients. Short connect and
// header timeouts let an unreachable storage fail fast instead of tying up a
// worker for minutes; large bodies are bounded by the caller's context.
var remoteTransport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
	ExpectContinueTimeout: time.Second,
	MaxIdleConnsPerHost:   8,
	IdleConnTimeout:       90 * time.Second,
	ForceAttemptHTTP2:     true,
}

func NewS3Client(cfg config.S3Config) *S3Client {
	return &S3Client{
		cfg:        cfg,
		httpClient: &http.Client{Transport: remoteTransport, Timeout: 30 * time.Minute},
	}
}

func (s *S3Client) Configured() bool {
	return s.cfg.Endpoint != "" && s.cfg.Bucket != "" && s.cfg.AccessKey != "" && s.cfg.SecretKey != ""
}

func (s *S3Client) PutObject(ctx context.Context, key string, contentType string, body []byte) error {
	return s.PutObjectReader(ctx, key, contentType, int64(len(body)), bytes.NewReader(body), sha256Hex(body))
}

func (s *S3Client) PutObjectReader(ctx context.Context, key string, contentType string, size int64, body io.Reader, payloadHash string) error {
	if !s.Configured() {
		return errors.New("s3 is not configured")
	}
	u, err := s.objectURL(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = size
	s.signHash(req, payloadHash)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("s3 put failed: %s %s", resp.Status, strings.TrimSpace(string(message)))
	}
	return nil
}

func (s *S3Client) GetObject(ctx context.Context, key string) ([]byte, error) {
	body, err := s.OpenObject(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

func (s *S3Client) OpenObject(ctx context.Context, key string) (io.ReadCloser, error) {
	if !s.Configured() {
		return nil, errors.New("s3 is not configured")
	}
	u, err := s.objectURL(key)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, nil)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: s3 %s", ErrObjectNotFound, key)
		}
		return nil, fmt.Errorf("s3 get failed: %s %s", resp.Status, strings.TrimSpace(string(message)))
	}
	return resp.Body, nil
}

func (s *S3Client) DeleteObject(ctx context.Context, key string) error {
	if !s.Configured() {
		return errors.New("s3 is not configured")
	}
	u, err := s.objectURL(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	s.sign(req, nil)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("s3 delete failed: %s %s", resp.Status, strings.TrimSpace(string(message)))
	}
	return nil
}

func (s *S3Client) objectURL(key string) (*url.URL, error) {
	base, err := url.Parse(s.cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	cleanKey := path.Clean("/" + key)
	if cleanKey == "/" {
		return nil, errors.New("invalid s3 key")
	}

	if s.cfg.ForcePathStyle {
		base.Path = joinURLPath(base.Path, s.cfg.Bucket, strings.TrimPrefix(cleanKey, "/"))
		return base, nil
	}

	base.Host = s.cfg.Bucket + "." + base.Host
	base.Path = joinURLPath(base.Path, strings.TrimPrefix(cleanKey, "/"))
	return base, nil
}

func (s *S3Client) sign(req *http.Request, payload []byte) {
	s.signHash(req, sha256Hex(payload))
}

func (s *S3Client) signHash(req *http.Request, payloadHash string) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeaders, canonicalHeaders := canonicalSignedHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		escapePath(req.URL.EscapedPath()),
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + s.cfg.Region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(s.cfg.SecretKey, dateStamp, s.cfg.Region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	authorization := "AWS4-HMAC-SHA256 Credential=" + s.cfg.AccessKey + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature
	req.Header.Set("Authorization", authorization)
}

func canonicalSignedHeaders(req *http.Request) (string, string) {
	headers := map[string]string{}
	for key, values := range req.Header {
		lower := strings.ToLower(key)
		if lower == "authorization" {
			continue
		}
		headers[lower] = strings.Join(values, ",")
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var canonical strings.Builder
	for _, key := range keys {
		canonical.WriteString(key)
		canonical.WriteByte(':')
		canonical.WriteString(strings.TrimSpace(headers[key]))
		canonical.WriteByte('\n')
	}
	return strings.Join(keys, ";"), canonical.String()
}

func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func joinURLPath(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			clean = append(clean, part)
		}
	}
	return "/" + strings.Join(clean, "/")
}

func escapePath(raw string) string {
	parts := strings.Split(raw, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	escaped := strings.Join(parts, "/")
	if escaped == "" {
		return "/"
	}
	return escaped
}
