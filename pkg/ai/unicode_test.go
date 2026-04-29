package ai

import "testing"

func TestSanitizeSurrogates(t *testing.T) {
	input := "ok" + string([]byte{0xED, 0xA0, 0xBD}) + "done"
	got := SanitizeSurrogates(input)
	if got != "okdone" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeSurrogatesPreservesValidUnicode(t *testing.T) {
	input := "hello 🙈 world"
	got := SanitizeSurrogates(input)
	if got != input {
		t.Fatalf("got %q want %q", got, input)
	}
}
