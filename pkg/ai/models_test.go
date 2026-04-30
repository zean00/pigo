package ai

import (
	"strings"
	"testing"
)

func TestModelRegistryDefaults(t *testing.T) {
	ResetModels()

	providers := GetProviders()
	if len(providers) == 0 {
		t.Fatal("expected default providers")
	}

	models := GetModels("openai")
	if len(models) == 0 {
		t.Fatal("expected openai models")
	}
	if models[0].Provider != "openai" {
		t.Fatalf("provider = %q", models[0].Provider)
	}

	model, ok := GetModel("openai", "gpt-5.4")
	if !ok {
		t.Fatal("expected default openai model")
	}
	if model.API != "openai-responses" && model.API != "openai-completions" {
		t.Fatalf("api = %q", model.API)
	}

	githubCopilotModel, ok := GetModel("github-copilot", "claude-haiku-4.5")
	if !ok {
		t.Fatal("expected github-copilot model")
	}
	if githubCopilotModel.Headers == nil || githubCopilotModel.Headers["User-Agent"] == "" {
		t.Fatalf("missing github-copilot header: %#v", githubCopilotModel.Headers)
	}
	if githubCopilotModel.Compat == nil || githubCopilotModel.Compat["supportsEagerToolInputStreaming"] == nil {
		t.Fatalf("missing github-copilot compat: %#v", githubCopilotModel.Compat)
	}
}

func TestRegisterModelAndCalculateCost(t *testing.T) {
	ResetModels()
	RegisterModel(Model{
		ID:       "custom-1",
		Name:     "Custom One",
		Provider: "openai",
		API:      "openai-responses",
		Cost: ModelCost{
			Input:      2,
			Output:     4,
			CacheRead:  1,
			CacheWrite: 3,
		},
	})

	model, ok := GetModel("openai", "custom-1")
	if !ok {
		t.Fatal("expected registered model")
	}
	if model.Name != "Custom One" {
		t.Fatalf("name = %q", model.Name)
	}

	usage := CalculateCost(model, Usage{Input: 1000, Output: 500, CacheRead: 200, CacheWrite: 100})
	if usage.Cost.Input != 0.002 {
		t.Fatalf("input cost = %v", usage.Cost.Input)
	}
	if usage.Cost.Total != 0.0045 {
		t.Fatalf("total cost = %v", usage.Cost.Total)
	}
	cost := CalculateCostValue(model, Usage{Input: 1000, Output: 500, CacheRead: 200, CacheWrite: 100})
	if cost.Total != usage.Cost.Total {
		t.Fatalf("cost total = %v", cost.Total)
	}
}

func TestGeneratedModelCatalogCoversProviders(t *testing.T) {
	ResetModels()
	for name := range providerSpecs {
		models := GetModels(name)
		if len(models) == 0 {
			t.Fatalf("missing generated models for provider %q", name)
		}
	}
}

func TestModelCatalogJSONImportExport(t *testing.T) {
	ResetModels()
	defer ResetModels()

	if err := LoadModelCatalogJSON([]byte(`{
		"OpenAI": {
			"custom": {
				"id": "custom",
				"name": "Custom",
				"api": "openai-responses",
				"provider": "ignored",
				"input": ["text"],
				"contextWindow": 123
			}
		}
	}`)); err != nil {
		t.Fatal(err)
	}
	model, ok := GetModel("openai", "custom")
	if !ok {
		t.Fatal("expected imported model")
	}
	if model.Provider != "openai" || model.ContextWindow != 123 {
		t.Fatalf("model = %#v", model)
	}

	exported, err := ExportModelCatalogJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exported), `"custom"`) || !strings.Contains(string(exported), `"contextWindow": 123`) {
		t.Fatalf("exported = %s", exported)
	}
}

func TestSupportsXhighAndModelEquality(t *testing.T) {
	if !SupportsXhigh(Model{ID: "gpt-5.5"}) {
		t.Fatal("expected gpt-5.5 to support xhigh")
	}
	if SupportsXhigh(Model{ID: "gpt-4.1"}) {
		t.Fatal("did not expect gpt-4.1 to support xhigh")
	}

	a := &Model{Provider: "OpenAI", ID: "gpt-5.5"}
	b := &Model{Provider: "openai", ID: "gpt-5.5"}
	if !ModelsAreEqual(a, b) {
		t.Fatal("expected models to be equal")
	}
	if ModelsAreEqual(a, nil) {
		t.Fatal("expected nil model comparison to be false")
	}
}
