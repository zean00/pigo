package ai

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type ProviderConfig struct {
	Name           string
	API            string
	BaseURL        string
	APIKey         string
	Models         []Model
	StreamSimple   StreamFunction
	CompleteSimple CompleteFunction
}

type ProviderConfigMutator func(config ProviderConfig) (ProviderConfig, error)

var (
	providerConfigMutatorsMu sync.RWMutex
	providerConfigMutators   = map[string]ProviderConfigMutator{}
)

func RegisterProviderConfigMutator(provider string, mutator ProviderConfigMutator) {
	provider = canonicalProviderName(provider)
	providerConfigMutatorsMu.Lock()
	defer providerConfigMutatorsMu.Unlock()
	if mutator == nil {
		delete(providerConfigMutators, provider)
		return
	}
	providerConfigMutators[provider] = mutator
}

func ClearProviderConfigMutators() {
	providerConfigMutatorsMu.Lock()
	defer providerConfigMutatorsMu.Unlock()
	providerConfigMutators = map[string]ProviderConfigMutator{}
}

func mutateProviderConfig(config ProviderConfig) (ProviderConfig, error) {
	providerConfigMutatorsMu.RLock()
	mutator := providerConfigMutators[canonicalProviderName(config.Name)]
	providerConfigMutatorsMu.RUnlock()
	if mutator == nil {
		return config, nil
	}
	return mutator(config)
}

func RegisterProviderConfig(config ProviderConfig) error {
	name := canonicalProviderName(config.Name)
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("missing provider name")
	}
	config.Name = name
	var err error
	config, err = mutateProviderConfig(config)
	if err != nil {
		return err
	}
	name = canonicalProviderName(config.Name)
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("missing provider name")
	}
	if config.StreamSimple != nil || config.CompleteSimple != nil {
		if strings.TrimSpace(config.API) == "" {
			return fmt.Errorf("provider %s: api is required when registering streamSimple", name)
		}
		streamSimple := config.StreamSimple
		if streamSimple != nil {
			streamSimple = func(ctx context.Context, model Model, messages []Message, options ChatOptions) *EventStream {
				return config.StreamSimple(ctx, model, messages, BuildSimpleOptions(model, options))
			}
		}
		completeSimple := config.CompleteSimple
		if completeSimple != nil {
			completeSimple = func(ctx context.Context, model Model, messages []Message, options ChatOptions) (NormalizedResult, []NormalizedEvent, error) {
				return config.CompleteSimple(ctx, model, messages, BuildSimpleOptions(model, options))
			}
		}
		RegisterAPIProvider(APIProvider{
			API:            config.API,
			StreamSimple:   streamSimple,
			CompleteSimple: completeSimple,
		}, "provider:"+name)
	}
	if len(config.Models) == 0 {
		return nil
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		return fmt.Errorf("provider %s: baseURL is required when defining models", name)
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("provider %s: apiKey is required when defining models", name)
	}
	for _, model := range config.Models {
		if strings.TrimSpace(model.ID) == "" {
			return fmt.Errorf("provider %s: model id is required", name)
		}
		if strings.TrimSpace(model.API) == "" {
			model.API = config.API
		}
		if strings.TrimSpace(model.API) == "" {
			return fmt.Errorf("provider %s, model %s: no api specified", name, model.ID)
		}
		model.Provider = name
		if strings.TrimSpace(model.BaseURL) == "" {
			model.BaseURL = config.BaseURL
		}
		if model.Name == "" {
			model.Name = model.ID
		}
		RegisterModel(model)
	}
	return nil
}

func UnregisterProviderConfig(name string) {
	name = canonicalProviderName(name)
	ClearProviderModels(name)
	UnregisterAPIProviders("provider:" + name)
}
