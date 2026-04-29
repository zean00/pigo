package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type StreamFunction func(context.Context, Model, []Message, ChatOptions) *EventStream
type CompleteFunction func(context.Context, Model, []Message, ChatOptions) (NormalizedResult, []NormalizedEvent, error)

type APIProvider struct {
	API            string
	Stream         StreamFunction
	Complete       CompleteFunction
	StreamSimple   StreamFunction
	CompleteSimple CompleteFunction
}

type registeredAPIProvider struct {
	provider APIProvider
	sourceID string
}

var (
	apiProvidersMu sync.RWMutex
	apiProviders   = map[string]registeredAPIProvider{}
)

func RegisterAPIProvider(provider APIProvider, sourceID ...string) {
	api := strings.TrimSpace(provider.API)
	if api == "" {
		return
	}
	normalized := normalizeAPIName(api)
	provider.API = normalized
	if provider.Complete == nil {
		provider.Complete = defaultAPIComplete
	}
	if provider.Stream == nil {
		provider.Stream = defaultAPIStream
	}
	if provider.CompleteSimple == nil {
		provider.CompleteSimple = func(ctx context.Context, model Model, contextMessages []Message, options ChatOptions) (NormalizedResult, []NormalizedEvent, error) {
			options = BuildSimpleOptions(model, options)
			return provider.Complete(ctx, model, contextMessages, options)
		}
	}
	if provider.StreamSimple == nil {
		provider.StreamSimple = func(ctx context.Context, model Model, contextMessages []Message, options ChatOptions) *EventStream {
			options = BuildSimpleOptions(model, options)
			return provider.Stream(ctx, model, contextMessages, options)
		}
	}
	id := ""
	if len(sourceID) > 0 {
		id = sourceID[0]
	}
	apiProvidersMu.Lock()
	defer apiProvidersMu.Unlock()
	apiProviders[normalized] = registeredAPIProvider{provider: provider, sourceID: id}
}

func ResolveAPIProvider(api string) (APIProvider, bool) {
	apiProvidersMu.RLock()
	defer apiProvidersMu.RUnlock()
	entry, ok := apiProviders[normalizeAPIName(api)]
	return entry.provider, ok
}

func GetAPIProviders() []APIProvider {
	apiProvidersMu.RLock()
	defer apiProvidersMu.RUnlock()
	providers := make([]APIProvider, 0, len(apiProviders))
	for _, entry := range apiProviders {
		providers = append(providers, entry.provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].API < providers[j].API
	})
	return providers
}

func UnregisterAPIProviders(sourceID string) {
	apiProvidersMu.Lock()
	defer apiProvidersMu.Unlock()
	for api, entry := range apiProviders {
		if entry.sourceID == sourceID {
			delete(apiProviders, api)
		}
	}
}

func ClearAPIProviders() {
	apiProvidersMu.Lock()
	defer apiProvidersMu.Unlock()
	apiProviders = map[string]registeredAPIProvider{}
}

func ResetAPIProviders() {
	ClearAPIProviders()
	RegisterBuiltinAPIProviders()
}

func StreamModel(ctx context.Context, model Model, contextMessages []Message, options ChatOptions) *EventStream {
	provider, ok := ResolveAPIProvider(model.API)
	if !ok {
		return errorEventStream(ctx, fmt.Errorf("no API provider registered for api: %s", model.API))
	}
	return provider.Stream(ctx, model, contextMessages, options)
}

func CompleteModel(ctx context.Context, model Model, contextMessages []Message, options ChatOptions) (NormalizedResult, []NormalizedEvent, error) {
	provider, ok := ResolveAPIProvider(model.API)
	if !ok {
		return NormalizedResult{}, nil, fmt.Errorf("no API provider registered for api: %s", model.API)
	}
	result, events, err := provider.Complete(ctx, model, contextMessages, options)
	result = FillResultMetadata(result, CompletionRequest{
		Provider: model.Provider,
		Model:    model.ID,
		Messages: contextMessages,
		Options:  options,
	})
	if result.API == "" {
		result.API = model.API
	}
	return result, AttachEventPayloads(events, result), err
}

func StreamSimple(ctx context.Context, model Model, contextMessages []Message, options ChatOptions) *EventStream {
	provider, ok := ResolveAPIProvider(model.API)
	if !ok {
		return errorEventStream(ctx, fmt.Errorf("no API provider registered for api: %s", model.API))
	}
	return provider.StreamSimple(ctx, model, contextMessages, options)
}

func CompleteSimple(ctx context.Context, model Model, contextMessages []Message, options ChatOptions) (NormalizedResult, []NormalizedEvent, error) {
	provider, ok := ResolveAPIProvider(model.API)
	if !ok {
		return NormalizedResult{}, nil, fmt.Errorf("no API provider registered for api: %s", model.API)
	}
	result, events, err := provider.CompleteSimple(ctx, model, contextMessages, options)
	result = FillResultMetadata(result, CompletionRequest{
		Provider: model.Provider,
		Model:    model.ID,
		Messages: contextMessages,
		Options:  options,
	})
	if result.API == "" {
		result.API = model.API
	}
	return result, AttachEventPayloads(events, result), err
}

func RegisterBuiltinAPIProviders() {
	seen := map[string]struct{}{}
	for _, api := range KnownAPIs() {
		if api == "" || api == "unknown" {
			continue
		}
		seen[api] = struct{}{}
		RegisterAPIProvider(APIProvider{API: api})
	}
	for _, spec := range providerProfiles() {
		api := apiForProviderMode(spec.Mode)
		if api == "" || api == "unknown" {
			continue
		}
		if _, exists := seen[api]; exists {
			continue
		}
		seen[api] = struct{}{}
		RegisterAPIProvider(APIProvider{API: api})
	}
}

func defaultAPIComplete(ctx context.Context, model Model, contextMessages []Message, options ChatOptions) (NormalizedResult, []NormalizedEvent, error) {
	result, events, err := Complete(ctx, CompletionRequest{
		Provider: model.Provider,
		Model:    model.ID,
		Messages: contextMessages,
		Options:  BuildBaseOptions(model, options, options.APIKey),
	})
	if result.API == "" {
		result.API = model.API
	}
	return result, AttachEventPayloads(events, result), err
}

func defaultAPIStream(ctx context.Context, model Model, contextMessages []Message, options ChatOptions) *EventStream {
	return Stream(ctx, CompletionRequest{
		Provider: model.Provider,
		Model:    model.ID,
		Messages: contextMessages,
		Options:  BuildBaseOptions(model, options, options.APIKey),
	})
}

func normalizeAPIName(api string) string {
	return strings.TrimSpace(strings.ToLower(api))
}

func streamFromResult(result NormalizedResult, events []NormalizedEvent) *EventStream {
	stream := CreateEventStream()
	go func() {
		for _, event := range AttachEventPayloads(events, result) {
			stream.events <- event
		}
		stream.Close(result, nil)
	}()
	return stream
}
