package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoogleVertexProviderUsesAPIKeyFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
		if req.URL.Path != "/v1/projects/test-project/locations/us-central1/publishers/google/models/gemini-vertex:generateContent" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.URL.Query().Get("key") != "vertex-key" {
			t.Fatalf("api key query = %q", req.URL.Query().Get("key"))
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["contents"]; !ok {
			t.Fatal("missing contents")
		}

		_, _ = fmt.Fprint(w, `{
			"candidates": [{
				"content": {
					"role": "model",
					"parts": [{"text": "ok"}]
				},
				"finishReason": "STOP"
			}],
			"usageMetadata": {
				"promptTokenCount": 4,
				"candidatesTokenCount": 2,
				"totalTokenCount": 6
			}
		}`)
	}))
	defer server.Close()

	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")

	provider := GoogleVertexProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "google-vertex",
		Model:    "gemini-vertex",
		Options: ChatOptions{
			APIKey:  "vertex-key",
			BaseURL: server.URL,
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" {
		t.Fatalf("text = %q", result.Text)
	}
}

func TestGoogleVertexProviderRejectsADCMarker(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")

	provider := GoogleVertexProvider()
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "google-vertex",
		Model:    "gemini-vertex",
		Options: ChatOptions{
			APIKey: googleVertexAuthenticatedMarker,
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ADC credentials are not supported") {
		t.Fatalf("error = %q", err)
	}
}
