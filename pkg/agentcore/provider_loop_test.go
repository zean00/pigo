package agentcore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

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

func TestRunProviderLoopTurnEndIncludesMessageAndToolResults(t *testing.T) {
	providerName := "agentcore-provider-loop-turn-end-payload"
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
			Content:    []any{map[string]any{"type": "text", "text": "done"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	var turnEnds []Event
	_, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		Tools: []Tool{{
			Name: "echo",
			Execute: func(_ context.Context, _ ai.ContentBlock) ToolResult {
				return ToolResult{Text: "echoed"}
			},
		}},
		EventSink: func(event Event) {
			if event["type"] == "turn_end" {
				turnEnds = append(turnEnds, event)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(turnEnds) != 2 {
		t.Fatalf("turn end count = %d", len(turnEnds))
	}
	firstMessage, ok := turnEnds[0]["message"].(Message)
	if !ok || firstMessage["role"] != "assistant" {
		t.Fatalf("turn end message = %#v", turnEnds[0]["message"])
	}
	toolResults, ok := turnEnds[0]["toolResults"].([]Message)
	if !ok || len(toolResults) != 1 || toolResults[0]["toolCallId"] != "tc-1" {
		t.Fatalf("turn end tool results = %#v", turnEnds[0]["toolResults"])
	}
	secondResults, ok := turnEnds[1]["toolResults"].([]Message)
	if !ok || len(secondResults) != 0 {
		t.Fatalf("second turn tool results = %#v", turnEnds[1]["toolResults"])
	}
}

func TestRunProviderLoopPreservesStructuredPromptMessageEvents(t *testing.T) {
	providerName := "agentcore-provider-loop-structured-prompt"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "done",
		Content:    []any{map[string]any{"type": "text", "text": "done"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		PromptMessages: []ai.Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "look"},
				map[string]any{"type": "image", "data": "abc", "mimeType": "image/png"},
			},
		}},
		Provider: providerName,
		Model:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) < 1 {
		t.Fatalf("messages = %#v", result.Messages)
	}
	content, ok := result.Messages[0]["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("user content = %#v", result.Messages[0]["content"])
	}
	blocks := ai.ParseContentBlocks(content)
	if len(blocks) != 2 || blocks[1].Type != "image" || blocks[1].Data != "abc" {
		t.Fatalf("event blocks = %#v", blocks)
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

func TestRunProviderLoopRejectsAssistantContinuation(t *testing.T) {
	_, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		History: []ai.Message{{Role: "assistant", Content: "done"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "cannot continue provider loop from assistant message"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestRunProviderLoopContinuesFromExistingContextWithoutPromptEvents(t *testing.T) {
	providerName := "agentcore-provider-loop-continue-context"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "continued",
		Content:    []any{map[string]any{"type": "text", "text": "continued"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	var events []string
	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		History:  []ai.Message{{Role: "user", Content: "existing"}},
		Provider: providerName,
		Model:    "test",
		EventSink: func(event Event) {
			events = append(events, eventType(event))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || result.Messages[0]["text"] != "continued" {
		t.Fatalf("messages = %#v", result.Messages)
	}
	if fmt.Sprint(events[:4]) != "[agent_start turn_start message_start message_update]" {
		t.Fatalf("events = %#v", events)
	}
	if provider.requests[0].Messages[0].Content != "existing" {
		t.Fatalf("request messages = %#v", provider.requests[0].Messages)
	}
}

func TestRunProviderLoopUsesSteeringAndFollowUpMessages(t *testing.T) {
	providerName := "agentcore-provider-loop-3"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "tc-1", "name": "echo", "arguments": map[string]any{"value": "first"}},
			},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "intermediate",
			Content:    []any{map[string]any{"type": "text", "text": "intermediate"}},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "final",
			Content:    []any{map[string]any{"type": "text", "text": "final"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	steeringCalls := 0
	followUpCalls := 0
	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		Tools: []Tool{{
			Name: "echo",
			Execute: func(_ context.Context, call ai.ContentBlock) ToolResult {
				return ToolResult{Text: fmt.Sprintf("echoed: %v", call.Arguments["value"])}
			},
		}},
		GetSteeringMessages: func() []ai.Message {
			steeringCalls++
			if steeringCalls != 1 {
				return nil
			}
			return []ai.Message{{Role: "user", Content: "steer"}}
		},
		GetFollowUpMessages: func() []ai.Message {
			followUpCalls++
			if followUpCalls != 1 {
				return nil
			}
			return []ai.Message{{Role: "user", Content: "follow-up"}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(provider.requests) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(provider.requests))
	}
	second := provider.requests[1].Messages
	if got := second[len(second)-1].Content; got != "steer" {
		t.Fatalf("second request last content = %#v", got)
	}
	third := provider.requests[2].Messages
	if got := third[len(third)-1].Content; got != "follow-up" {
		t.Fatalf("third request last content = %#v", got)
	}
	if len(result.Messages) != 7 {
		t.Fatalf("message count = %d, want 7", len(result.Messages))
	}
}

func TestRunProviderLoopSteeringOneAtATime(t *testing.T) {
	providerName := "agentcore-provider-loop-steering-one"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "tc-1", "name": "echo", "arguments": map[string]any{"value": "first"}},
			},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "second",
			Content:    []any{map[string]any{"type": "text", "text": "second"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	queue := []ai.Message{{Role: "user", Content: "steer-1"}, {Role: "user", Content: "steer-2"}}
	_, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		Tools: []Tool{{
			Name: "echo",
			Execute: func(_ context.Context, _ ai.ContentBlock) ToolResult {
				return ToolResult{Text: "ok"}
			},
		}},
		GetSteeringMessages: func() []ai.Message {
			if len(queue) == 0 {
				return nil
			}
			next := queue[0]
			queue = queue[1:]
			return []ai.Message{next}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 1 || queue[0].Content != "steer-2" {
		t.Fatalf("queue = %#v", queue)
	}
	if got := provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Content; got != "steer-1" {
		t.Fatalf("second request last message = %#v", got)
	}
}

func TestRunProviderLoopBeforeAndAfterToolHooks(t *testing.T) {
	providerName := "agentcore-provider-loop-4"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "tc-1", "name": "blocked", "arguments": map[string]any{"value": "nope"}},
				map[string]any{"type": "toolCall", "id": "tc-2", "name": "echo", "arguments": map[string]any{"value": "ok"}},
			},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "done",
			Content:    []any{map[string]any{"type": "text", "text": "done"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	var executed []string
	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		Tools: []Tool{
			{
				Name: "blocked",
				Execute: func(_ context.Context, _ ai.ContentBlock) ToolResult {
					executed = append(executed, "blocked")
					return ToolResult{Text: "should-not-run"}
				},
			},
			{
				Name: "echo",
				Execute: func(_ context.Context, call ai.ContentBlock) ToolResult {
					executed = append(executed, call.Name)
					return ToolResult{Text: fmt.Sprintf("echoed: %v", call.Arguments["value"])}
				},
			},
		},
		BeforeToolCall: func(_ context.Context, input BeforeToolCallContext) (BeforeToolCallResult, error) {
			if input.ToolCall.Name == "blocked" {
				return BeforeToolCallResult{Block: true, Reason: "blocked by policy"}, nil
			}
			return BeforeToolCallResult{}, nil
		},
		AfterToolCall: func(_ context.Context, input AfterToolCallContext) (ToolResult, error) {
			input.Result.Text = input.Result.Text + " [wrapped]"
			input.Result.Terminate = true
			return input.Result, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(executed) != 1 || executed[0] != "echo" {
		t.Fatalf("executed tools = %#v", executed)
	}
	if len(result.Messages) != 5 {
		t.Fatalf("message count = %d, want 5", len(result.Messages))
	}
	if got := result.Messages[2]["text"]; got != "blocked by policy" {
		t.Fatalf("blocked tool result = %#v", got)
	}
	if got := result.Messages[3]["text"]; got != "echoed: ok [wrapped]" {
		t.Fatalf("after hook result = %#v", got)
	}
}

func TestRunProviderLoopStopsWhenEveryToolResultTerminates(t *testing.T) {
	providerName := "agentcore-provider-loop-terminate-batch"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "toolUse",
		Content: []any{
			map[string]any{"type": "toolCall", "id": "tc-1", "name": "one", "arguments": map[string]any{}},
			map[string]any{"type": "toolCall", "id": "tc-2", "name": "two", "arguments": map[string]any{}},
		},
	}}}
	ai.RegisterProvider(providerName, provider)

	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		Tools: []Tool{
			{Name: "one", Execute: func(_ context.Context, _ ai.ContentBlock) ToolResult { return ToolResult{Text: "one", Terminate: true} }},
			{Name: "two", Execute: func(_ context.Context, _ ai.ContentBlock) ToolResult { return ToolResult{Text: "two", Terminate: true} }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(provider.requests))
	}
	if len(result.Messages) != 4 {
		t.Fatalf("messages = %#v", result.Messages)
	}
}

func TestRunProviderLoopStreamsAssistantUpdates(t *testing.T) {
	providerName := "agentcore-provider-loop-streaming"
	ai.RegisterProvider(providerName, scriptedProviderFunc(func(context.Context, ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		return ai.NormalizedResult{
				Role:       "assistant",
				StopReason: "stop",
				Text:       "hello world",
				Content:    []any{map[string]any{"type": "text", "text": "hello world"}},
			}, []ai.NormalizedEvent{
				{Type: "start"},
				{Type: "text_start", ContentIdx: 0},
				{Type: "text_delta", ContentIdx: 0, Delta: "hello"},
				{Type: "text_delta", ContentIdx: 0, Delta: " world"},
				{Type: "text_end", ContentIdx: 0, Content: "hello world"},
				{Type: "done", Reason: "stop"},
			}, nil
	}))

	var updates []string
	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		Options:  ai.ChatOptions{Stream: true},
		EventSink: func(event Event) {
			if event["type"] == "message_update" {
				updates = append(updates, event["assistantEventType"].(string))
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) < 3 {
		t.Fatalf("updates = %#v", updates)
	}
	if got := result.Messages[1]["text"]; got != "hello world" {
		t.Fatalf("assistant text = %#v", got)
	}
}

func TestRunProviderLoopValidatesAndPreparesToolArguments(t *testing.T) {
	providerName := "agentcore-provider-loop-validated-tool"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "tc-1", "name": "echo", "arguments": map[string]any{"count": "4", "value": 9}},
			},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "done",
			Content:    []any{map[string]any{"type": "text", "text": "done"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	var seenArgs map[string]any
	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		ToolSpecs: []ai.Tool{{
			Name: "echo",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer"},
					"value": map[string]any{"type": "string"},
				},
				"required":             []any{"count", "value"},
				"additionalProperties": false,
			},
		}},
		Tools: []Tool{{
			Name: "echo",
			PrepareArguments: func(args map[string]any) (map[string]any, error) {
				args["value"] = fmt.Sprintf("prepared:%v", args["value"])
				return args, nil
			},
			Execute: func(_ context.Context, call ai.ContentBlock) ToolResult {
				seenArgs = call.Arguments
				return ToolResult{Text: "ok"}
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := seenArgs["count"]; got != float64(4) {
		t.Fatalf("count = %#v", got)
	}
	if got := seenArgs["value"]; got != "prepared:9" {
		t.Fatalf("value = %#v", got)
	}
	if got := result.Messages[2]["text"]; got != "ok" {
		t.Fatalf("tool result text = %#v", got)
	}
}

func TestRunProviderLoopPreparesArgumentsBeforeValidationAndBeforeHook(t *testing.T) {
	providerName := "agentcore-provider-loop-prepare-before-validate"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "tc-1", "name": "echo", "arguments": map[string]any{"count": "4"}},
			},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "done",
			Content:    []any{map[string]any{"type": "text", "text": "done"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	var beforeArgs map[string]any
	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		ToolSpecs: []ai.Tool{{
			Name: "echo",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer"},
				},
				"required":             []any{"count"},
				"additionalProperties": false,
			},
		}},
		Tools: []Tool{{
			Name: "echo",
			PrepareArguments: func(args map[string]any) (map[string]any, error) {
				args["count"] = 4
				return args, nil
			},
			Execute: func(_ context.Context, call ai.ContentBlock) ToolResult {
				return ToolResult{Text: fmt.Sprintf("count:%v", call.Arguments["count"])}
			},
		}},
		BeforeToolCall: func(_ context.Context, input BeforeToolCallContext) (BeforeToolCallResult, error) {
			beforeArgs = input.Args
			return BeforeToolCallResult{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if beforeArgs["count"] != 4 && beforeArgs["count"] != float64(4) {
		t.Fatalf("before hook args = %#v", beforeArgs)
	}
	if got := result.Messages[2]["text"]; got != "count:4" {
		t.Fatalf("tool result text = %#v", got)
	}
}

func TestRunProviderLoopRejectsInvalidToolArguments(t *testing.T) {
	providerName := "agentcore-provider-loop-invalid-tool"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "tc-1", "name": "echo", "arguments": map[string]any{"extra": true}},
			},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "done",
			Content:    []any{map[string]any{"type": "text", "text": "done"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	called := false
	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		ToolSpecs: []ai.Tool{{
			Name: "echo",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "string"},
				},
				"required":             []any{"value"},
				"additionalProperties": false,
			},
		}},
		Tools: []Tool{{
			Name: "echo",
			Execute: func(_ context.Context, call ai.ContentBlock) ToolResult {
				called = true
				return ToolResult{Text: fmt.Sprintf("%v", call.Arguments)}
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("tool should not execute")
	}
	if got := result.Messages[2]["isError"]; got != true {
		t.Fatalf("tool result isError = %#v", got)
	}
}

func TestRunProviderLoopForwardsExtendedChatOptions(t *testing.T) {
	providerName := "agentcore-provider-loop-options"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "done",
		Content:    []any{map[string]any{"type": "text", "text": "done"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	_, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
		Options: ai.ChatOptions{
			Transport:        "websocket",
			SessionID:        "session-1",
			CacheRetention:   ai.CacheRetentionLong,
			ReasoningEffort:  "high",
			ReasoningSummary: "detailed",
			ServiceTier:      "priority",
			TextVerbosity:    "high",
			Metadata:         map[string]any{"user_id": "u1"},
			MaxRetries:       2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d", len(provider.requests))
	}
	options := provider.requests[0].Options
	if options.Transport != "websocket" || options.SessionID != "session-1" || options.CacheRetention != ai.CacheRetentionLong {
		t.Fatalf("options transport/session/cache = %#v", options)
	}
	if options.ReasoningEffort != "high" || options.ReasoningSummary != "detailed" || options.ServiceTier != "priority" || options.TextVerbosity != "high" {
		t.Fatalf("reasoning/service/text options = %#v", options)
	}
	if options.Metadata["user_id"] != "u1" || options.MaxRetries != 2 {
		t.Fatalf("metadata/retries = %#v", options)
	}
}

func TestRunProviderLoopParallelToolResultMessagesStayInSourceOrder(t *testing.T) {
	providerName := "agentcore-provider-loop-parallel-source-order"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "toolUse",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "slow", "name": "slow", "arguments": map[string]any{}},
				map[string]any{"type": "toolCall", "id": "fast", "name": "fast", "arguments": map[string]any{}},
			},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "done",
			Content:    []any{map[string]any{"type": "text", "text": "done"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	var ended []string
	var messageEnds []string
	result, err := RunProviderLoop(context.Background(), ProviderLoopInput{
		Prompts:       []string{"go"},
		Provider:      providerName,
		Model:         "test",
		ToolExecution: ToolExecutionParallel,
		Tools: []Tool{
			{
				Name: "slow",
				Execute: func(_ context.Context, _ ai.ContentBlock) ToolResult {
					time.Sleep(40 * time.Millisecond)
					return ToolResult{Text: "slow"}
				},
			},
			{
				Name: "fast",
				Execute: func(_ context.Context, _ ai.ContentBlock) ToolResult {
					return ToolResult{Text: "fast"}
				},
			},
		},
		EventSink: func(event Event) {
			if event["type"] == "tool_execution_end" {
				ended = append(ended, event["toolCallId"].(string))
			}
			if event["type"] == "message_end" {
				message, _ := event["message"].(Message)
				if message["role"] == "toolResult" {
					messageEnds = append(messageEnds, message["toolCallId"].(string))
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ended) != "[fast slow]" {
		t.Fatalf("tool execution end order = %#v", ended)
	}
	if fmt.Sprint(messageEnds) != "[slow fast]" {
		t.Fatalf("tool result message order = %#v", messageEnds)
	}
	if result.Messages[2]["toolCallId"] != "slow" || result.Messages[3]["toolCallId"] != "fast" {
		t.Fatalf("result messages = %#v", result.Messages)
	}
}
