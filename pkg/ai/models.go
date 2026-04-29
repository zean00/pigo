package ai

import (
	"sort"
	"strings"
	"sync"
)

type ModelCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

type Model struct {
	ID            string
	Name          string
	API           string
	Provider      string
	BaseURL       string
	Reasoning     bool
	Input         []string
	Cost          ModelCost
	ContextWindow int
	MaxTokens     int
}

var (
	modelsMu       sync.RWMutex
	modelRegistry  = map[string]map[string]Model{}
	defaultCatalog = buildDefaultModelCatalog()
)

func init() {
	ResetModels()
}

func buildDefaultModelCatalog() map[string]map[string]Model {
	out := map[string]map[string]Model{}
	for _, spec := range providerProfiles() {
		modelID := strings.TrimSpace(spec.DefaultModel)
		if modelID == "" {
			continue
		}
		providerModels := out[spec.Name]
		if providerModels == nil {
			providerModels = map[string]Model{}
			out[spec.Name] = providerModels
		}
		providerModels[modelID] = Model{
			ID:        modelID,
			Name:      modelID,
			API:       apiForProviderMode(spec.Mode),
			Provider:  spec.Name,
			BaseURL:   spec.BaseURL,
			Reasoning: modelSupportsReasoning(modelID),
			Input:     defaultModelInput(spec.Mode),
			Cost:      ModelCost{},
		}
	}
	return out
}

func apiForProviderMode(mode string) string {
	switch mode {
	case providerModeOpenAI:
		return "openai-completions"
	case providerModeBedrock:
		return "bedrock-converse-stream"
	case providerModeAnthropic:
		return "anthropic-messages"
	case providerModeGoogle:
		return "google-generative-ai"
	case providerModeGoogleCLI:
		return "google-gemini-cli"
	case providerModeGoogleVertex:
		return "google-vertex"
	case providerModeMistral:
		return "mistral-conversations"
	case providerModeCodex:
		return "openai-codex-responses"
	default:
		return "unknown"
	}
}

func modelSupportsReasoning(modelID string) bool {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(modelID, "gpt-5") ||
		strings.Contains(modelID, "claude") ||
		strings.Contains(modelID, "gemini") ||
		strings.Contains(modelID, "kimi") ||
		strings.Contains(modelID, "deepseek") ||
		strings.Contains(modelID, "grok") ||
		strings.Contains(modelID, "glm")
}

func defaultModelInput(mode string) []string {
	switch mode {
	case providerModeOpenAI, providerModeAnthropic, providerModeGoogle, providerModeGoogleCLI, providerModeGoogleVertex, providerModeMistral, providerModeCodex, providerModeBedrock:
		return []string{"text", "image"}
	default:
		return []string{"text"}
	}
}

func cloneModel(model Model) Model {
	model.Input = append([]string(nil), model.Input...)
	return model
}

func GetModel(provider, modelID string) (Model, bool) {
	modelsMu.RLock()
	defer modelsMu.RUnlock()
	provider = canonicalProviderName(provider)
	modelID = strings.TrimSpace(modelID)
	providerModels := modelRegistry[provider]
	if providerModels == nil {
		return Model{}, false
	}
	model, ok := providerModels[modelID]
	if !ok {
		return Model{}, false
	}
	return cloneModel(model), true
}

func GetProviders() []string {
	modelsMu.RLock()
	defer modelsMu.RUnlock()
	providers := make([]string, 0, len(modelRegistry))
	for provider := range modelRegistry {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}

func GetModels(provider string) []Model {
	modelsMu.RLock()
	defer modelsMu.RUnlock()
	provider = canonicalProviderName(provider)
	providerModels := modelRegistry[provider]
	if providerModels == nil {
		return nil
	}
	out := make([]Model, 0, len(providerModels))
	for _, model := range providerModels {
		out = append(out, cloneModel(model))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func RegisterModel(model Model) {
	provider := canonicalProviderName(model.Provider)
	model.Provider = provider
	model.ID = strings.TrimSpace(model.ID)
	model.Name = strings.TrimSpace(model.Name)
	if model.Name == "" {
		model.Name = model.ID
	}
	model.API = strings.TrimSpace(model.API)
	if model.API == "" {
		if spec, ok := ProviderSpecForProvider(provider); ok {
			model.API = apiForProviderMode(spec.Mode)
			if model.BaseURL == "" {
				model.BaseURL = spec.BaseURL
			}
		}
	}
	modelsMu.Lock()
	defer modelsMu.Unlock()
	providerModels := modelRegistry[provider]
	if providerModels == nil {
		providerModels = map[string]Model{}
		modelRegistry[provider] = providerModels
	}
	providerModels[model.ID] = cloneModel(model)
}

func ClearModels() {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	modelRegistry = map[string]map[string]Model{}
}

func ResetModels() {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	modelRegistry = map[string]map[string]Model{}
	for provider, models := range defaultCatalog {
		copied := map[string]Model{}
		for modelID, model := range models {
			copied[modelID] = cloneModel(model)
		}
		modelRegistry[provider] = copied
	}
}

func CalculateCost(model Model, usage Usage) Usage {
	result := usage
	inputCost := (model.Cost.Input / 1_000_000) * float64(usage.Input)
	outputCost := (model.Cost.Output / 1_000_000) * float64(usage.Output)
	cacheReadCost := (model.Cost.CacheRead / 1_000_000) * float64(usage.CacheRead)
	cacheWriteCost := (model.Cost.CacheWrite / 1_000_000) * float64(usage.CacheWrite)
	result.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	result.Cost = Cost{
		Input:      inputCost,
		Output:     outputCost,
		CacheRead:  cacheReadCost,
		CacheWrite: cacheWriteCost,
		Total:      inputCost + outputCost + cacheReadCost + cacheWriteCost,
	}
	return result
}

func SupportsXhigh(model Model) bool {
	id := strings.ToLower(strings.TrimSpace(model.ID))
	return strings.Contains(id, "gpt-5.2") ||
		strings.Contains(id, "gpt-5.3") ||
		strings.Contains(id, "gpt-5.4") ||
		strings.Contains(id, "gpt-5.5") ||
		strings.Contains(id, "deepseek-v4-pro") ||
		strings.Contains(id, "opus-4-6") ||
		strings.Contains(id, "opus-4.6") ||
		strings.Contains(id, "opus-4-7") ||
		strings.Contains(id, "opus-4.7")
}

func ModelsAreEqual(a, b *Model) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && canonicalProviderName(a.Provider) == canonicalProviderName(b.Provider)
}
