package ai

import (
	"context"
	"fmt"
)

type unsupportedProvider struct {
	provider string
}

func unsupportedProviderFor(name string) ChatProvider {
	return unsupportedProvider{provider: canonicalProviderName(name)}
}

func (provider unsupportedProvider) Complete(_ context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	return NormalizedResult{}, nil, fmt.Errorf("provider %q is not yet implemented in pigo", req.Provider)
}
