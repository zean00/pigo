package ai

import (
	"encoding/json"
	"testing"
)

func TestParseContentBlocksPreservesProviderSignatures(t *testing.T) {
	raw := []any{
		map[string]any{"type": "text", "text": "hello", "textSignature": "txt-1"},
		map[string]any{"type": "thinking", "thinking": "hmm", "thinkingSignature": "think-1", "redacted": true},
		map[string]any{
			"type":             "toolCall",
			"id":               "call-1",
			"name":             "search",
			"arguments":        map[string]any{"query": "pi"},
			"thoughtSignature": "thought-1",
		},
	}

	blocks := ParseContentBlocks(raw)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].TextSignature != "txt-1" {
		t.Fatalf("textSignature = %q", blocks[0].TextSignature)
	}
	if blocks[1].ThinkingSignature != "think-1" || !blocks[1].Redacted {
		t.Fatalf("thinking block = %#v", blocks[1])
	}
	if blocks[2].ID != "call-1" || blocks[2].ThoughtSignature != "thought-1" {
		t.Fatalf("tool block = %#v", blocks[2])
	}
}

func TestNormalizedContentPreservesProviderSignatures(t *testing.T) {
	content := NormalizedContent([]ContentBlock{
		{Type: "text", Text: "hello", TextSignature: "txt-1"},
		{Type: "thinking", Thinking: "hmm", ThinkingSignature: "think-1", Redacted: true},
		{Type: "toolCall", ID: "call-1", Name: "search", Arguments: map[string]any{"query": "pi"}, ThoughtSignature: "thought-1"},
	})

	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded[0]["textSignature"] != "txt-1" {
		t.Fatalf("text item = %#v", decoded[0])
	}
	if decoded[1]["thinkingSignature"] != "think-1" || decoded[1]["redacted"] != true {
		t.Fatalf("thinking item = %#v", decoded[1])
	}
	if decoded[2]["id"] != "call-1" || decoded[2]["thoughtSignature"] != "thought-1" {
		t.Fatalf("tool item = %#v", decoded[2])
	}
	if decoded[2]["hasId"] != true {
		t.Fatalf("tool hasId = %#v", decoded[2])
	}
}

func TestAssistantEventsPreserveToolIDAndThoughtSignature(t *testing.T) {
	events := AssistantEvents([]ContentBlock{
		{Type: "toolCall", ID: "call-1", Name: "search", Arguments: map[string]any{"query": "pi"}, ThoughtSignature: "thought-1"},
	}, "toolUse")

	if len(events) < 4 || events[3].ToolCall == nil {
		t.Fatalf("events = %#v", events)
	}
	toolCall := events[3].ToolCall
	if toolCall.ID != "call-1" || !toolCall.HasID || toolCall.ThoughtSignature != "thought-1" {
		t.Fatalf("toolCall = %#v", toolCall)
	}
}

func TestAttachEventPayloadsAddsTypeScriptEventPayloads(t *testing.T) {
	result := NormalizedResult{
		Role:       "assistant",
		StopReason: "stop",
		Text:       "hello",
		Content:    NormalizedContent([]ContentBlock{{Type: "text", Text: "hello"}}),
	}
	events := AttachEventPayloads([]NormalizedEvent{
		{Type: "start"},
		{Type: "text_delta", ContentIdx: 0, Delta: "hello"},
		{Type: "done", Reason: "stop"},
	}, result)

	if events[0].Partial == nil || events[0].Partial.Text != "hello" {
		t.Fatalf("start event = %#v", events[0])
	}
	if events[1].Partial == nil || events[1].Partial.Text != "hello" {
		t.Fatalf("delta event = %#v", events[1])
	}
	if events[2].Message == nil || events[2].Message.Text != "hello" {
		t.Fatalf("done event = %#v", events[2])
	}
}

func TestAttachEventPayloadsAddsErrorPayload(t *testing.T) {
	result := NormalizedResult{Role: "assistant", StopReason: "error"}
	events := AttachEventPayloads([]NormalizedEvent{
		{Type: "error", Reason: "error", ErrorMessage: "boom"},
	}, result)

	if events[0].Error == nil || events[0].Error.ErrorMessage != "boom" {
		t.Fatalf("error event = %#v", events[0])
	}
}

func TestNormalizedEventJSONMatchesTypeScriptShape(t *testing.T) {
	result := NormalizedResult{Role: "assistant", StopReason: "toolUse"}
	events := AttachEventPayloads([]NormalizedEvent{
		{
			Type:       "toolcall_end",
			ContentIdx: 0,
			ToolCall: &NormalizedTool{
				ID:               "call-1",
				Name:             "search",
				Arguments:        map[string]any{"query": "pi"},
				HasID:            true,
				ThoughtSignature: "thought-1",
			},
		},
		{Type: "done", Reason: "toolUse"},
	}, result)

	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded[0]["contentIndex"] != float64(0) {
		t.Fatalf("event = %#v", decoded[0])
	}
	if _, ok := decoded[0]["errorMessage"]; ok {
		t.Fatalf("unexpected errorMessage = %#v", decoded[0])
	}
	tool, _ := decoded[0]["toolCall"].(map[string]any)
	if tool["type"] != "toolCall" || tool["id"] != "call-1" || tool["thoughtSignature"] != "thought-1" {
		t.Fatalf("tool = %#v", tool)
	}
	if tool["hasId"] != true {
		t.Fatalf("missing hasId = %#v", tool)
	}
	if decoded[1]["message"] == nil {
		t.Fatalf("done event = %#v", decoded[1])
	}
}

func TestNormalizedErrorEventJSONMatchesTypeScriptShape(t *testing.T) {
	events := AttachEventPayloads([]NormalizedEvent{
		{Type: "error", Reason: "error", ErrorMessage: "boom"},
	}, NormalizedResult{Role: "assistant", StopReason: "error"})

	data, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["error"] == nil || decoded["errorMessage"] != nil {
		t.Fatalf("event = %#v", decoded)
	}
}

func TestFillResultMetadataAddsAssistantMessageFields(t *testing.T) {
	ResetModels()
	result := FillResultMetadata(NormalizedResult{StopReason: "stop"}, CompletionRequest{
		Provider: "openai",
		Model:    "gpt-5.4",
	})

	if result.Role != "assistant" {
		t.Fatalf("role = %q", result.Role)
	}
	if result.Provider != "openai" || result.Model != "gpt-5.4" {
		t.Fatalf("provider/model = %q/%q", result.Provider, result.Model)
	}
	if result.API == "" {
		t.Fatalf("api = %q", result.API)
	}
	if result.Timestamp == 0 {
		t.Fatal("expected timestamp")
	}
	if result.Usage == nil {
		t.Fatal("expected usage")
	}
}

func TestPrepareMessagesForModelDowngradesUnsupportedImages(t *testing.T) {
	model := Model{ID: "text-only", Provider: "openai", API: "openai-completions", Input: []string{"text"}}
	messages := PrepareMessagesForModel([]Message{{
		Role: "user",
		Content: []any{
			map[string]any{"type": "image", "data": "abc", "mimeType": "image/png"},
			map[string]any{"type": "image", "data": "def", "mimeType": "image/png"},
			map[string]any{"type": "text", "text": "after"},
		},
	}}, model)

	blocks := ParseContentBlocks(messages[0].Content)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].Type != "text" || blocks[0].Text != nonVisionUserImagePlaceholder {
		t.Fatalf("first block = %#v", blocks[0])
	}
	if blocks[1].Text != "after" {
		t.Fatalf("second block = %#v", blocks[1])
	}
}

func TestPrepareMessagesForModelSynthesizesMissingToolResult(t *testing.T) {
	model := Model{ID: "claude", Provider: "anthropic", API: "anthropic-messages", Input: []string{"text"}}
	messages := PrepareMessagesForModel([]Message{
		{
			Role:       "assistant",
			Provider:   "openai",
			API:        "openai-responses",
			Model:      "gpt-5.4",
			StopReason: "toolUse",
			Content: NormalizedContent([]ContentBlock{{
				Type:      "toolCall",
				ID:        "call|item",
				Name:      "search",
				Arguments: map[string]any{"query": "pi"},
			}}),
		},
		{Role: "user", Content: "continue"},
	}, model)

	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[1].Role != "toolResult" || !messages[1].IsError {
		t.Fatalf("synthetic tool result = %#v", messages[1])
	}
	if messages[1].ToolCallID != "call_item" {
		t.Fatalf("toolCallId = %q", messages[1].ToolCallID)
	}
	blocks := ParseContentBlocks(messages[1].Content)
	if len(blocks) != 1 || blocks[0].Text != "No result provided" {
		t.Fatalf("synthetic blocks = %#v", blocks)
	}
}

func TestPrepareMessagesForModelDropsErroredAssistantReplay(t *testing.T) {
	model := Model{ID: "gpt-5.4", Provider: "openai", API: "openai-responses", Input: []string{"text"}}
	messages := PrepareMessagesForModel([]Message{
		{Role: "assistant", Provider: "openai", API: "openai-responses", Model: "gpt-5.4", StopReason: "error", Content: "partial"},
		{Role: "user", Content: "retry"},
	}, model)

	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestPrepareMessagesForModelConvertsCrossModelThinkingToText(t *testing.T) {
	model := Model{ID: "target", Provider: "anthropic", API: "anthropic-messages", Input: []string{"text"}}
	messages := PrepareMessagesForModel([]Message{{
		Role:       "assistant",
		Provider:   "openai",
		API:        "openai-responses",
		Model:      "source",
		StopReason: "stop",
		Content: NormalizedContent([]ContentBlock{{
			Type:              "thinking",
			Thinking:          "internal reasoning",
			ThinkingSignature: "sig",
		}}),
	}}, model)

	blocks := ParseContentBlocks(messages[0].Content)
	if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "internal reasoning" {
		t.Fatalf("blocks = %#v", blocks)
	}
}
