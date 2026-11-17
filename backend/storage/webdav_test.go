package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"zhuapic/config"
	"zhuapic/models"
)

// fakeDAV behaves like common servers: PUT and MKCOL need an existing parent
// collection (409 otherwise) and every request needs Basic auth.
type fakeDAV struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
	calls []string
}

func newFakeDAV() *fakeDAV {
	return &fakeDAV{files: map[string][]byte{}, dirs: map[string]bool{"/dav": true}}
}

func (f *fakeDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok || user != "alice" || pass != "s3cret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.TrimRight(r.URL.Path, "/")
	f.calls = append(f.calls, r.Method+" "+p)
	switch r.Method {
	case "MKCOL":
		if f.dirs[p] {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !f.dirs[path.Dir(p)] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.dirs[p] = true
		w.WriteHeader(http.StatusCreated)
	case http.MethodPut:
		if !f.dirs[path.Dir(p)] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.files[p] = body
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		body, ok := f.files[p]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	case http.MethodDelete:
		if _, ok := f.files[p]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.files, p)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestWebDAVClientRoundTrip(t *testing.T) {
	dav := newFakeDAV()
	server := httptest.NewServer(dav)
	defer server.Close()
	client := NewWebDAVClient(WebDAVConfig{URL: server.URL + "/dav/", Username: "alice", Password: "s3cret"})
	ctx := context.Background()
	body := []byte("image bytes")
	key := "images/20261117/a b.png"
	if err := client.PutReader(ctx, key, "image/png", int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if got := dav.files["/dav/images/20261117/a b.png"]; !bytes.Equal(got, body) {
		t.Fatalf("stored %q", got)
	}
	// Collections are created once per process, then cached.
	before := len(dav.calls)
	if err := client.PutReader(ctx, "images/20261117/c.png", "image/png", 1, strings.NewReader("c")); err != nil {
		t.Fatal(err)
	}
	if extra := dav.calls[before:]; len(extra) != 1 || !strings.HasPrefix(extra[0], "PUT ") {
		t.Fatalf("expected a single PUT, got %v", extra)
	}
	got, err := client.Get(ctx, key)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("get: %q %v", got, err)
	}
	if err = client.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err = client.Delete(ctx, key); err != nil {
		t.Fatalf("deleting a missing object must succeed: %v", err)
	}
	if _, err = client.Get(ctx, key); err == nil {
		t.Fatal("expected missing object error")
	}
}

func TestWebDAVClientRejectsBadAuthAndKeys(t *testing.T) {
	server := httptest.NewServer(newFakeDAV())
	defer server.Close()
	client := NewWebDAVClient(WebDAVConfig{URL: server.URL + "/dav", Username: "alice", Password: "wrong"})
	err := client.PutReader(context.Background(), "x/y.png", "image/png", 1, strings.NewReader("y"))
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("expected a 401 without leaking the password, got %v", err)
	}
	for _, key := range []string{"", "../escape.png", "a//b.png", "a/./b.png"} {
		if _, err := client.objectURL(key); err == nil {
			t.Fatalf("key %q accepted", key)
		}
	}
	if NewWebDAVClient(WebDAVConfig{URL: "ftp://example.com"}).Configured() {
		t.Fatal("non-HTTP URL accepted")
	}
}

func TestWebDAVDoesNotFollowRedirects(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			leaked = true
		}
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := NewWebDAVClient(WebDAVConfig{URL: redirect.URL, Username: "alice", Password: "s3cret"})
	if _, err := client.Get(context.Background(), "a.png"); err == nil || leaked {
		t.Fatalf("redirect followed (leaked=%v, err=%v)", leaked, err)
	}
}

func TestManagerWebDAVSaveReadDelete(t *testing.T) {
	dav := newFakeDAV()
	server := httptest.NewServer(dav)
	defer server.Close()
	dir := t.TempDir()
	manager := NewManager(&config.Config{UploadDir: dir + "/uploads", CacheDir: dir + "/cache"})
	st := &Storage{Name: "NAS", Provider: "webdav", Config: StorageConfig{Endpoint: server.URL + "/dav/", Prefix: "/origin/", AccessKey: "alice", SecretKey: "s3cret"}}
	if err := st.Validate(); err == nil {
		t.Fatal("absolute prefix accepted")
	}
	st.Config.Prefix = "origin/"
	if err := st.Validate(); err != nil || st.Config.Prefix != "origin" || st.Config.Endpoint != server.URL+"/dav" {
		t.Fatalf("validation: %v %+v", err, st.Config)
	}
	opts := st.Options(manager.DefaultOptions())
	if !manager.Configured("webdav", opts) || st.Mode() != "webdav" || opts.S3.Endpoint != "" {
		t.Fatal("webdav storage not configured")
	}
	image := &models.Image{PublicName: "abc.png", MimeType: "image/png", CreatedAt: time.Now()}
	body := []byte("png-data")
	if err := manager.SaveReader(context.Background(), opts, "webdav", image, bytes.NewReader(body), int64(len(body)), ""); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(image.StorageKey, "origin/") || !strings.HasSuffix(image.StorageKey, "/abc.png") {
		t.Fatalf("unexpected key %q", image.StorageKey)
	}
	reader, err := manager.Open(context.Background(), opts, "webdav", image.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	reader.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("read: %q", got)
	}
	if err = manager.DeleteObject(context.Background(), opts, "webdav", image.StorageKey); err != nil || len(dav.files) != 0 {
		t.Fatalf("delete: %v, remaining %d", err, len(dav.files))
	}
	if _, err = manager.Open(context.Background(), opts, "webdav", image.StorageKey); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing object should be ErrObjectNotFound, got %v", err)
	}
}
