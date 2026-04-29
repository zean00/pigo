package ai

import "strings"

func FindEnvKeys(provider string) []string {
	envKeys := apiKeyEnvKeys(provider)
	if len(envKeys) == 0 {
		return nil
	}
	found := make([]string, 0, len(envKeys))
	for _, key := range envKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if strings.TrimSpace(getenv(key)) != "" {
			found = append(found, key)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return found
}

func apiKeyEnvKeys(provider string) []string {
	provider = canonicalProviderName(provider)
	switch provider {
	case "github-copilot":
		return []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}
	case "anthropic":
		return []string{"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}
	case "openai":
		return []string{"OPENAI_API_KEY"}
	case "azure-openai-responses":
		return []string{"AZURE_OPENAI_API_KEY"}
	case "deepseek":
		return []string{"DEEPSEEK_API_KEY"}
	case "google", "google-gemini-cli", "google-antigravity":
		return []string{"GEMINI_API_KEY"}
	case "google-vertex":
		return []string{"GOOGLE_CLOUD_API_KEY"}
	case "groq":
		return []string{"GROQ_API_KEY"}
	case "cerebras":
		return []string{"CEREBRAS_API_KEY"}
	case "xai":
		return []string{"XAI_API_KEY"}
	case "openrouter":
		return []string{"OPENROUTER_API_KEY"}
	case "vercel-ai-gateway":
		return []string{"AI_GATEWAY_API_KEY"}
	case "zai":
		return []string{"ZAI_API_KEY"}
	case "mistral":
		return []string{"MISTRAL_API_KEY"}
	case "minimax":
		return []string{"MINIMAX_API_KEY"}
	case "minimax-cn":
		return []string{"MINIMAX_CN_API_KEY"}
	case "huggingface":
		return []string{"HF_TOKEN"}
	case "fireworks":
		return []string{"FIREWORKS_API_KEY"}
	case "opencode", "opencode-go":
		return []string{"OPENCODE_API_KEY"}
	case "kimi-coding":
		return []string{"KIMI_API_KEY"}
	default:
		return nil
	}
}

func GetEnvAPIKey(provider string) string {
	value, _ := ProviderAPIKey(provider)
	return value
}

func ClampReasoning(effort string) string {
	effort = strings.TrimSpace(effort)
	if effort == "xhigh" {
		return "high"
	}
	return effort
}

func BuildBaseOptions(model Model, options ChatOptions, apiKey string) ChatOptions {
	result := options
	if strings.TrimSpace(apiKey) != "" {
		result.APIKey = apiKey
	}
	if strings.TrimSpace(result.BaseURL) == "" && strings.TrimSpace(model.BaseURL) != "" {
		result.BaseURL = strings.TrimSpace(model.BaseURL)
	}
	if len(model.Headers) > 0 {
		headers := map[string]string{}
		for key, value := range model.Headers {
			headers[key] = value
		}
		for key, value := range result.Headers {
			headers[key] = value
		}
		result.Headers = headers
	}
	if result.MaxTokens <= 0 && model.MaxTokens > 0 {
		if model.MaxTokens > 32000 {
			result.MaxTokens = 32000
		} else {
			result.MaxTokens = model.MaxTokens
		}
	}
	return result
}

func BuildSimpleOptions(model Model, options ChatOptions) ChatOptions {
	result := BuildBaseOptions(model, options, options.APIKey)
	effort := strings.TrimSpace(result.ReasoningEffort)
	if effort != "" && effort != "off" && effort != "minimal" && effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" {
		effort = ""
	}
	if effort != "" && !SupportsXhigh(model) {
		effort = ClampReasoning(effort)
	}
	result.ReasoningEffort = effort
	return result
}

func AdjustMaxTokensForThinking(baseMaxTokens, modelMaxTokens int, reasoningLevel string, custom ThinkingBudgets) (int, int) {
	level := ClampReasoning(reasoningLevel)
	defaults := ThinkingBudgets{
		Minimal: 1024,
		Low:     2048,
		Medium:  8192,
		High:    16384,
	}
	budgets := mergeThinkingBudgets(defaults, custom)
	thinkingBudget := thinkingBudgetForLevel(level, budgets)
	if thinkingBudget == 0 {
		return minPositive(baseMaxTokens, modelMaxTokens), 0
	}
	maxTokens := baseMaxTokens + thinkingBudget
	if modelMaxTokens > 0 && (maxTokens == 0 || maxTokens > modelMaxTokens) {
		maxTokens = modelMaxTokens
	}
	const minOutputTokens = 1024
	if maxTokens <= thinkingBudget {
		thinkingBudget = maxTokens - minOutputTokens
		if thinkingBudget < 0 {
			thinkingBudget = 0
		}
	}
	return maxTokens, thinkingBudget
}

func mergeThinkingBudgets(base, override ThinkingBudgets) ThinkingBudgets {
	if override.Minimal > 0 {
		base.Minimal = override.Minimal
	}
	if override.Low > 0 {
		base.Low = override.Low
	}
	if override.Medium > 0 {
		base.Medium = override.Medium
	}
	if override.High > 0 {
		base.High = override.High
	}
	return base
}

func thinkingBudgetForLevel(level string, budgets ThinkingBudgets) int {
	switch strings.TrimSpace(level) {
	case "minimal":
		return budgets.Minimal
	case "low":
		return budgets.Low
	case "medium":
		return budgets.Medium
	case "high":
		return budgets.High
	default:
		return 0
	}
}

func minPositive(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}
