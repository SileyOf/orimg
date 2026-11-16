package utils

import (
	"encoding/hex"
	"testing"
)

func TestPBKDF2Vectors(t *testing.T) {
	cases := []struct {
		password, salt string
		iterations, n  int
		want           string
	}{
		// RFC 7914 section 11.
		{"passwd", "salt", 1, 64, "55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc49ca9cccf179b645991664b39d77ef317c71b845b1e30bd509112041d3a19783"},
		{"Password", "NaCl", 80000, 64, "4ddcd8f60b98be21830cee5ef22701f9641a4418d04c0414aeff08876b34ab56a1d425a1225833549adb841b51c9b3176a272bdebba1d078478f62b397f33c8d"},
		// Output of the v0.5.0 implementation, kept so stored hashes stay valid.
		{"correct horse", "0123456789abcdef", 1000, 32, "70183c0f60ee9e0441f64efab334e17f97a17f2073f7dd5acba3d3f12af09383"},
	}
	for _, c := range cases {
		got := hex.EncodeToString(pbkdf2SHA256([]byte(c.password), []byte(c.salt), c.iterations, c.n))
		if got != c.want {
			t.Fatalf("pbkdf2(%q,%q,%d) = %s", c.password, c.salt, c.iterations, got)
		}
	}
}

func TestVerifyPasswordOrDummy(t *testing.T) {
	hash, err := HashPassword("secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPasswordOrDummy(hash, true, "secret-value") || VerifyPasswordOrDummy(hash, true, "wrong") {
		t.Fatal("known account verification changed")
	}
	if VerifyPasswordOrDummy("", false, "origin-timing-equalizer") {
		t.Fatal("missing account must never verify")
	}
}

func BenchmarkHashPassword(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = HashPassword("benchmark-password")
	}
}
