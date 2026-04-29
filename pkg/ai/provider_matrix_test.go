package ai

import (
	"strings"
	"testing"
)

func TestProviderCatalogModesMatchRuntimeBehavior(t *testing.T) {
	for name, spec := range providerSpecs {
		name := name
		spec := spec

		t.Run(name, func(t *testing.T) {
			t.Helper()

			for _, envKey := range spec.EnvKeys {
				t.Setenv(envKey, "")
			}

			// Clear ambient credential indicators that could otherwise short-circuit catalog checks.
			t.Setenv("AWS_PROFILE", "")
			t.Setenv("AWS_ACCESS_KEY_ID", "")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "")
			t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
			t.Setenv("AWS_BEDROCK_SKIP_AUTH", "")
			t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
			t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")
			t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
			t.Setenv("GOOGLE_CLOUD_API_KEY", "")
			t.Setenv("GOOGLE_CLOUD_PROJECT", "")
			t.Setenv("GCLOUD_PROJECT", "")
			t.Setenv("GOOGLE_CLOUD_LOCATION", "")
			t.Setenv("OPENAI_BASE_URL", "")

			// Avoid any accidental external calls: implemented providers should fail due
			// to missing credentials, while unsupported providers should fail before
			// network.
			provider, err := ResolveProvider(name)
			if err != nil {
				t.Fatalf("resolve provider %q = %v", name, err)
			}

			model := spec.DefaultModel
			if model == "" {
				model = "model-id"
			}
			_, _, err = provider.Complete(
				t.Context(),
				CompletionRequest{
					Provider: name,
					Model:    model,
					Messages: []Message{{Role: "user", Content: "ping"}},
					Options:  ChatOptions{},
				},
			)
			if err == nil {
				t.Fatalf("expected provider %q to return an error", name)
			}

			if spec.Mode == providerModeOpenAI || spec.Mode == providerModeBedrock || spec.Mode == providerModeAnthropic || spec.Mode == providerModeMistral || spec.Mode == providerModeGoogle || spec.Mode == providerModeGoogleCLI || spec.Mode == providerModeGoogleVertex || spec.Mode == providerModeCodex {
				if !strings.Contains(err.Error(), "missing API key") {
					t.Fatalf("implemented provider %q error = %q", name, err)
				}
				return
			}

			if !strings.Contains(err.Error(), "not yet implemented") {
				t.Fatalf("unsupported provider %q error = %q", name, err)
			}
		})
	}
}
