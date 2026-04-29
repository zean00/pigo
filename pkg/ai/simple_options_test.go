package ai

import "testing"

func TestFindEnvKeysAndGetEnvAPIKey(t *testing.T) {
	keys := FindEnvKeys("openai")
	if len(keys) == 0 || keys[0] != "OPENAI_API_KEY" {
		t.Fatalf("keys = %#v", keys)
	}

	t.Setenv("OPENAI_API_KEY", "test-key")
	if got := GetEnvAPIKey("openai"); got != "test-key" {
		t.Fatalf("api key = %q", got)
	}
}

func TestClampReasoning(t *testing.T) {
	if got := ClampReasoning("xhigh"); got != "high" {
		t.Fatalf("got %q", got)
	}
	if got := ClampReasoning("medium"); got != "medium" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildBaseOptionsAndAdjustMaxTokensForThinking(t *testing.T) {
	options := BuildBaseOptions(Model{MaxTokens: 64000}, ChatOptions{}, "api-key")
	if options.APIKey != "api-key" {
		t.Fatalf("api key = %q", options.APIKey)
	}
	if options.MaxTokens != 32000 {
		t.Fatalf("max tokens = %d", options.MaxTokens)
	}

	maxTokens, thinkingBudget := AdjustMaxTokensForThinking(4000, 10000, "medium", ThinkingBudgets{})
	if maxTokens != 10000 {
		t.Fatalf("max tokens = %d", maxTokens)
	}
	if thinkingBudget != 8192 {
		t.Fatalf("thinking budget = %d", thinkingBudget)
	}
}
