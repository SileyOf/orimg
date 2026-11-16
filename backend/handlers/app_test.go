package handlers

import (
	"net/http/httptest"
	"testing"
	"zhuapic/config"
)

func TestSanitizeDomains(t *testing.T) {
	got := sanitizeDomains("pic.example.com; https://cdn.example.com invalid/path pic.example.com", "https://img.example.com")
	want := "img.example.com,pic.example.com,cdn.example.com"
	if got != want {
		t.Fatalf("sanitizeDomains() = %q, want %q", got, want)
	}
}

func TestDefaultAccentColor(t *testing.T) {
	for _, color := range []string{"lime", "blue", "purple", "pink", "red", "orange", "yellow", "green", "graphite"} {
		if got := sanitizeSetting("default_accent_color", color, nil); got != color {
			t.Errorf("accent %q became %q", color, got)
		}
	}
	for _, value := range []string{"", "site", "#fff", "url(https://example.test)", "BLUE"} {
		if got := sanitizeSetting("default_accent_color", value, nil); got != "lime" {
			t.Errorf("invalid accent %q became %q", value, got)
		}
	}
	if !editableSettingKeys["default_accent_color"] {
		t.Fatal("default accent must be editable by admins")
	}
	for _, key := range publicSettingKeys {
		if key == "default_accent_color" {
			return
		}
	}
	t.Fatal("default accent must be available to public pages")
}

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "IMG.EXAMPLE.COM", want: "img.example.com"},
		{input: "http://localhost:8080", want: "localhost:8080"},
		{input: "bad host", want: ""},
		{input: "https://user@example.com", want: ""},
	}
	for _, test := range tests {
		if got := normalizeDomain(test.input); got != test.want {
			t.Errorf("normalizeDomain(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestSelectRequestBaseURL(t *testing.T) {
	baseURL := "https://img.example.com"
	domains := "img.example.com,pic.example.com"
	tests := []struct {
		name   string
		host   string
		scheme string
		want   string
	}{
		{name: "primary domain", host: "img.example.com", scheme: "https", want: "https://img.example.com"},
		{name: "secondary domain", host: "pic.example.com", scheme: "https", want: "https://pic.example.com"},
		{name: "untrusted host", host: "example.com", scheme: "https", want: baseURL},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := selectRequestBaseURL(baseURL, domains, test.host, test.scheme); got != test.want {
				t.Fatalf("selectRequestBaseURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClientIPOnlyTrustsExplicitProxy(t *testing.T) {
	app := &App{Cfg: &config.Config{TrustedProxies: "127.0.0.1/32,10.0.0.2/32"}}
	request := httptest.NewRequest("GET", "http://example.test", nil)
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("X-Forwarded-For", "203.0.113.8, 10.0.0.2")
	if got := app.clientIP(request); got != "203.0.113.8" {
		t.Fatalf("private proxy forwarded IP = %q", got)
	}

	request.RemoteAddr = "198.51.100.10:42000"
	if got := app.clientIP(request); got != "198.51.100.10" {
		t.Fatalf("public peer should ignore spoofed header, got %q", got)
	}
}
