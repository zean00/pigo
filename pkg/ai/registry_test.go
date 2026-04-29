package ai

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestResolveProviderSupportsAliasesAndCaseInsensitiveNames(t *testing.T) {
	for _, name := range []string{
		"openai",
		"OPENAI",
		"deepseek",
		"Deep-Seek",
		"kimi coding",
		"KIMI_CODING",
		"amazon-bedrock",
	} {
		if _, err := ResolveProvider(name); err != nil {
			t.Fatalf("ResolveProvider(%q) = %v", name, err)
		}
	}
}

func TestProviderWithOpenAICompatibilitySupportsFlow(t *testing.T) {
	provider, err := ResolveProvider("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.Complete(context.Background(), CompletionRequest{Provider: "deepseek", Model: "x", Messages: nil}); err == nil {
		t.Fatal("expected request failure without API key")
	}
}

func TestAnthropicProviderRequiresCredentials(t *testing.T) {
	provider, err := ResolveProvider("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = provider.Complete(context.Background(), CompletionRequest{Provider: "anthropic", Model: "dummy"})
	if err == nil {
		t.Fatal("expected missing credential error")
	}
	if !strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProviderCatalogCoversKnownPiProviders(t *testing.T) {
	expectedDefaults := map[string]string{
		"amazon-bedrock":         "us.anthropic.claude-opus-4-6-v1",
		"anthropic":              "claude-opus-4-7",
		"google":                 "gemini-3.1-pro-preview",
		"google-gemini-cli":      "gemini-3.1-pro-preview",
		"google-antigravity":     "gemini-3.1-pro-high",
		"google-vertex":          "gemini-3.1-pro-preview",
		"openai":                 "gpt-5.4",
		"azure-openai-responses": "gpt-5.4",
		"openai-codex":           "gpt-5.5",
		"deepseek":               "deepseek-v4-pro",
		"github-copilot":         "gpt-5.4",
		"xai":                    "grok-4.20-0309-reasoning",
		"groq":                   "openai/gpt-oss-120b",
		"cerebras":               "zai-glm-4.7",
		"openrouter":             "moonshotai/kimi-k2.6",
		"vercel-ai-gateway":      "zai/glm-5.1",
		"zai":                    "glm-5.1",
		"mistral":                "devstral-medium-latest",
		"minimax":                "MiniMax-M2.7",
		"minimax-cn":             "MiniMax-M2.7",
		"huggingface":            "moonshotai/Kimi-K2.6",
		"fireworks":              "accounts/fireworks/models/kimi-k2p6",
		"opencode":               "kimi-k2.6",
		"opencode-go":            "kimi-k2.6",
		"kimi-coding":            "kimi-for-coding",
	}

	for provider := range expectedDefaults {
		spec, ok := ProviderSpecForProvider(provider)
		if !ok {
			t.Fatalf("missing provider spec: %s", provider)
		}
		if got := strings.TrimSpace(spec.DefaultModel); got != expectedDefaults[provider] {
			t.Fatalf("default model for %s = %q, want %q", provider, got, expectedDefaults[provider])
		}
	}
}

func TestProviderCatalogKnownProviderEnvKeys(t *testing.T) {
	spec, ok := ProviderSpecForProvider("fireworks")
	if !ok {
		t.Fatal("missing provider spec for fireworks")
	}
	if !slices.Contains(spec.EnvKeys, "FIREWORKS_API_KEY") {
		t.Fatalf("fireworks env keys = %#v", spec.EnvKeys)
	}

	spec, ok = ProviderSpecForProvider("amazon-bedrock")
	if !ok {
		t.Fatal("missing provider spec for amazon-bedrock")
	}
	if !slices.Contains(spec.EnvKeys, "AWS_PROFILE") {
		t.Fatalf("amazon-bedrock env keys = %#v", spec.EnvKeys)
	}

	spec, ok = ProviderSpecForProvider("anthropic")
	if !ok {
		t.Fatal("missing provider spec for anthropic")
	}
	if !slices.Contains(spec.EnvKeys, "ANTHROPIC_OAUTH_TOKEN") {
		t.Fatalf("anthropic env keys = %#v", spec.EnvKeys)
	}

	spec, ok = ProviderSpecForProvider("google-vertex")
	if !ok {
		t.Fatal("missing provider spec for google-vertex")
	}
	if !slices.Contains(spec.EnvKeys, "GOOGLE_CLOUD_API_KEY") {
		t.Fatalf("google-vertex env keys = %#v", spec.EnvKeys)
	}

	spec, ok = ProviderSpecForProvider("github-copilot")
	if !ok {
		t.Fatal("missing provider spec for github-copilot")
	}
	expectedGitHubKeys := []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}
	for _, key := range expectedGitHubKeys {
		if !slices.Contains(spec.EnvKeys, key) {
			t.Fatalf("github-copilot env keys = %#v, missing %s", spec.EnvKeys, key)
		}
	}
}

func TestProviderAPIKeyReturnsGoogleVertexEnvKeyBeforeADCMarker(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_API_KEY", "vertex-key")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")

	apiKey, ok := ProviderAPIKey("google-vertex")
	if !ok {
		t.Fatal("expected google-vertex API key")
	}
	if apiKey != "vertex-key" {
		t.Fatalf("apiKey = %q, want %q", apiKey, "vertex-key")
	}
}
