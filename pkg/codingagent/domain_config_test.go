package codingagent

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestDefaultDomainConfigPreservesCodingPrompt(t *testing.T) {
	root := t.TempDir()
	if err := WriteWorkspaceFile(root, "go.mod", "module example.com/test\n"); err != nil {
		t.Fatal(err)
	}
	session := NewSession(root, nil)
	prompt := session.HeadlessSystemPrompt()
	if !strings.Contains(prompt, "headless coding agent") {
		t.Fatalf("missing coding prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Project context:") || !strings.Contains(prompt, "go.mod") {
		t.Fatalf("missing package context: %s", prompt)
	}
}

func TestDomainConfigPurposeAndContextToggles(t *testing.T) {
	root := t.TempDir()
	if err := WriteWorkspaceFile(root, "go.mod", "module example.com/test\n"); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspaceFile(root, "CUSTOM.md", "custom instructions"); err != nil {
		t.Fatal(err)
	}
	git := false
	packages := false
	session := NewSession(root, nil)
	if err := session.SetDomainConfig(SessionDomainConfig{
		Purpose:               SessionPurposeReadonly,
		ContextFiles:          []string{"CUSTOM.md"},
		IncludeGitContext:     &git,
		IncludePackageContext: &packages,
		ExtraInstructions:     "prefer citations",
	}); err != nil {
		t.Fatal(err)
	}
	prompt := session.HeadlessSystemPrompt()
	for _, want := range []string{"headless read-only agent", "CUSTOM.md", "custom instructions", "prefer citations"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unwanted := range []string{"headless coding agent", "Project context:", "go.mod"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("prompt contains %q:\n%s", unwanted, prompt)
		}
	}
}

func TestDomainConfigFromEnvAndRunHeadlessPreservesDefaults(t *testing.T) {
	t.Setenv("PIGO_SESSION_PURPOSE", "generic")
	t.Setenv("PIGO_CONTEXT_FILES", "LOCAL.md")
	t.Setenv("PIGO_INCLUDE_PACKAGE_CONTEXT", "false")

	root := t.TempDir()
	if err := WriteWorkspaceFile(root, "LOCAL.md", "local context"); err != nil {
		t.Fatal(err)
	}
	var capturedSystem string
	ai.RegisterProvider("domain-config-provider", providerFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		for _, message := range req.Messages {
			if message.Role == "system" {
				capturedSystem, _ = message.Content.(string)
				break
			}
		}
		return ai.NormalizedResult{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "ok",
			Content:    []any{map[string]any{"type": "text", "text": "ok"}},
		}, ai.AssistantEvents([]ai.ContentBlock{{Type: "text", Text: "ok"}}, "stop"), nil
	}))
	result, err := RunHeadlessSession(context.Background(), root, SessionInput{
		Provider: "domain-config-provider",
		ModelID:  "domain-config-model",
		Prompts:  []string{"hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}
	if !strings.Contains(capturedSystem, "headless agent operating") || !strings.Contains(capturedSystem, "local context") {
		t.Fatalf("system prompt did not use env domain config:\n%s", capturedSystem)
	}
}

func TestDomainConfigFromEnvRejectsInvalidContextFileNames(t *testing.T) {
	t.Setenv("PIGO_SESSION_PURPOSE", "research")
	t.Setenv("PIGO_CONTEXT_FILES", "../SECRET.md,nested/AGENTS.md")
	config := SessionDomainConfigFromEnv()
	if config.Purpose != SessionPurposeCoding {
		t.Fatalf("expected fallback purpose, got %q", config.Purpose)
	}
	for _, name := range config.ContextFiles {
		if strings.ContainsAny(name, `/\`) {
			t.Fatalf("unsafe context file name survived env parsing: %#v", config.ContextFiles)
		}
	}
}

func TestDomainConfigRejectsInvalidPurposeAndPathNames(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.SetDomainConfig(SessionDomainConfig{Purpose: "sales"}); err == nil {
		t.Fatal("expected invalid purpose error")
	}
	if err := session.SetDomainConfig(SessionDomainConfig{ContextFiles: []string{filepath.Join("nested", "AGENTS.md")}}); err == nil {
		t.Fatal("expected invalid context filename error")
	}
}

func TestRPCDomainConfig(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"d1","type":"set_domain_config","purpose":"research","contextFiles":["RESEARCH.md"],"includeGitContext":false,"includePackageContext":false,"extraInstructions":"cite sources"}` + "\n" +
			`{"id":"d2","type":"get_domain_config"}` + "\n" +
			`{"id":"d3","type":"set_domain_config","extraInstructions":""}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 3 || responses[0]["success"] != true || responses[1]["success"] != true || responses[2]["success"] != true {
		t.Fatalf("responses = %#v", responses)
	}
	config := responses[1]["data"].(map[string]any)
	if config["purpose"] != "research" || config["includeGitContext"] != false || config["includePackageContext"] != false || config["extraInstructions"] != "cite sources" {
		t.Fatalf("config = %#v", config)
	}
	if session.GetDomainConfig().ExtraInstructions != "" {
		t.Fatalf("extra instructions not cleared: %#v", session.GetDomainConfig())
	}
}
