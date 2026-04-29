package ai

import (
	"context"
	"fmt"
	"sync"
	"time"
)

var (
	providersMu sync.RWMutex
	providers   = map[string]ChatProvider{}
)

func RegisterProvider(name string, provider ChatProvider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers[normalizeProviderName(name)] = provider
}

func ResolveProvider(name string) (ChatProvider, error) {
	providersMu.RLock()
	defer providersMu.RUnlock()
	providerName := canonicalProviderName(name)
	if provider, ok := providers[providerName]; ok {
		return provider, nil
	}
	return nil, fmt.Errorf("unsupported provider: %s", name)
}

func Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	provider, err := ResolveProvider(req.Provider)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	result, events, err := provider.Complete(ctx, req)
	result = FillResultMetadata(result, req)
	if err != nil && result.StopReason == "" {
		result.StopReason = "error"
		if ctx.Err() != nil {
			result.StopReason = "aborted"
		}
		if result.ErrorMessage == "" {
			result.ErrorMessage = err.Error()
		}
	}
	return result, AttachEventPayloads(events, result), err
}

func FillResultMetadata(result NormalizedResult, req CompletionRequest) NormalizedResult {
	if result.Role == "" {
		result.Role = "assistant"
	}
	if result.Provider == "" {
		result.Provider = canonicalProviderName(req.Provider)
	}
	if result.Model == "" {
		result.Model = req.Model
	}
	if result.API == "" {
		if model, ok := GetModel(result.Provider, result.Model); ok {
			result.API = model.API
		} else if spec, ok := ProviderSpecForProvider(result.Provider); ok {
			result.API = apiForProviderMode(spec.Mode)
		}
	}
	if result.Timestamp == 0 {
		result.Timestamp = time.Now().UnixMilli()
	}
	if result.Usage == nil {
		result.Usage = &Usage{}
	}
	return result
}
