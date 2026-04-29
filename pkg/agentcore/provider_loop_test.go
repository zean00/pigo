package agentcore

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

type scriptedProvider struct {
	mu        sync.Mutex
	responses []ai.NormalizedResult
	requests  []ai.CompletionRequest
	calls     int
}

func (provider *scriptedProvider) Complete(ctx context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	provider.requests = append(provider.requests, req)
	index := provider.calls - 1
	if index < 0 || index >= len(provider.responses) {
		return ai.NormalizedResult{}, nil, fmt.Errorf("unexpected provider call: %d", provider.calls)
	}
	return provider.responses[index], nil, nil
}

func TestRunProviderLoopExecutesToolsAndStops(t *testing.T) {
	providerName := "agentcore-provider-loop-1"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "tc-1", "name": "echo", "arguments": map[string]any{"value": "ok"}},
			},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "done",
			Content: []any{
				map[string]any{"type": "text", "text": "done"},
			},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	toolCalled := false
	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts: []string{"go"},
		Tools: []Tool{
			{
				Name: "echo",
				Execute: func(_ context.Context, call ai.ContentBlock) ToolResult {
					toolCalled = true
					if call.Name != "echo" {
						t.Fatalf("unexpected call name %q", call.Name)
					}
					value, _ := call.Arguments["value"].(string)
					if value != "ok" {
						t.Fatalf("unexpected argument value %q", value)
					}
					return ToolResult{Text: "echoed: " + value}
				},
			},
		},
		History:  []ai.Message{},
		Provider: providerName,
		Model:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !toolCalled {
		t.Fatal("tool not executed")
	}
	if len(result.Messages) != 4 {
		t.Fatalf("message count = %d", len(result.Messages))
	}
	if result.Messages[0]["role"] != "user" {
		t.Fatalf("first message role = %v", result.Messages[0]["role"])
	}
	if result.Messages[1]["role"] != "assistant" {
		t.Fatalf("second message role = %v", result.Messages[1]["role"])
	}
	if result.Messages[3]["role"] != "assistant" {
		t.Fatalf("last message role = %v", result.Messages[3]["role"])
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(provider.requests))
	}
	secondRequestMessages := provider.requests[1].Messages
	if got, want := secondRequestMessages[len(secondRequestMessages)-1].ToolCallID, "tc-1"; got != want {
		t.Fatalf("tool result toolCallID = %q, want %q", got, want)
	}
}

func TestRunProviderLoopReturnsProviderError(t *testing.T) {
	providerName := "agentcore-provider-loop-2"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{}}
	ai.RegisterProvider(providerName, provider)

	_, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Tools:    []Tool{},
		History:  []ai.Message{},
		Provider: providerName,
		Model:    "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
