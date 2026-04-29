package agentcore

import (
	"context"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestRunScriptedLoopExecutesToolAndContinues(t *testing.T) {
	result, err := RunScriptedLoop(context.Background(), ScriptedLoopInput{
		Prompts: []string{"start"},
		Tools: []Tool{{
			Name: "echo",
			Execute: func(_ context.Context, call ai.ContentBlock) ToolResult {
				if call.Arguments["value"] != "hello" {
					t.Fatalf("unexpected tool args: %#v", call.Arguments)
				}
				return ToolResult{Text: "echoed: hello"}
			},
		}},
		Turns: []AssistantTurn{
			{
				StopReason: "toolUse",
				Content: []ai.ContentBlock{{
					Type:      "toolCall",
					ID:        "tool-1",
					Name:      "echo",
					Arguments: map[string]any{"value": "hello"},
				}},
			},
			{
				StopReason: "stop",
				Content:    []ai.ContentBlock{{Type: "text", Text: "done"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	roles := make([]string, 0, len(result.Messages))
	for _, message := range result.Messages {
		roles = append(roles, message["role"].(string))
	}
	wantRoles := []string{"user", "assistant", "toolResult", "assistant"}
	if !equalStrings(roles, wantRoles) {
		t.Fatalf("roles = %#v, want %#v", roles, wantRoles)
	}

	toolEnd := findEvent(result.Events, "tool_execution_end")
	if toolEnd == nil {
		t.Fatal("missing tool_execution_end")
	}
	if toolEnd["isError"] != false {
		t.Fatalf("tool_execution_end isError = %#v", toolEnd["isError"])
	}
}

func TestRunScriptedLoopMissingToolProducesErrorResult(t *testing.T) {
	result, err := RunScriptedLoop(context.Background(), ScriptedLoopInput{
		Prompts: []string{"start"},
		Turns: []AssistantTurn{{
			StopReason: "toolUse",
			Content: []ai.ContentBlock{{
				Type:      "toolCall",
				ID:        "tool-1",
				Name:      "missing",
				Arguments: map[string]any{},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	toolEnd := findEvent(result.Events, "tool_execution_end")
	if toolEnd == nil {
		t.Fatal("missing tool_execution_end")
	}
	if toolEnd["isError"] != true {
		t.Fatalf("tool_execution_end isError = %#v", toolEnd["isError"])
	}
}

func TestRunScriptedLoopExecutesMultipleToolsInOneTurn(t *testing.T) {
	result, err := RunScriptedLoop(context.Background(), ScriptedLoopInput{
		Prompts: []string{"start"},
		Tools: []Tool{
			{
				Name: "first",
				Execute: func(_ context.Context, _ ai.ContentBlock) ToolResult {
					return ToolResult{Text: "first: alpha"}
				},
			},
			{
				Name: "second",
				Execute: func(_ context.Context, _ ai.ContentBlock) ToolResult {
					return ToolResult{Text: "second: beta"}
				},
			},
		},
		Turns: []AssistantTurn{
			{
				StopReason: "toolUse",
				Content: []ai.ContentBlock{
					{
						Type:      "toolCall",
						ID:        "tool-1",
						Name:      "first",
						Arguments: map[string]any{"value": "alpha"},
					},
					{
						Type:      "toolCall",
						ID:        "tool-2",
						Name:      "second",
						Arguments: map[string]any{"value": "beta"},
					},
				},
			},
			{
				StopReason: "stop",
				Content:    []ai.ContentBlock{{Type: "text", Text: "done"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	roles := make([]string, 0, len(result.Messages))
	for _, message := range result.Messages {
		roles = append(roles, message["role"].(string))
	}
	wantRoles := []string{"user", "assistant", "toolResult", "toolResult", "assistant"}
	if !equalStrings(roles, wantRoles) {
		t.Fatalf("roles = %#v, want %#v", roles, wantRoles)
	}

	toolEnds := 0
	for _, event := range result.Events {
		if event["type"] == "tool_execution_end" {
			toolEnds++
		}
	}
	if toolEnds != 2 {
		t.Fatalf("tool execution end events = %d, want 2", toolEnds)
	}
}

func findEvent(events []Event, eventType string) Event {
	for _, event := range events {
		if event["type"] == eventType {
			return event
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
