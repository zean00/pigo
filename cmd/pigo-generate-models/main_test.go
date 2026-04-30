package main

import (
	"encoding/json"
	"testing"
)

func TestGenerateModelsJSONConvertsTSObject(t *testing.T) {
	input := `
		// generated
		export const MODELS = {
			"provider-a": {
				"model-1": {
					id: "model-1",
					name: "Model One",
					api: "openai-completions",
					provider: "provider-a",
					baseUrl: "https://api.example.com",
					reasoning: false,
					input: ["text","image"],
					cost: {
						input: 0.1,
						output: 0.2,
						cacheRead: 0.0,
						cacheWrite: 0.0,
					},
					contextWindow: 2048,
					maxTokens: 1024,
				} satisfies Model<"openai-completions">,
			},
		};
	`

	result, err := GenerateModelsJSON(input)
	if err != nil {
		t.Fatalf("GenerateModelsJSON() error: %v", err)
	}

	var catalog map[string]map[string]struct {
		ID        string `json:"id"`
		Provider  string `json:"provider"`
		Context   int    `json:"contextWindow"`
		MaxTokens int    `json:"maxTokens"`
		Reasoning bool   `json:"reasoning"`
		BaseURL   string `json:"baseUrl"`
		API       string `json:"api"`
		CostInput int    `json:"-"`
	}
	if err := json.Unmarshal(result, &catalog); err != nil {
		t.Fatalf("json.Unmarshal(result) error: %v", err)
	}

	provider, ok := catalog["provider-a"]
	if !ok {
		t.Fatalf("expected provider-a in output")
	}
	model, ok := provider["model-1"]
	if !ok {
		t.Fatalf("expected model-1 in output")
	}
	if model.ID != "model-1" {
		t.Fatalf("expected model id = model-1, got %q", model.ID)
	}
	if model.Provider != "provider-a" {
		t.Fatalf("expected provider = provider-a, got %q", model.Provider)
	}
	if model.Context != 2048 {
		t.Fatalf("expected contextWindow = 2048, got %d", model.Context)
	}
	if model.MaxTokens != 1024 {
		t.Fatalf("expected maxTokens = 1024, got %d", model.MaxTokens)
	}
	if model.Reasoning {
		t.Fatalf("expected reasoning = false")
	}
	if model.BaseURL != "https://api.example.com" {
		t.Fatalf("unexpected baseUrl = %q", model.BaseURL)
	}
	if model.API != "openai-completions" {
		t.Fatalf("unexpected api = %q", model.API)
	}
}

func TestGenerateModelsJSONRequiresModelsExport(t *testing.T) {
	_, err := GenerateModelsJSON("export const OTHER = {}")
	if err == nil {
		t.Fatalf("expected missing MODELS export error")
	}
}
