package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// WebDAVConfig points at a collection (directory) on a WebDAV server, such as
// Nextcloud, Synology, Alist or rclone serve. Objects are stored below it.
type WebDAVConfig struct {
	URL      string
	Username string
	Password string
}

type WebDAVClient struct {
	cfg        WebDAVConfig
	base       *url.URL
	httpClient *http.Client
}

// Collections already created on a server, so uploads skip repeated MKCOLs.
var webdavCollections sync.Map

func NewWebDAVClient(cfg WebDAVConfig) *WebDAVClient {
	base, _ := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.URL), "/"))
	return &WebDAVClient{cfg: cfg, base: base, httpClient: &http.Client{
		Timeout: 5 * time.Minute,
		// Redirects would replay credentials to another location.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (w *WebDAVClient) Configured() bool {
	return w.base != nil && (w.base.Scheme == "https" || w.base.Scheme == "http") && w.base.Host != ""
}

func (w *WebDAVClient) objectURL(key string) (string, error) {
	if !w.Configured() {
		return "", errors.New("webdav is not configured")
	}
	key = strings.Trim(key, "/")
	if key == "" {
		return "", errors.New("webdav object key is required")
	}
	parts := strings.Split(key, "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("invalid webdav object key")
		}
		parts[i] = url.PathEscape(part)
	}
	u := *w.base
	u.Path = strings.TrimRight(w.base.Path, "/") + "/" + key
	u.RawPath = strings.TrimRight(w.base.EscapedPath(), "/") + "/" + strings.Join(parts, "/")
	return u.String(), nil
}

func (w *WebDAVClient) request(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if w.cfg.Username != "" || w.cfg.Password != "" {
		req.SetBasicAuth(w.cfg.Username, w.cfg.Password)
	}
	return req, nil
}

func (w *WebDAVClient) do(req *http.Request) (*http.Response, error) {
	resp, err := w.httpClient.Do(req)
	if err != nil {
		// Never echo the URL with any credentials attached.
		return nil, fmt.Errorf("webdav %s failed: %w", req.Method, errors.Unwrap(err))
	}
	return resp, nil
}

func webdavStatusError(op string, resp *http.Response) error {
	message, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	text := strings.TrimSpace(string(message))
	if strings.HasPrefix(text, "<") {
		text = "" // XML/HTML error bodies are noise in the admin UI
	}
	return fmt.Errorf("webdav %s failed: %s %s", op, resp.Status, text)
}

// ensureCollections creates every parent directory of key, top-down.
func (w *WebDAVClient) ensureCollections(ctx context.Context, key string) error {
	parts := strings.Split(strings.Trim(key, "/"), "/")
	for i := 1; i < len(parts); i++ {
		dir := strings.Join(parts[:i], "/")
		target, err := w.objectURL(dir)
		if err != nil {
			return err
		}
		target += "/"
		cacheKey := w.cfg.Username + "\x00" + target
		if _, ok := webdavCollections.Load(cacheKey); ok {
			continue
		}
		req, err := w.request(ctx, "MKCOL", target, nil)
		if err != nil {
			return err
		}
		resp, err := w.do(req)
		if err != nil {
			return err
		}
		// 405 Method Not Allowed: the collection already exists.
		switch resp.StatusCode {
		case http.StatusCreated, http.StatusOK, http.StatusNoContent, http.StatusMethodNotAllowed:
			resp.Body.Close()
			webdavCollections.Store(cacheKey, true)
		default:
			err = webdavStatusError("mkcol", resp)
			resp.Body.Close()
			return err
		}
	}
	return nil
}

func (w *WebDAVClient) PutReader(ctx context.Context, key, contentType string, size int64, body io.Reader) error {
	target, err := w.objectURL(key)
	if err != nil {
		return err
	}
	if err = w.ensureCollections(ctx, key); err != nil {
		return err
	}
	req, err := w.request(ctx, http.MethodPut, target, body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := w.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return webdavStatusError("put", resp)
	}
	return nil
}

func (w *WebDAVClient) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	target, err := w.objectURL(key)
	if err != nil {
		return nil, err
	}
	req, err := w.request(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, webdavStatusError("get", resp)
	}
	return resp.Body, nil
}

func (w *WebDAVClient) Get(ctx context.Context, key string) ([]byte, error) {
	body, err := w.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

func (w *WebDAVClient) Delete(ctx context.Context, key string) error {
	target, err := w.objectURL(key)
	if err != nil {
		return err
	}
	req, err := w.request(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	resp, err := w.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || (resp.StatusCode >= 200 && resp.StatusCode <= 299) {
		return nil
	}
	return webdavStatusError("delete", resp)
}
