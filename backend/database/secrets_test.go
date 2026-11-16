package database

import (
	"encoding/json"
	"testing"
	"zhuapic/utils"
)

func TestVerifyEncryptedValue(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	encrypted, err := utils.EncryptSecret("test-only-secret", key)
	if err != nil {
		t.Fatal(err)
	}
	nested, _ := json.Marshal(map[string]any{"nested": []any{map[string]string{"secret": encrypted}}})
	for _, value := range []string{encrypted, string(nested)} {
		if err := verifyEncryptedValue(value, key); err != nil {
			t.Fatal(err)
		}
		if err := verifyEncryptedValue(value, "wrong-key"); err == nil {
			t.Fatal("wrong key accepted")
		}
	}
}
