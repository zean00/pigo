package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBuiltinAPIProvidersAreRegistered(t *testing.T) {
	ResetAPIProviders()
	for _, api := range []string{
		"openai-completions",
		"openai-responses",
		"anthropic-messages",
		"bedrock-converse-stream",
		"google-generative-ai",
		"google-gemini-cli",
		"google-vertex",
		"mistral-conversations",
		"openai-codex-responses",
	} {
		if _, ok := ResolveAPIProvider(api); !ok {
			t.Fatalf("missing API provider %q", api)
		}
	}
}

func TestRegisterAPIProviderAndUnregisterBySource(t *testing.T) {
	ClearAPIProviders()
	defer ResetAPIProviders()
	RegisterAPIProvider(APIProvider{
		API: "custom-api",
		Complete: func(_ context.Context, model Model, messages []Message, _ ChatOptions) (NormalizedResult, []NormalizedEvent, error) {
			if model.ID != "m1" || len(messages) != 1 {
				t.Fatalf("model/messages = %#v %#v", model, messages)
			}
			result := NormalizedResult{Role: "assistant", StopReason: "stop", Text: "ok"}
			return result, AssistantEvents([]ContentBlock{{Type: "text", Text: "ok"}}, "stop"), nil
		},
	}, "extension-1")

	result, events, err := CompleteModel(context.Background(), Model{API: "custom-api", ID: "m1"}, []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" || len(events) == 0 {
		t.Fatalf("result/events = %#v %#v", result, events)
	}
	UnregisterAPIProviders("extension-1")
	if _, ok := ResolveAPIProvider("custom-api"); ok {
		t.Fatal("custom provider should be unregistered")
	}
}

func TestStreamSimpleBuildsSimpleOptions(t *testing.T) {
	ClearAPIProviders()
	defer ResetAPIProviders()
	RegisterAPIProvider(APIProvider{
		API: "simple-api",
		Stream: func(_ context.Context, _ Model, _ []Message, options ChatOptions) *EventStream {
			if options.APIKey != "key" || options.MaxTokens != 32000 || options.ReasoningEffort != "high" {
				return errorEventStream(context.Background(), errors.New("bad options"))
			}
			return streamFromResult(NormalizedResult{Role: "assistant", StopReason: "stop", Text: "ok"}, AssistantEvents([]ContentBlock{{Type: "text", Text: "ok"}}, "stop"))
		},
	})

	stream := StreamSimple(context.Background(), Model{API: "simple-api", Provider: "openai", ID: "gpt-4o", MaxTokens: 64000}, nil, ChatOptions{
		APIKey:          "key",
		ReasoningEffort: "xhigh",
	})
	for range stream.Events() {
	}
	result, err := stream.Result()
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" {
		t.Fatalf("result = %#v", result)
	}
}

func TestStreamModelMissingAPIProviderReturnsErrorStream(t *testing.T) {
	stream := StreamModel(context.Background(), Model{API: "missing-api"}, nil, ChatOptions{})
	var events []NormalizedEvent
	for event := range stream.Events() {
		events = append(events, event)
	}
	result, err := stream.Result()
	if err == nil {
		t.Fatal("expected error")
	}
	if result.StopReason != "error" || len(events) != 2 || events[1].Type != "error" {
		t.Fatalf("result/events = %#v %#v", result, events)
	}
	if !strings.Contains(events[1].ErrorMessage, "missing-api") {
		t.Fatalf("error event = %#v", events[1])
	}
}
