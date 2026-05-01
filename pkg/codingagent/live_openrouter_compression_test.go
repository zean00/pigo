package codingagent

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveOpenRouterCommandCompression(t *testing.T) {
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		t.Skip("OPENROUTER_API_KEY is not set")
	}

	session := NewSession(t.TempDir(), nil)
	if _, err := session.SetModel("openrouter", "openai/gpt-4o-mini"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetCommandCompression(CommandOutputCompressionConfig{
		Mode:           CommandCompressionForce,
		EnabledFilters: []string{"generic"},
		MaxBytes:       240,
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := session.Prompt(ctx, "Use the bash tool exactly once to run this command: yes line | head -n 300. Do not answer without using the tool."); err != nil {
		t.Fatal(err)
	}

	for _, message := range session.Messages {
		if message["role"] != "toolResult" || message["toolName"] != "bash" {
			continue
		}
		details, _ := message["details"].(map[string]any)
		if details["compressed"] != true {
			t.Fatalf("tool result was not compressed: %#v", details)
		}
		if details["compressionFilter"] != "generic" || details["compressionMode"] != "force" {
			t.Fatalf("unexpected compression details: %#v", details)
		}
		return
	}
	t.Fatalf("missing compressed bash tool result: %#v", session.Messages)
}
