package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestStreamProxyReconstructsAssistantEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/stream" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		events := []ProxyAssistantMessageEvent{
			{Type: "start"},
			{Type: "text_start", ContentIndex: 0},
			{Type: "text_delta", ContentIndex: 0, Delta: "he"},
			{Type: "text_delta", ContentIndex: 0, Delta: "llo"},
			{Type: "text_end", ContentIndex: 0},
			{Type: "done", Reason: "stop", Usage: &ai.Usage{Input: 1, Output: 2, TotalTokens: 3}},
		}
		for _, event := range events {
			data, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}))
	defer server.Close()

	stream := StreamProxy(context.Background(), ai.Model{
		ID:       "model",
		API:      "api",
		Provider: "provider",
	}, []ai.Message{{Role: "user", Content: "hi"}}, ProxyStreamOptions{
		AuthToken: "token",
		ProxyURL:  server.URL,
	})

	var eventTypes []string
	for event := range stream.Events() {
		eventTypes = append(eventTypes, event.Type)
	}
	result, err := stream.Result()
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "stop" || result.Text != "hello" || result.Usage == nil || result.Usage.TotalTokens != 3 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v", result.Content)
	}
	textBlock, ok := result.Content[0].(map[string]any)
	if !ok || textBlock["text"] != "hello" {
		t.Fatalf("text block = %#v", result.Content[0])
	}
	want := []string{"start", "text_start", "text_delta", "text_delta", "text_end", "done"}
	if fmt.Sprint(eventTypes) != fmt.Sprint(want) {
		t.Fatalf("events = %#v, want %#v", eventTypes, want)
	}
}
