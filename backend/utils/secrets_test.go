package utils

import (
	"strings"
	"testing"
)

func TestSecretRoundTripAndPlaintextCompatibility(t *testing.T) {
	encrypted, err := EncryptSecret("s3-secret", "stable-master-key")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == "s3-secret" || !strings.HasPrefix(encrypted, encryptedSecretPrefix) {
		t.Fatalf("secret was not encrypted: %q", encrypted)
	}
	plain, err := DecryptSecret(encrypted, "stable-master-key")
	if err != nil || plain != "s3-secret" {
		t.Fatalf("round trip failed: plain=%q err=%v", plain, err)
	}
	legacy, err := DecryptSecret("legacy-plaintext", "stable-master-key")
	if err != nil || legacy != "legacy-plaintext" {
		t.Fatalf("legacy plaintext compatibility failed: plain=%q err=%v", legacy, err)
	}
}
