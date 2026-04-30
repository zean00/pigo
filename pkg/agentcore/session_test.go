package agentcore

import "testing"

func TestAgentSessionFacadeWrapsAgentState(t *testing.T) {
	session := NewAgentSession(AgentOptions{
		SystemPrompt: "system",
		Messages:     []Message{UserMessage("hello")},
	})
	if session.State().SystemPrompt != "system" {
		t.Fatalf("state = %#v", session.State())
	}
	if len(session.Messages()) != 1 {
		t.Fatalf("messages = %#v", session.Messages())
	}
	session.ReplaceMessages([]Message{UserMessage("next")})
	if session.Messages()[0]["text"] != "next" {
		t.Fatalf("messages = %#v", session.Messages())
	}
}

func TestCustomMessageHelpers(t *testing.T) {
	message := NewCustomMessage("demo", map[string]any{"value": 1}, true, map[string]any{"path": "x"})
	custom, ok := AsCustomMessage(message)
	if !ok {
		t.Fatalf("not custom: %#v", message)
	}
	if custom.CustomType != "demo" || custom.Display != true {
		t.Fatalf("custom = %#v", custom)
	}
	if IsCustomMessage(UserMessage("hello")) {
		t.Fatal("user message should not be custom")
	}
}
