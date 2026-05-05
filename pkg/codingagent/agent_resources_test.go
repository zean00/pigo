package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestLoadAgentProfilesTeamsAndChains(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent-home")
	if err := os.MkdirAll(filepath.Join(root, ".pi", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	userProfile := "---\nname: reviewer\ndescription: Review code\nprovider: test-provider\nmodel: test-model\nthinkingLevel: high\ntools: read,grep\n---\nReview changes carefully."
	if err := os.WriteFile(filepath.Join(agentDir, "agents", "reviewer.md"), []byte(userProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	projectProfile := "---\nname: planner\ndescription: Plan work\n---\nMake a concise plan."
	if err := os.WriteFile(filepath.Join(root, ".pi", "agents", "planner.md"), []byte(projectProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	teams := "teams:\n  - name: delivery\n    description: Delivery team\n    agents: [planner, reviewer]\n"
	if err := os.WriteFile(filepath.Join(root, ".pi", "agents", "teams.yaml"), []byte(teams), 0o644); err != nil {
		t.Fatal(err)
	}
	chains := "chains:\n  - name: review-chain\n    description: Review chain\n    steps:\n      - name: plan\n        agent: planner\n        prompt: Plan this\n      - name: review\n        agent: reviewer\n        prompt: Review this\n"
	if err := os.WriteFile(filepath.Join(root, ".pi", "agents", "chains.yaml"), []byte(chains), 0o644); err != nil {
		t.Fatal(err)
	}

	session := NewSession(root, nil)
	session.LoadSlashCommandResources(ResourceLoadOptions{AgentDir: agentDir, IncludeDefaults: true})

	profiles := session.AgentProfiles()
	if len(profiles) != 2 {
		t.Fatalf("profiles = %#v", profiles)
	}
	if profiles[0].Name != "reviewer" || profiles[0].ModelID != "test-model" || profiles[0].ThinkingLevel != "high" || len(profiles[0].Tools) != 2 {
		t.Fatalf("reviewer profile = %#v", profiles[0])
	}
	if teams := session.AgentTeams(); len(teams) != 1 || teams[0].Name != "delivery" || len(teams[0].Agents) != 2 {
		t.Fatalf("teams = %#v", teams)
	}
	if chains := session.AgentChains(); len(chains) != 1 || chains[0].Name != "review-chain" || len(chains[0].Steps) != 2 {
		t.Fatalf("chains = %#v", chains)
	}
}

func TestActiveAgentProfileAugmentsSystemPromptAndSelection(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pi", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := "---\nname: reviewer\nprovider: profile-provider\nmodel: profile-model\nthinkingLevel: medium\n---\nUse the reviewer profile instructions."
	if err := os.WriteFile(filepath.Join(root, ".pi", "agents", "reviewer.md"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession(root, nil)
	session.LoadSlashCommandResources(ResourceLoadOptions{IncludeDefaults: true})
	if err := session.SetActiveAgentProfile("reviewer"); err != nil {
		t.Fatalf("set active profile: %v", err)
	}
	if session.ActiveAgentProfile() != "reviewer" || session.Provider != "profile-provider" || session.ModelID != "profile-model" || session.ThinkingLevel != "medium" {
		t.Fatalf("profile selection failed: active=%q provider=%q model=%q level=%q", session.ActiveAgentProfile(), session.Provider, session.ModelID, session.ThinkingLevel)
	}
	if prompt := session.HeadlessSystemPrompt(); !strings.Contains(prompt, "Use the reviewer profile instructions.") {
		t.Fatalf("system prompt missing profile instructions:\n%s", prompt)
	}
	if err := session.SetActiveAgentProfile("default"); err != nil {
		t.Fatalf("clear active profile: %v", err)
	}
	if session.ActiveAgentProfile() != "" {
		t.Fatalf("active profile not cleared: %q", session.ActiveAgentProfile())
	}
}

func TestRPCAgentProfileCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pi", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".pi", "agents", "planner.md"), []byte("---\nname: planner\n---\nPlan work."), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession(root, nil)
	session.LoadSlashCommandResources(ResourceLoadOptions{IncludeDefaults: true})
	server := RPCServer{Session: session}
	set := server.handle(context.Background(), rpcCommand{ID: "set", Type: "set_agent_profile", Name: "planner"})
	if !set.Success {
		t.Fatalf("set profile rpc failed: %s", set.Error)
	}
	get := server.handle(context.Background(), rpcCommand{ID: "get", Type: "get_agent_profiles"})
	if !get.Success {
		t.Fatalf("get profiles rpc failed: %s", get.Error)
	}
	data, ok := get.Data.(map[string]any)
	if !ok || data["active"] != "planner" {
		t.Fatalf("unexpected get profiles data: %#v", get.Data)
	}
}

func TestToolSearchIsOptInAndReadOnly(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if containsString(specNames(session.toolSpecs()), "tool_search") {
		t.Fatal("tool_search exposed by default")
	}
	session.SetToolSearchEnabled(true)
	var toolSearch ai.Tool
	for _, spec := range session.toolSpecs() {
		if spec.Name == "tool_search" {
			toolSearch = spec
			break
		}
	}
	if toolSearch.Name == "" {
		t.Fatal("tool_search not exposed when enabled")
	}
	for _, tool := range session.builtinTools() {
		if tool.Name != "tool_search" {
			continue
		}
		result := tool.Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"query": "grep"}})
		if result.IsError || !strings.Contains(result.Text, "tools matched") {
			t.Fatalf("tool_search result = %#v", result)
		}
		return
	}
	t.Fatal("tool_search executor not found")
}
