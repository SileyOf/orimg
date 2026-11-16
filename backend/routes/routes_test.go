package routes

import (
	"testing"

	"zhuapic/config"
)

func TestOriginAllowed(t *testing.T) {
	cfg := &config.Config{BaseURL: "https://img.example.com"}

	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "configured site", origin: "https://img.example.com", want: true},
		{name: "same request origin", origin: "https://IMG.EXAMPLE.COM", want: true},
		{name: "secondary domain", origin: "https://pic.example.com", want: true},
		{name: "untrusted site", origin: "https://example.com", want: false},
		{name: "invalid origin", origin: "javascript:alert(1)", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := originAllowed(cfg, test.origin, "img.example.com,pic.example.com"); got != test.want {
				t.Fatalf("originAllowed(%q) = %v, want %v", test.origin, got, test.want)
			}
		})
	}
}
