package ai

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamEmitsProviderStreamingEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var chunks bytes.Buffer
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tc-1\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"value\\\":\\\"ok\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n")
		chunks.WriteString("data: [DONE]\n")
		_, _ = w.Write(chunks.Bytes())
	}))
	defer server.Close()

	stream := Stream(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Tools:    []Tool{{Name: "echo"}},
		Options: ChatOptions{
			APIKey:  "test-key",
			BaseURL: server.URL + "/v1",
		},
		Messages: []Message{{Role: "user", Content: "please"}},
	})

	var events []NormalizedEvent
	for event := range stream.Events() {
		events = append(events, event)
	}
	result, err := stream.Result()
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if len(events) < 5 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Type != "start" || events[1].Type != "text_start" || events[2].Type != "text_delta" {
		t.Fatalf("unexpected leading events = %#v", events[:3])
	}
	if last := events[len(events)-1]; last.Type != "done" || last.Reason != "toolUse" {
		t.Fatalf("last event = %#v", last)
	}
}

func TestStreamSynthesizesErrorEvent(t *testing.T) {
	providerName := "stream-error-provider"
	RegisterProvider(providerName, streamTestProviderFunc(func(context.Context, CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
		return NormalizedResult{}, nil, errors.New("boom")
	}))

	stream := Stream(context.Background(), CompletionRequest{Provider: providerName, Model: "test"})
	var events []NormalizedEvent
	for event := range stream.Events() {
		events = append(events, event)
	}
	result, err := stream.Result()
	if err == nil {
		t.Fatal("expected error")
	}
	if result.StopReason != "error" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if len(events) != 2 || events[0].Type != "start" || events[1].Type != "error" {
		t.Fatalf("events = %#v", events)
	}
}

type streamTestProviderFunc func(context.Context, CompletionRequest) (NormalizedResult, []NormalizedEvent, error)

func (fn streamTestProviderFunc) Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	return fn(ctx, req)
}
