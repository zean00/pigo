package codingagent

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

var ErrUsageQuotaExceeded = errors.New("usage quota exceeded")

const (
	UsageQuotaOff     = "off"
	UsageQuotaEnforce = "enforce"
)

type UsageQuotaConfig struct {
	Mode                string  `json:"mode"`
	MaxInputTokens      int     `json:"maxInputTokens,omitempty"`
	MaxOutputTokens     int     `json:"maxOutputTokens,omitempty"`
	MaxCacheReadTokens  int     `json:"maxCacheReadTokens,omitempty"`
	MaxCacheWriteTokens int     `json:"maxCacheWriteTokens,omitempty"`
	MaxTotalTokens      int     `json:"maxTotalTokens,omitempty"`
	MaxCost             float64 `json:"maxCost,omitempty"`
}

type UsageLedgerEntry struct {
	Timestamp    string   `json:"timestamp"`
	MessageID    string   `json:"messageId,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	ModelID      string   `json:"modelId,omitempty"`
	Usage        ai.Usage `json:"usage"`
	PricingKnown bool     `json:"pricingKnown"`
}

type UsageQuotaStatus struct {
	Mode       string           `json:"mode"`
	Enabled    bool             `json:"enabled"`
	Limits     UsageQuotaConfig `json:"limits"`
	Usage      ai.Usage         `json:"usage"`
	Exceeded   []string         `json:"exceeded,omitempty"`
	Warnings   []string         `json:"warnings,omitempty"`
	EntryCount int              `json:"entryCount"`
}

func DefaultUsageQuotaConfig() UsageQuotaConfig {
	return UsageQuotaConfig{Mode: UsageQuotaOff}
}

func UsageQuotaConfigFromEnv() UsageQuotaConfig {
	config := DefaultUsageQuotaConfig()
	if mode := strings.TrimSpace(os.Getenv("PIGO_USAGE_QUOTA")); mode != "" {
		config.Mode = mode
	}
	config.MaxInputTokens = intEnv("PIGO_USAGE_MAX_INPUT_TOKENS")
	config.MaxOutputTokens = intEnv("PIGO_USAGE_MAX_OUTPUT_TOKENS")
	config.MaxCacheReadTokens = intEnv("PIGO_USAGE_MAX_CACHE_READ_TOKENS")
	config.MaxCacheWriteTokens = intEnv("PIGO_USAGE_MAX_CACHE_WRITE_TOKENS")
	config.MaxTotalTokens = intEnv("PIGO_USAGE_MAX_TOTAL_TOKENS")
	config.MaxCost = floatEnv("PIGO_USAGE_MAX_COST")
	return config.Normalized()
}

func (c UsageQuotaConfig) Normalized() UsageQuotaConfig {
	switch strings.TrimSpace(strings.ToLower(c.Mode)) {
	case UsageQuotaEnforce:
		c.Mode = UsageQuotaEnforce
	default:
		c.Mode = UsageQuotaOff
	}
	if c.MaxInputTokens < 0 {
		c.MaxInputTokens = 0
	}
	if c.MaxOutputTokens < 0 {
		c.MaxOutputTokens = 0
	}
	if c.MaxCacheReadTokens < 0 {
		c.MaxCacheReadTokens = 0
	}
	if c.MaxCacheWriteTokens < 0 {
		c.MaxCacheWriteTokens = 0
	}
	if c.MaxTotalTokens < 0 {
		c.MaxTotalTokens = 0
	}
	if c.MaxCost < 0 {
		c.MaxCost = 0
	}
	return c
}

func (c UsageQuotaConfig) Validate() error {
	switch strings.TrimSpace(strings.ToLower(c.Mode)) {
	case UsageQuotaOff, UsageQuotaEnforce:
		return nil
	default:
		return fmt.Errorf("invalid usage quota mode %q", c.Mode)
	}
}

func (c UsageQuotaConfig) Metadata() map[string]any {
	c = c.Normalized()
	return map[string]any{
		"mode":                c.Mode,
		"maxInputTokens":      c.MaxInputTokens,
		"maxOutputTokens":     c.MaxOutputTokens,
		"maxCacheReadTokens":  c.MaxCacheReadTokens,
		"maxCacheWriteTokens": c.MaxCacheWriteTokens,
		"maxTotalTokens":      c.MaxTotalTokens,
		"maxCost":             c.MaxCost,
	}
}

func usageFromMessage(message agentcore.Message) (ai.Usage, bool) {
	raw, ok := message["usage"].(map[string]any)
	if !ok {
		return ai.Usage{}, false
	}
	usage := ai.Usage{
		Input:       asInt(raw["input"]),
		Output:      asInt(raw["output"]),
		CacheRead:   asInt(raw["cacheRead"]),
		CacheWrite:  asInt(raw["cacheWrite"]),
		TotalTokens: asInt(raw["totalTokens"]),
	}
	if cost, ok := raw["cost"].(map[string]any); ok {
		usage.Cost = ai.Cost{
			Input:      asFloat(cost["input"]),
			Output:     asFloat(cost["output"]),
			CacheRead:  asFloat(cost["cacheRead"]),
			CacheWrite: asFloat(cost["cacheWrite"]),
			Total:      asFloat(cost["total"]),
		}
	}
	return usage, usage.Input != 0 || usage.Output != 0 || usage.CacheRead != 0 || usage.CacheWrite != 0 || usage.TotalTokens != 0 || usage.Cost.Total != 0
}

func addUsage(left, right ai.Usage) ai.Usage {
	left.Input += right.Input
	left.Output += right.Output
	left.CacheRead += right.CacheRead
	left.CacheWrite += right.CacheWrite
	left.TotalTokens += right.TotalTokens
	left.Cost.Input += right.Cost.Input
	left.Cost.Output += right.Cost.Output
	left.Cost.CacheRead += right.Cost.CacheRead
	left.Cost.CacheWrite += right.Cost.CacheWrite
	left.Cost.Total += right.Cost.Total
	return left
}

func modelPricingKnown(model ai.Model) bool {
	return model.Cost.Input != 0 || model.Cost.Output != 0 || model.Cost.CacheRead != 0 || model.Cost.CacheWrite != 0
}

func intEnv(name string) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0
	}
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func floatEnv(name string) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
