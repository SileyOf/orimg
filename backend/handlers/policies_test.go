package handlers

import (
	"strings"
	"testing"
)

func TestCleanGroupName(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "  创作者  ", want: "创作者", ok: true},
		{input: "A", want: "A", ok: false},
		{input: "  ", want: "", ok: false},
		{input: strings.Repeat("a", 81), want: strings.Repeat("a", 81), ok: false},
	}
	for _, test := range tests {
		got, ok := cleanGroupName(test.input)
		if got != test.want || ok != test.ok {
			t.Fatalf("cleanGroupName(%q) = (%q, %v), want (%q, %v)", test.input, got, ok, test.want, test.ok)
		}
	}
}
