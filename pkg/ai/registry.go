package ai

import (
	"context"
	"fmt"
	"sync"
)

var (
	providersMu sync.RWMutex
	providers   = map[string]ChatProvider{}
)

func RegisterProvider(name string, provider ChatProvider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers[name] = provider
}

func ResolveProvider(name string) (ChatProvider, error) {
	providersMu.RLock()
	defer providersMu.RUnlock()
	if provider, ok := providers[name]; ok {
		return provider, nil
	}
	return nil, fmt.Errorf("unsupported provider: %s", name)
}

func Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	provider, err := ResolveProvider(req.Provider)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	return provider.Complete(ctx, req)
}

