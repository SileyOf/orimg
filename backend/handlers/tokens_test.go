package handlers

import "testing"

func TestHashAPITokenIsDeterministicAndDoesNotExposeRawToken(t *testing.T) {
	raw := "zpic_example-token-value"
	first := hashAPIToken(raw)
	second := hashAPIToken(raw)
	if first != second {
		t.Fatal("token hash must be deterministic")
	}
	if first == raw || len(first) != 64 {
		t.Fatalf("unexpected token hash %q", first)
	}
	if first == hashAPIToken(raw+"-other") {
		t.Fatal("different tokens must not share a hash")
	}
}
