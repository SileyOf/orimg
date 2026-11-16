package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRequestBodyLimitAndDeadline(t *testing.T) {
	h := requestLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Error("missing bounded request context")
		}
		if _, err := io.ReadAll(r.Body); err == nil {
			t.Error("oversized JSON was accepted")
		}
	}))
	r := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(strings.Repeat("a", (1<<20)+1)))
	h.ServeHTTP(httptest.NewRecorder(), r)
}

func TestUploadConcurrencyLimit(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	h := requestLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release }))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/upload", nil))
		}()
	}
	for i := 0; i < 4; i++ {
		<-entered
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/upload", nil))
	if w.Code != 503 {
		t.Error("upload concurrency unbounded")
	}
	close(release)
	wg.Wait()
}

func TestForwardedHTTPSRequiresTrustedProxy(t *testing.T) {
	for _, trusted := range []string{"", "127.0.0.1/32"} {
		r := httptest.NewRequest("GET", "http://example.test/", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), trusted).ServeHTTP(w, r)
		if (w.Header().Get("Strict-Transport-Security") != "") != (trusted != "") {
			t.Fatal("untrusted HSTS scheme")
		}
	}
}
