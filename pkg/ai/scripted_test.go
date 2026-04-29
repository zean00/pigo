package ai

import "testing"

func TestNormalizeTextResponse(t *testing.T) {
	result, events := NormalizeResponse(NewTextResponse("hello"))
	if result.Text != "hello" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.StopReason != "stop" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if len(events) != 5 {
		t.Fatalf("events len = %d", len(events))
	}
	if events[1].Type != "text_start" || events[len(events)-1].Type != "done" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestNormalizeToolResponse(t *testing.T) {
	result, events := NormalizeResponse(NewToolResponse("call-1", "math", map[string]any{"a": float64(1)}))
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content len = %d", len(result.Content))
	}
	if events[1].Type != "toolcall_start" || events[3].ToolCall == nil {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestNormalizeErrorResponseOmitsUsage(t *testing.T) {
	result, events := NormalizeResponse(NewErrorResponse())
	if result.Usage != nil {
		t.Fatalf("usage = %#v, want nil", result.Usage)
	}
	if events[len(events)-1].Type != "error" {
		t.Fatalf("last event = %#v", events[len(events)-1])
	}
}
