package ai

import "testing"

func TestFindEnvKeysAndGetEnvAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	keys := FindEnvKeys("openai")
	if keys != nil {
		t.Fatalf("keys = %#v", keys)
	}

	t.Setenv("OPENAI_API_KEY", "test-key")
	keys = FindEnvKeys("openai")
	if len(keys) != 1 || keys[0] != "OPENAI_API_KEY" {
		t.Fatalf("keys = %#v", keys)
	}
	if got := GetEnvAPIKey("openai"); got != "test-key" {
		t.Fatalf("api key = %q", got)
	}
}

func TestFindEnvKeysExcludesAmbientCredentialSources(t *testing.T) {
	t.Setenv("AWS_PROFILE", "default")
	t.Setenv("AWS_ACCESS_KEY_ID", "access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	if keys := FindEnvKeys("amazon-bedrock"); keys != nil {
		t.Fatalf("bedrock keys = %#v", keys)
	}

	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/tmp/credentials.json")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
	t.Setenv("GOOGLE_CLOUD_API_KEY", "")
	if keys := FindEnvKeys("google-vertex"); keys != nil {
		t.Fatalf("vertex keys = %#v", keys)
	}

	t.Setenv("GOOGLE_CLOUD_API_KEY", "vertex-key")
	keys := FindEnvKeys("google-vertex")
	if len(keys) != 1 || keys[0] != "GOOGLE_CLOUD_API_KEY" {
		t.Fatalf("vertex api keys = %#v", keys)
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
	options := BuildBaseOptions(Model{
		MaxTokens: 64000,
		BaseURL:   "https://proxy.example.com",
		Headers:   map[string]string{"X-Model": "model", "X-Shared": "model"},
	}, ChatOptions{Headers: map[string]string{"X-Shared": "request"}}, "api-key")
	if options.APIKey != "api-key" {
		t.Fatalf("api key = %q", options.APIKey)
	}
	if options.BaseURL != "https://proxy.example.com" {
		t.Fatalf("base url = %q", options.BaseURL)
	}
	if options.Headers["X-Model"] != "model" || options.Headers["X-Shared"] != "request" {
		t.Fatalf("headers = %#v", options.Headers)
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
