package a2a

import "testing"

func TestConfigNormalizesRemoteAgents(t *testing.T) {
	config := Config{
		Enabled: true,
		Agents: []RemoteAgent{{
			Name:          "Research Agent",
			URL:           "http://localhost:8080/a2a",
			BearerToken:   "secret",
			AllowInsecure: true,
		}},
	}.Normalized()
	if len(config.Agents) != 1 {
		t.Fatalf("expected agent")
	}
	agent := config.Agents[0]
	if agent.Name != "research_agent" {
		t.Fatalf("unexpected safe name %q", agent.Name)
	}
	if agent.CardURL != "http://localhost:8080/.well-known/agent-card.json" {
		t.Fatalf("unexpected card url %q", agent.CardURL)
	}
	if agent.Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("bearer token was not converted to authorization header")
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigRejectsInsecureNonLocalAgent(t *testing.T) {
	config := Config{Agents: []RemoteAgent{{Name: "remote", URL: "http://example.com/a2a"}}}.Normalized()
	if err := config.Validate(); err == nil {
		t.Fatal("expected insecure remote URL to be rejected")
	}
}
