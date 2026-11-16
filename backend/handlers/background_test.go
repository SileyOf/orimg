package handlers

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestBackgroundSettings(t *testing.T) {
	for input, want := range map[string]string{
		"": "", "/assets/site-background-abc.png": "/assets/site-background-abc.png",
		"https://example.com/bg.webp?v=1": "https://example.com/bg.webp?v=1",
		"http://example.com/bg.jpg":       "", "//example.com/bg.png": "", "javascript:alert(1)": "",
		"data:image/svg+xml,test": "", "https://user:pass@example.com/a": "", "/assets/../.env": "",
		"/assets/%2e%2e/.env": "", "https://example.com/a\n": "", "file:///etc/passwd": "",
	} {
		if got := sanitizeBackgroundURL(input); got != want {
			t.Errorf("%q = %q, want %q", input, got, want)
		}
	}
	if got := sanitizeSetting("background_overlay", "0", nil); got != "75" {
		t.Fatal(got)
	}
	if got := sanitizeSetting("background_overlay", "invalid", nil); got != "82" {
		t.Fatal(got)
	}
	if got := sanitizeSetting("background_blur", "100", nil); got != "20" {
		t.Fatal(got)
	}
}

func TestBackgroundImageValidation(t *testing.T) {
	var body bytes.Buffer
	if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	if ext, err := backgroundExtension(body.Bytes()); err != nil || ext != ".png" {
		t.Fatalf("%q %v", ext, err)
	}
	for _, invalid := range [][]byte{[]byte("<svg onload='alert(1)'></svg>"), []byte("not an image"), body.Bytes()[:10]} {
		if _, err := backgroundExtension(invalid); err == nil {
			t.Fatal("accepted invalid background")
		}
	}
}
