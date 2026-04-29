package ai

import (
	"context"
	"testing"
)

func TestRegisterProviderConfigAddsModelsAndCustomStreamSimple(t *testing.T) {
	ClearModels()
	ClearAPIProviders()
	defer func() {
		ResetModels()
		ResetAPIProviders()
	}()

	err := RegisterProviderConfig(ProviderConfig{
		Name:    "my-proxy",
		API:     "my-proxy-api",
		BaseURL: "https://proxy.example.com",
		APIKey:  "PROXY_API_KEY",
		Models: []Model{{
			ID:            "proxy-model",
			Reasoning:     true,
			Input:         []string{"text", "image"},
			ContextWindow: 200000,
			MaxTokens:     16384,
			Headers:       map[string]string{"X-Proxy": "model"},
		}},
		StreamSimple: func(_ context.Context, model Model, _ []Message, options ChatOptions) *EventStream {
			if model.Provider != "my-proxy" || model.BaseURL != "https://proxy.example.com" {
				return errorEventStream(context.Background(), errTest("bad model"))
			}
			if options.BaseURL != "https://proxy.example.com" || options.Headers["X-Proxy"] != "model" {
				return errorEventStream(context.Background(), errTest("bad options"))
			}
			result := NormalizedResult{Role: "assistant", StopReason: "stop", Text: "ok"}
			return streamFromResult(result, AssistantEvents([]ContentBlock{{Type: "text", Text: "ok"}}, "stop"))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, ok := GetModel("my-proxy", "proxy-model")
	if !ok {
		t.Fatal("missing proxy model")
	}
	stream := StreamSimple(context.Background(), model, []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	for range stream.Events() {
	}
	result, err := stream.Result()
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" {
		t.Fatalf("result = %#v", result)
	}

	UnregisterProviderConfig("my-proxy")
	if _, ok := GetModel("my-proxy", "proxy-model"); ok {
		t.Fatal("proxy model should be removed")
	}
	if _, ok := ResolveAPIProvider("my-proxy-api"); ok {
		t.Fatal("proxy API should be removed")
	}
}

type errTest string

func (err errTest) Error() string { return string(err) }
