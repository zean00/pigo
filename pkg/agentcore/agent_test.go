package agentcore

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestAgentPromptAndContinue(t *testing.T) {
	providerName := "agentcore-agent-1"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "first",
			Content:    []any{map[string]any{"type": "text", "text": "first"}},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "second",
			Content:    []any{map[string]any{"type": "text", "text": "second"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	agent := NewAgent(AgentOptions{Provider: providerName, Model: "test"})
	var events []string
	unsubscribe := agent.Subscribe(func(event Event, _ context.Context) {
		events = append(events, eventType(event))
	})
	defer unsubscribe()

	if err := agent.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	agent.FollowUp(UserMessage("again"))
	if err := agent.Continue(context.Background()); err != nil {
		t.Fatal(err)
	}

	state := agent.State()
	if len(state.Messages) != 4 {
		t.Fatalf("message count = %d, want 4", len(state.Messages))
	}
	if got := state.Messages[3]["text"]; got != "second" {
		t.Fatalf("last message text = %#v", got)
	}
	if len(events) == 0 || events[len(events)-1] != "agent_end" {
		t.Fatalf("events = %#v", events)
	}
}

func TestAgentSteeringQueueAndDynamicAPIKey(t *testing.T) {
	providerName := "agentcore-agent-2"
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

	var apiKeyCalls int32
	agent := NewAgent(AgentOptions{
		Provider: providerName,
		Model:    "test",
		Tools: []Tool{{
			Name: "echo",
			Execute: func(_ context.Context, call ai.ContentBlock) ToolResult {
				return ToolResult{Text: fmt.Sprintf("echoed: %v", call.Arguments["value"])}
			},
		}},
		GetAPIKey: func(provider string) string {
			if provider != providerName {
				t.Fatalf("provider = %q", provider)
			}
			atomic.AddInt32(&apiKeyCalls, 1)
			return "dynamic-key"
		},
	})
	agent.Steer(UserMessage("steer"))

	if err := agent.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	if got := atomic.LoadInt32(&apiKeyCalls); got != 2 {
		t.Fatalf("api key calls = %d, want 2", got)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(provider.requests))
	}
	if got := provider.requests[0].Options.APIKey; got != "dynamic-key" {
		t.Fatalf("first request api key = %q", got)
	}
	if got := provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Content; got != "steer" {
		t.Fatalf("second request last content = %#v", got)
	}
}

func TestAgentAbortMarksFailureState(t *testing.T) {
	providerName := "agentcore-agent-3"
	blocked := make(chan struct{})
	release := make(chan struct{})
	ai.RegisterProvider(providerName, scriptedProviderFunc(func(ctx context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		close(blocked)
		<-release
		return ai.NormalizedResult{}, nil, ctx.Err()
	}))

	agent := NewAgent(AgentOptions{Provider: providerName, Model: "test"})
	done := make(chan error, 1)
	go func() {
		done <- agent.Prompt(context.Background(), "go")
	}()
	<-blocked
	agent.Abort()
	close(release)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected abort error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not finish")
	}

	state := agent.State()
	if state.ErrorMessage == "" {
		t.Fatal("expected error message")
	}
	last := state.Messages[len(state.Messages)-1]
	if got := last["stopReason"]; got != "aborted" {
		t.Fatalf("stopReason = %#v", got)
	}
}

func TestAgentOptionsExposeProviderRuntimeFields(t *testing.T) {
	providerName := "agentcore-agent-runtime-options"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	agent := NewAgent(AgentOptions{
		Provider:      providerName,
		Model:         "test",
		SessionID:     "session-1",
		Transport:     "websocket",
		MaxRetryDelay: 5000,
		ThinkingBudgets: ai.ThinkingBudgets{
			High: 12000,
		},
		OnPayload: func(payload any, req ai.CompletionRequest) (any, error) {
			return payload, nil
		},
		OnResponse: func(response ai.ProviderResponse, req ai.CompletionRequest) error {
			return nil
		},
	})

	if err := agent.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d", len(provider.requests))
	}
	options := provider.requests[0].Options
	if options.SessionID != "session-1" || options.Transport != "websocket" {
		t.Fatalf("session/transport = %#v", options)
	}
	if options.MaxRetryDelay != 5*time.Second {
		t.Fatalf("max retry delay = %v", options.MaxRetryDelay)
	}
	if options.ThinkingBudgets.High != 12000 {
		t.Fatalf("thinking budgets = %#v", options.ThinkingBudgets)
	}
	if options.OnPayload == nil || options.OnResponse == nil {
		t.Fatalf("expected hooks to be forwarded: %#v", options)
	}
}

func TestAgentIncludesSystemPromptInProviderHistory(t *testing.T) {
	providerName := "agentcore-agent-system-prompt"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	agent := NewAgent(AgentOptions{
		SystemPrompt: "be terse",
		Provider:     providerName,
		Model:        "test",
	})

	if err := agent.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d", len(provider.requests))
	}
	messages := provider.requests[0].Messages
	if len(messages) == 0 {
		t.Fatal("missing provider messages")
	}
	if messages[0].Role != "system" || messages[0].Content != "be terse" {
		t.Fatalf("first provider message = %#v", messages[0])
	}
}

func TestAgentSubscribeAsyncWaitsBeforePromptReturns(t *testing.T) {
	providerName := "agentcore-agent-async-listener"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	agent := NewAgent(AgentOptions{Provider: providerName, Model: "test"})
	agentEndStarted := make(chan struct{})
	release := make(chan struct{})
	agentEndFinished := make(chan struct{})
	agent.SubscribeAsync(func(event Event, ctx context.Context) error {
		if eventType(event) != "agent_end" {
			return nil
		}
		if ctx == nil {
			t.Fatal("missing active context")
		}
		close(agentEndStarted)
		<-release
		close(agentEndFinished)
		return nil
	})

	done := make(chan error, 1)
	go func() {
		done <- agent.Prompt(context.Background(), "go")
	}()
	select {
	case <-agentEndStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("async listener was not called")
	}
	select {
	case <-done:
		t.Fatal("prompt returned before async listener finished")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not finish")
	}
	select {
	case <-agentEndFinished:
	default:
		t.Fatal("listener did not finish")
	}
}

func TestAgentTypedSubscribersReceiveTypedEvents(t *testing.T) {
	providerName := "agentcore-agent-typed-listener"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	agent := NewAgent(AgentOptions{Provider: providerName, Model: "test"})
	var gotEnd bool
	agent.SubscribeTyped(func(event any, _ context.Context) {
		if typed, ok := event.(AgentEndEvent); ok && typed.MessageCount == 2 {
			gotEnd = true
		}
	})
	if err := agent.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if !gotEnd {
		t.Fatal("typed agent_end event not received")
	}
}

func TestAgentTypedAsyncSubscriberErrorReturnsFromPrompt(t *testing.T) {
	providerName := "agentcore-agent-typed-async-listener-error"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	wantErr := errors.New("typed listener failed")
	agent := NewAgent(AgentOptions{Provider: providerName, Model: "test"})
	agent.SubscribeTypedAsync(func(event any, _ context.Context) error {
		if _, ok := event.(MessageEvent); ok {
			return wantErr
		}
		return nil
	})
	err := agent.Prompt(context.Background(), "go")
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestAgentSubscribeAsyncErrorReturnsFromPrompt(t *testing.T) {
	providerName := "agentcore-agent-async-listener-error"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	wantErr := errors.New("listener failed")
	agent := NewAgent(AgentOptions{Provider: providerName, Model: "test"})
	agent.SubscribeAsync(func(event Event, _ context.Context) error {
		if eventType(event) == "message_end" {
			return wantErr
		}
		return nil
	})

	err := agent.Prompt(context.Background(), "go")
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestAgentInitialStateSettersAndPromptImages(t *testing.T) {
	providerName := "agentcore-agent-images"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	agent := NewAgent(AgentOptions{
		InitialState: &AgentState{
			SystemPrompt: "from-state",
			Provider:     providerName,
			Model:        "test",
			Messages:     []Message{UserMessage("old")},
		},
	})
	agent.SetMessages(nil)
	agent.SetSystemPrompt("updated")

	if err := agent.PromptWithImages(context.Background(), "look", []ai.ContentBlock{{Data: "abc", MimeType: "image/png"}}); err != nil {
		t.Fatal(err)
	}

	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d", len(provider.requests))
	}
	messages := provider.requests[0].Messages
	if messages[0].Role != "system" || messages[0].Content != "updated" {
		t.Fatalf("system message = %#v", messages[0])
	}
	content, ok := messages[1].Content.([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("prompt content = %#v", messages[1].Content)
	}
	image, ok := content[1].(map[string]any)
	if !ok || image["type"] != "image" || image["mimeType"] != "image/png" || image["data"] != "abc" {
		t.Fatalf("image block = %#v", content[1])
	}
	blocks := ai.ParseContentBlocks(messages[1].Content)
	if len(blocks) != 2 || blocks[1].Type != "image" || blocks[1].MimeType != "image/png" {
		t.Fatalf("provider prompt blocks = %#v", blocks)
	}
	state := agent.State()
	if len(state.Messages) < 1 {
		t.Fatalf("state messages = %#v", state.Messages)
	}
	userContent, ok := state.Messages[0]["content"].([]any)
	if !ok || len(userContent) != 2 {
		t.Fatalf("state user content = %#v", state.Messages[0]["content"])
	}
}

func TestAgentPromptWithImagesPreservesHistoryOnContinue(t *testing.T) {
	providerName := "agentcore-agent-images-continue"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "first",
			Content:    []any{map[string]any{"type": "text", "text": "first"}},
		},
		{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "second",
			Content:    []any{map[string]any{"type": "text", "text": "second"}},
		},
	}}
	ai.RegisterProvider(providerName, provider)

	agent := NewAgent(AgentOptions{Provider: providerName, Model: "test"})
	if err := agent.PromptWithImages(context.Background(), "look", []ai.ContentBlock{{Data: "abc", MimeType: "image/png"}}); err != nil {
		t.Fatal(err)
	}
	agent.FollowUp(UserMessage("again"))
	if err := agent.Continue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider calls = %d", len(provider.requests))
	}
	blocks := ai.ParseContentBlocks(provider.requests[1].Messages[0].Content)
	if len(blocks) != 2 || blocks[1].Type != "image" || blocks[1].Data != "abc" {
		t.Fatalf("continued history blocks = %#v", blocks)
	}
}

func TestAgentSetModelUpdatesProviderRequests(t *testing.T) {
	firstProvider := "agentcore-agent-set-model-first"
	secondProvider := "agentcore-agent-set-model-second"
	ai.RegisterProvider(firstProvider, &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "first",
		Content:    []any{map[string]any{"type": "text", "text": "first"}},
	}}})
	second := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "second",
		Content:    []any{map[string]any{"type": "text", "text": "second"}},
	}}}
	ai.RegisterProvider(secondProvider, second)

	agent := NewAgent(AgentOptions{Provider: firstProvider, Model: "first-model"})
	agent.SetModel(secondProvider, "second-model")
	if err := agent.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(second.requests) != 1 {
		t.Fatalf("second provider calls = %d", len(second.requests))
	}
	if second.requests[0].Model != "second-model" {
		t.Fatalf("request model = %q", second.requests[0].Model)
	}
}

func TestAgentConversionAndContextHooks(t *testing.T) {
	providerName := "agentcore-agent-conversion-hooks"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	agent := NewAgent(AgentOptions{
		Provider: providerName,
		Model:    "test",
		TransformMessages: func(ctx context.Context, messages []Message) ([]Message, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			messages[0]["text"] = "converted"
			return messages, nil
		},
		ConvertToLLM: func(messages []Message) ([]ai.Message, error) {
			return []ai.Message{{Role: "user", Content: messages[0]["text"]}}, nil
		},
		TransformContextFunc: func(ctx context.Context, messages []ai.Message) ([]ai.Message, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			messages = append(messages, ai.Message{Role: "user", Content: "extra"})
			return messages, nil
		},
	})

	if err := agent.Prompt(context.Background(), "original"); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d", len(provider.requests))
	}
	messages := provider.requests[0].Messages
	if len(messages) != 2 || messages[0].Content != "converted" || messages[1].Content != "extra" {
		t.Fatalf("provider messages = %#v", messages)
	}
}

type scriptedProviderFunc func(ctx context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error)

func (fn scriptedProviderFunc) Complete(ctx context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
	return fn(ctx, req)
}
