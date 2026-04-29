package ai

import "strings"

func FindEnvKeys(provider string) []string {
	spec, ok := ProviderSpecForProvider(provider)
	if !ok {
		return nil
	}
	return append([]string(nil), spec.EnvKeys...)
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
	if result.MaxTokens <= 0 && model.MaxTokens > 0 {
		if model.MaxTokens > 32000 {
			result.MaxTokens = 32000
		} else {
			result.MaxTokens = model.MaxTokens
		}
	}
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
