package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	for _, code := range []int{200, 503, 302} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/health" {
				t.Error("wrong health endpoint")
			}
			w.WriteHeader(code)
		}))
		_, port, _ := net.SplitHostPort(s.Listener.Addr().String())
		err := healthcheck(port)
		s.Close()
		if (err == nil) != (code == 200) {
			t.Fatalf("status %d: %v", code, err)
		}
	}
	for _, port := range []string{"0", "65536", "invalid"} {
		if healthcheck(port) == nil {
			t.Fatal("invalid port accepted")
		}
	}
}
