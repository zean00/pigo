package agentcore

import (
	"context"
	"fmt"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestRunProviderLoopStreamReturnsEventsAndMessages(t *testing.T) {
	providerName := "agentcore-stream-wrapper"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	stream := RunProviderLoopStream(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
	})

	var eventTypes []string
	for event := range stream.Events() {
		eventTypes = append(eventTypes, eventType(event))
	}
	messages, err := stream.Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1]["text"] != "ok" {
		t.Fatalf("messages = %#v", messages)
	}
	if fmt.Sprint(eventTypes) != "[agent_start turn_start message_start message_end message_start message_update message_end turn_end agent_end]" {
		t.Fatalf("event types = %#v", eventTypes)
	}
}

func TestAgentLoopAndContinueStreamWrappers(t *testing.T) {
	providerName := "agentcore-agent-loop-wrapper"
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

	first := AgentLoop(context.Background(), []Message{UserMessage("go")}, ProviderLoopInput{
		Provider: providerName,
		Model:    "test",
	})
	for range first.Events() {
	}
	firstMessages, err := first.Result()
	if err != nil {
		t.Fatal(err)
	}

	continueStream := AgentLoopContinue(context.Background(), ProviderLoopInput{
		History:  []ai.Message{{Role: "user", Content: "again"}},
		Provider: providerName,
		Model:    "test",
	})
	for range continueStream.Events() {
	}
	continueMessages, err := continueStream.Result()
	if err != nil {
		t.Fatal(err)
	}
	if firstMessages[1]["text"] != "first" || continueMessages[0]["text"] != "second" {
		t.Fatalf("messages = %#v %#v", firstMessages, continueMessages)
	}
}

func TestTypedEventConvertsKnownEvents(t *testing.T) {
	typed := TypedEvent(Event{
		"type":       "tool_execution_end",
		"toolCallId": "tc-1",
		"toolName":   "echo",
		"result":     map[string]any{"text": "ok"},
		"isError":    false,
		"text":       "ok",
	})
	event, ok := typed.(ToolExecutionEndEvent)
	if !ok {
		t.Fatalf("typed event = %#v", typed)
	}
	if event.Type != EventToolExecutionEnd || event.ToolCallID != "tc-1" || event.ToolName != "echo" || event.Text != "ok" {
		t.Fatalf("event = %#v", event)
	}
}

func TestAgentEventStreamTypedEvents(t *testing.T) {
	providerName := "agentcore-stream-typed-events"
	provider := &scriptedProvider{responses: []ai.NormalizedResult{{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "ok",
		Content:    []any{map[string]any{"type": "text", "text": "ok"}},
	}}}
	ai.RegisterProvider(providerName, provider)

	stream := RunProviderLoopStream(context.Background(), ProviderLoopInput{
		Prompts:  []string{"go"},
		Provider: providerName,
		Model:    "test",
	})
	var sawStart bool
	var sawEnd bool
	for event := range stream.TypedEvents() {
		switch typed := event.(type) {
		case AgentStartEvent:
			sawStart = true
		case AgentEndEvent:
			sawEnd = typed.MessageCount == 2
		}
	}
	if _, err := stream.Result(); err != nil {
		t.Fatal(err)
	}
	if !sawStart || !sawEnd {
		t.Fatalf("typed events sawStart=%v sawEnd=%v", sawStart, sawEnd)
	}
}
