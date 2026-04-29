package conformance

import (
	"encoding/json"
	"fmt"
	"strings"
)

func VerifyAgentConformanceOutput(testCase AgentCase, output AgentOutput) VerificationResult {
	errors := []string{}
	if output.Case != testCase.Name {
		errors = append(errors, fmt.Sprintf("case name mismatch: expected %s, got %s", testCase.Name, output.Case))
	}
	expect := testCase.Expect
	if len(expect.EventTypesInOrder) > 0 {
		eventTypes := make([]string, 0, len(output.Events))
		for _, event := range output.Events {
			eventTypes = append(eventTypes, normalizedStringField(event, "type"))
		}
		if !containsInOrder(eventTypes, expect.EventTypesInOrder) {
			errors = append(errors, fmt.Sprintf("event order mismatch: expected subsequence %s", strings.Join(expect.EventTypesInOrder, " -> ")))
		}
	}
	if len(expect.FinalMessageRoles) > 0 {
		roles := make([]string, 0, len(output.Messages))
		for _, message := range output.Messages {
			roles = append(roles, normalizedStringField(message, "role"))
		}
		if !stringSlicesEqual(roles, expect.FinalMessageRoles) {
			errors = append(errors, fmt.Sprintf("final message roles mismatch: expected %s, got %s", strings.Join(expect.FinalMessageRoles, ", "), strings.Join(roles, ", ")))
		}
	}
	for _, text := range expect.FinalTextContains {
		found := false
		for _, message := range output.Messages {
			if strings.Contains(normalizedStringField(message, "text"), text) {
				found = true
				break
			}
		}
		if !found {
			errors = append(errors, fmt.Sprintf("final messages do not contain %q", text))
		}
	}
	for _, expected := range expect.ToolResults {
		actual, ok := findNormalizedToolResult(output.Messages, expected.ToolName, expected.ToolCallID)
		if !ok {
			errors = append(errors, fmt.Sprintf("missing tool result %s", expected.ToolName))
			continue
		}
		if expected.TextContains != "" && !strings.Contains(normalizedStringField(actual, "text"), expected.TextContains) {
			errors = append(errors, fmt.Sprintf("tool result %s does not contain %q", expected.ToolName, expected.TextContains))
		}
		if expected.IsError != nil && normalizedBoolField(actual, "isError") != *expected.IsError {
			errors = append(errors, fmt.Sprintf("tool result %s isError mismatch: expected %v", expected.ToolName, *expected.IsError))
		}
	}
	for _, expected := range expect.ToolExecutions {
		actual, ok := findNormalizedToolExecution(output.Events, expected.ToolName, expected.ToolCallID)
		if !ok {
			errors = append(errors, fmt.Sprintf("missing tool execution %s", expected.ToolName))
			continue
		}
		if expected.IsError != nil && normalizedBoolField(actual, "isError") != *expected.IsError {
			errors = append(errors, fmt.Sprintf("tool execution %s isError mismatch: expected %v", expected.ToolName, *expected.IsError))
		}
	}
	return VerificationResult{OK: len(errors) == 0, Errors: errors}
}

func containsInOrder(actual, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	index := 0
	for _, item := range actual {
		if item == expected[index] {
			index++
		}
		if index == len(expected) {
			return true
		}
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
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

func findNormalizedToolResult(messages []any, toolName, toolCallID string) (any, bool) {
	for _, message := range messages {
		if normalizedStringField(message, "role") != "toolResult" || normalizedStringField(message, "toolName") != toolName {
			continue
		}
		if toolCallID == "" || normalizedStringField(message, "toolCallId") == toolCallID {
			return message, true
		}
	}
	return nil, false
}

func findNormalizedToolExecution(events []any, toolName, toolCallID string) (any, bool) {
	for _, event := range events {
		if normalizedStringField(event, "type") != "tool_execution_end" || normalizedStringField(event, "toolName") != toolName {
			continue
		}
		if toolCallID == "" || normalizedStringField(event, "toolCallId") == toolCallID {
			return event, true
		}
	}
	return nil, false
}

func normalizedStringField(value any, field string) string {
	object := normalizedMap(value)
	text, _ := object[field].(string)
	return text
}

func normalizedBoolField(value any, field string) bool {
	object := normalizedMap(value)
	flag, _ := object[field].(bool)
	return flag
}

func normalizedMap(value any) map[string]any {
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}
