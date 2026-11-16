package installer

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zhuapic/config"
)

func fixtureConfig(t *testing.T) *config.Config {
	root := t.TempDir()
	return &config.Config{Port: "8080", BaseURL: "http://localhost:8080", DBTLS: "disabled", DBMaxOpen: 2, DBMaxIdle: 2,
		DBConnLifetime: time.Minute, JWTExpiry: time.Hour, CacheTTL: time.Hour, CleanupInterval: time.Hour,
		UploadDir: filepath.Join(root, "uploads"), CacheDir: filepath.Join(root, "cache"), AssetDir: filepath.Join(root, "assets"), DisableBackgroundWork: true}
}

func installRequest(s *Server, method, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://localhost:8080"+path, bytes.NewReader(b))
	r.RemoteAddr = "127.0.0.1:4321"
	r.Header.Set("Authorization", "Bearer "+s.Token)
	r.Header.Set("Origin", "http://localhost:8080")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestInstallationBoundary(t *testing.T) {
	s := &Server{Base: fixtureConfig(t), Token: strings.Repeat("a", 48)}
	for _, tc := range []struct {
		name, peer, host, origin, token string
		want                            int
	}{
		{"local", "127.0.0.1:5", "localhost:8080", "http://localhost:8080", s.Token, 200},
		{"missing code", "127.0.0.1:5", "localhost:8080", "http://localhost:8080", "", 401},
		{"cross origin", "127.0.0.1:5", "localhost:8080", "https://evil.test", s.Token, 403},
		{"remote http", "192.0.2.2:5", "example.test", "http://example.test", s.Token, 403},
		{"rebound host", "127.0.0.1:5", "example.test", "http://example.test", s.Token, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tc.host+"/api/install/status", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("X-Forwarded-Proto", "https")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("setup response may be cached")
			}
		})
	}
	s.mu.Lock()
	if w := installRequest(s, "GET", "/api/install/status", nil); w.Code != 409 {
		t.Fatal("concurrent setup admitted")
	}
	s.mu.Unlock()
	s.completed = true
	if w := installRequest(s, "GET", "/api/install/status", nil); w.Code != 410 {
		t.Fatal("completed setup reopened")
	}
	if w := installRequest(s, "GET", "/api/ready", nil); w.Code != 503 {
		t.Fatal("uninstalled site reported ready")
	}
}

func TestStrictInstallPayload(t *testing.T) {
	s := &Server{Base: fixtureConfig(t)}
	for _, body := range []string{`{"unknown":true}`, `{} {}`, strings.Repeat(" ", 33<<10) + `{}`} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		if _, _, err := s.request(w, r); err == nil {
			t.Fatal("invalid payload accepted")
		}
	}
}
