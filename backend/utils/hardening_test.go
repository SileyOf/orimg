package utils

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitProxyTrust(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.test/", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 192.0.2.8")
	r.Header.Set("X-Forwarded-Proto", "https")
	if ClientIP(r, "") != "10.0.0.2" || RequestScheme(r, "") != "http" {
		t.Fatal("implicitly trusted private peer")
	}
	if ClientIP(r, "10.0.0.2/32") != "192.0.2.8" {
		t.Fatal("trusted forged leftmost address")
	}
	if ClientIP(r, "10.0.0.2/32,192.0.2.8") != "198.51.100.7" || RequestScheme(r, "10.0.0.2") != "https" {
		t.Fatal("trusted chain not resolved")
	}
	if SecureSetupRequest(r, "") {
		t.Fatal("cleartext remote setup allowed")
	}
	r.RemoteAddr = "127.0.0.1:1234"
	if SecureSetupRequest(r, "") {
		t.Fatal("loopback peer with public Host allowed")
	}
	r.Host = "127.0.0.1:8080"
	if !SecureSetupRequest(r, "") {
		t.Fatal("local setup denied")
	}
	r.Header.Set("X-Forwarded-For", "invalid")
	if ClientIP(r, "127.0.0.1") != "127.0.0.1" {
		t.Fatal("invalid forwarding accepted")
	}
}

func TestGIFFrameProbeAndDecodeBudget(t *testing.T) {
	picture := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	for _, frames := range []int{1, 2, 50} {
		g := &gif.GIF{}
		for i := 0; i < frames; i++ {
			g.Image = append(g.Image, picture)
			g.Delay = append(g.Delay, 1)
		}
		var b bytes.Buffer
		if err := gif.EncodeAll(&b, g); err != nil {
			t.Fatal(err)
		}
		if IsAnimatedImage(b.Bytes(), "image/gif") != (frames > 1) {
			t.Fatalf("frame count %d", frames)
		}
		for n := 0; n < b.Len(); n++ {
			// Truncated files must never panic, regardless of which block is cut.
			_ = IsAnimatedImage(b.Bytes()[:n], "image/gif")
		}
		if frames == 1 {
			body := append([]byte(nil), b.Bytes()...)
			body[6], body[7], body[8], body[9] = 255, 255, 255, 255
			if _, _, _, err := ThumbnailJPEG(body, 320); err == nil {
				t.Fatal("huge decoded canvas accepted")
			}
			if _, _, _, err := ThumbnailJPEG(b.Bytes(), 320); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestPersistentSiteAssets(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSiteAsset(dir, "site-logo-fixture.png", []byte("fixture")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../site-logo-x", "site-logo-../x", "app.js"} {
		if SaveSiteAsset(dir, name, nil) == nil {
			t.Fatal("unsafe name accepted", name)
		}
	}
	r := httptest.NewRequest("GET", "http://localhost/assets/site-logo-fixture.png", nil)
	w := httptest.NewRecorder()
	if !ServeSiteAsset(dir, w, r) || w.Code != 200 || w.Body.String() != "fixture" {
		t.Fatal("asset unavailable")
	}
	info, _ := os.Stat(filepath.Join(dir, "site-logo-fixture.png"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("unexpected asset permissions")
	}
}

func TestPreserveLegacyAssetsWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "static", "assets")
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "site-logo-fixture.png"), []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "app.js"), []byte("application"), 0600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	dest := filepath.Join(root, "persistent")
	if err = PreserveSiteAssets(dest); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(dest, "site-logo-fixture.png")); err != nil || string(body) != "legacy" {
		t.Fatal("legacy asset not preserved", err)
	}
	if _, err = os.Stat(filepath.Join(dest, "app.js")); !os.IsNotExist(err) {
		t.Fatal("application code copied as data")
	}
	if err = SaveSiteAsset(dest, "site-logo-fixture.png", []byte("newer")); err != nil {
		t.Fatal(err)
	}
	if err = PreserveSiteAssets(dest); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(dest, "site-logo-fixture.png")); string(body) != "newer" {
		t.Fatal("persistent asset overwritten")
	}
}
