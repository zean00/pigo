package ai

import "testing"

func TestIsContextOverflowMatchesErrorPatterns(t *testing.T) {
	result := NormalizedResult{
		Role:         "assistant",
		StopReason:   "error",
		ErrorMessage: "Your input exceeds the context window of this model",
	}
	if !IsContextOverflow(result, 0) {
		t.Fatal("expected overflow")
	}
}

func TestIsContextOverflowMatchesNoBody429(t *testing.T) {
	result := NormalizedResult{
		Role:         "assistant",
		StopReason:   "error",
		ErrorMessage: "429 status code (no body)",
	}
	if !IsContextOverflow(result, 0) {
		t.Fatal("expected overflow")
	}
}

func TestIsContextOverflowIgnoresRateLimitMessages(t *testing.T) {
	result := NormalizedResult{
		Role:         "assistant",
		StopReason:   "error",
		ErrorMessage: "Throttling error: Too many tokens, please wait before trying again.",
	}
	if IsContextOverflow(result, 0) {
		t.Fatal("did not expect overflow")
	}
}

func TestIsContextOverflowDetectsSilentOverflowFromUsage(t *testing.T) {
	result := NormalizedResult{
		Role:       "assistant",
		StopReason: "stop",
		Usage: &Usage{
			Input:     1000,
			CacheRead: 200,
		},
	}
	if !IsContextOverflow(result, 1100) {
		t.Fatal("expected silent overflow")
	}
}
