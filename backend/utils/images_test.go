package utils

import (
	"strings"
	"testing"
)

func TestSanitizeSVGAllowsStandardNamespace(t *testing.T) {
	body := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><path d="M0 0h10v10z"/></svg>`)
	clean, err := SanitizeSVG(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clean), "<svg") {
		t.Fatalf("unexpected sanitized svg: %s", clean)
	}
}

func TestSanitizeSVGBlocksScriptsAndExternalResources(t *testing.T) {
	cases := [][]byte{
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><image href="https://example.com/a.png"/></svg>`),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><path onclick="alert(1)"/></svg>`),
	}
	for _, body := range cases {
		if _, err := SanitizeSVG(body); err == nil {
			t.Fatalf("unsafe svg was accepted: %s", body)
		}
	}
}
