package ai

import (
	"bytes"
	_ "embed"
	"encoding/json"
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
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	API           string            `json:"api"`
	Provider      string            `json:"provider"`
	BaseURL       string            `json:"baseUrl"`
	Reasoning     bool              `json:"reasoning"`
	Input         []string          `json:"input"`
	Cost          ModelCost         `json:"cost"`
	ContextWindow int               `json:"contextWindow"`
	MaxTokens     int               `json:"maxTokens"`
	Headers       map[string]string `json:"headers"`
	Compat        map[string]any    `json:"compat"`
}

//go:embed models.generated.json
var generatedModelCatalogJSON []byte

var (
	modelsMu       sync.RWMutex
	modelRegistry  = map[string]map[string]Model{}
	defaultCatalog = buildDefaultModelCatalog()
)

func init() {
	ResetModels()
	ResetAPIProviders()
}

func buildDefaultModelCatalog() map[string]map[string]Model {
	if catalog, err := parseGeneratedModelCatalog(generatedModelCatalogJSON); err == nil && len(catalog) > 0 {
		return catalog
	}
	return buildLegacyDefaultModelCatalog()
}

func buildLegacyDefaultModelCatalog() map[string]map[string]Model {
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

func KnownAPIs() []string {
	return []string{
		"openai-completions",
		"mistral-conversations",
		"openai-responses",
		"azure-openai-responses",
		"openai-codex-responses",
		"anthropic-messages",
		"bedrock-converse-stream",
		"google-generative-ai",
		"google-gemini-cli",
		"google-vertex",
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
	model.Headers = copyStringMap(model.Headers)
	model.Compat = cloneCompatMap(model.Compat)
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

func parseGeneratedModelCatalog(data []byte) (map[string]map[string]Model, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	rawCatalog := map[string]map[string]Model{}
	if err := json.Unmarshal(data, &rawCatalog); err != nil {
		return nil, err
	}
	modelCatalog := map[string]map[string]Model{}
	for rawProvider, rawModels := range rawCatalog {
		provider := canonicalProviderName(rawProvider)
		providerModels := modelCatalog[provider]
		if providerModels == nil {
			providerModels = map[string]Model{}
			modelCatalog[provider] = providerModels
		}
		spec, hasSpec := ProviderSpecForProvider(provider)

		for _, model := range rawModels {
			modelID := strings.TrimSpace(model.ID)
			if modelID == "" {
				continue
			}

			model.Provider = provider
			model.Name = strings.TrimSpace(model.Name)
			if model.Name == "" {
				model.Name = modelID
			}
			model.API = strings.TrimSpace(model.API)
			if model.API == "" && hasSpec {
				model.API = apiForProviderMode(spec.Mode)
			}
			model.BaseURL = strings.TrimSpace(model.BaseURL)
			if model.BaseURL == "" && hasSpec {
				model.BaseURL = spec.BaseURL
			}
			if len(model.Input) == 0 && hasSpec {
				model.Input = defaultModelInput(spec.Mode)
			}
			if hasSpec {
				model.Headers = mergeHeaders(spec.DefaultHeader, model.Headers)
			}

			providerModels[modelID] = cloneModel(model)
		}
	}
	if len(modelCatalog) == 0 {
		return nil, nil
	}
	return modelCatalog, nil
}

func mergeHeaders(base, over map[string]string) map[string]string {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	merged := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(key) == "" {
			continue
		}
		merged[key] = value
	}
	for key, value := range over {
		if strings.TrimSpace(key) == "" {
			continue
		}
		merged[key] = value
	}
	return merged
}

func copyStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	copied := make(map[string]string, len(value))
	for key, val := range value {
		copied[key] = val
	}
	return copied
}

func cloneCompatMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, raw := range value {
		out[key] = cloneAnyValue(raw)
	}
	return out
}

func cloneAnyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copied := make(map[string]any, len(typed))
		for key, entry := range typed {
			copied[key] = cloneAnyValue(entry)
		}
		return copied
	case []any:
		copied := make([]any, 0, len(typed))
		for _, entry := range typed {
			copied = append(copied, cloneAnyValue(entry))
		}
		return copied
	default:
		return value
	}
}

func ClearModels() {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	modelRegistry = map[string]map[string]Model{}
}

func ClearProviderModels(provider string) {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	delete(modelRegistry, canonicalProviderName(provider))
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

func CalculateCostValue(model Model, usage Usage) Cost {
	return CalculateCost(model, usage).Cost
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
