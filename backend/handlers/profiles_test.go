package handlers

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"testing"
)

func TestAvatarNormalization(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 256, 256)))
	clean, err := normalizeAvatar(append(b.Bytes(), []byte("<script>bad metadata</script>")...))
	if err != nil || bytes.Contains(clean, []byte("script")) {
		t.Fatal("avatar was not re-encoded", err)
	}
	for _, body := range [][]byte{[]byte("<svg/>"), []byte("not an image")} {
		if _, err := normalizeAvatar(body); err == nil {
			t.Fatal("invalid avatar accepted")
		}
	}
	b.Reset()
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 513, 1)))
	if _, err := normalizeAvatar(b.Bytes()); err == nil {
		t.Fatal("oversized avatar accepted")
	}
}

func TestPersonalQuota(t *testing.T) {
	old := int64(25)
	for _, tc := range []struct {
		raw  string
		want *int64
		fail bool
	}{
		{"", &old, false}, {"null", nil, false}, {"0", new(int64), false}, {"-1", nil, true}, {"1.5", nil, true}, {`"10"`, nil, true}, {"1125899906842624", nil, true},
	} {
		got, err := parseUserQuota(json.RawMessage(tc.raw), &old)
		if (err != nil) != tc.fail {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if !tc.fail && ((got == nil) != (tc.want == nil) || got != nil && *got != *tc.want) {
			t.Fatalf("wrong quota for %s", tc.raw)
		}
	}
}
