package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/badlogic/pigo/pkg/a2a"
	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
	"github.com/badlogic/pigo/pkg/orchestrator"
)

func TestOrchestratorModuleRegistersToolsWhenEnabled(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.SetOrchestratorConfig(orchestrator.Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_, specs := session.ensureModuleRegistry().Tools()
	if !hasToolSpec(specs, "delegate_task") || !hasToolSpec(specs, "orchestrate_task") {
		t.Fatalf("missing orchestrator specs: %#v", specs)
	}
}

func TestOrchestratorRPCDelegatesToA2AAgent(t *testing.T) {
	remote := newCodingAgentA2ATestServer(t, "delegated result")
	defer remote.Close()

	session := NewSession(t.TempDir(), nil)
	if err := session.SetA2AConfig(a2a.Config{Enabled: true, Agents: []a2a.RemoteAgent{{
		Name:          "worker",
		URL:           remote.URL + "/a2a",
		CardURL:       remote.URL + "/.well-known/agent-card.json",
		AllowInsecure: true,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := session.SetOrchestratorConfig(orchestrator.Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	server := RPCServer{Session: session}
	var out bytes.Buffer
	command := rpcCommand{ID: "1", Type: "start_orchestration", Goal: "delegate", Steps: []orchestrator.TaskSpec{{Agent: "worker", Message: "work"}}}
	data, _ := json.Marshal(command)
	if err := server.Serve(context.Background(), bytes.NewReader(append(data, '\n')), &out); err != nil {
		t.Fatal(err)
	}
	var response rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success {
		t.Fatalf("rpc failed: %s", response.Error)
	}
	raw, _ := json.Marshal(response.Data)
	var run orchestrator.Run
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	if run.State != orchestrator.StateCompleted || run.Result == "" {
		t.Fatalf("unexpected run: %#v", run)
	}
	if len(session.CustomEntries(orchestratorCustomType)) == 0 {
		t.Fatal("expected orchestration snapshot entry")
	}
}

func TestOrchestratorToolAutoRoutesWhenAgentOmitted(t *testing.T) {
	remote := newCodingAgentA2ATestServer(t, "auto routed result")
	defer remote.Close()

	session := NewSession(t.TempDir(), nil)
	if err := session.SetA2AConfig(a2a.Config{Enabled: true, Agents: []a2a.RemoteAgent{{
		Name:          "worker",
		URL:           remote.URL + "/a2a",
		CardURL:       remote.URL + "/.well-known/agent-card.json",
		AllowInsecure: true,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := session.SetOrchestratorConfig(orchestrator.Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	tools := orchestratorTools(session)
	var orchestrate agentcoreTool
	for _, tool := range tools {
		if tool.Name == "orchestrate_task" {
			orchestrate = agentcoreTool{execute: tool.ExecuteWithUpdate}
			break
		}
	}
	if orchestrate.execute == nil {
		t.Fatal("missing orchestrate_task")
	}
	result := orchestrate.execute(context.Background(), ai.ContentBlock{
		Arguments: map[string]any{"goal": "route this without explicit agent"},
	}, nil)
	if result.IsError {
		t.Fatalf("orchestrate_task failed: %s", result.Text)
	}
	if !bytes.Contains([]byte(result.Text), []byte("auto routed result")) {
		t.Fatalf("unexpected result text: %q", result.Text)
	}
}

type agentcoreTool struct {
	execute func(context.Context, ai.ContentBlock, func(agentcore.ToolResult)) agentcore.ToolResult
}

func hasToolSpec(specs []ai.Tool, name string) bool {
	for _, spec := range specs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func newCodingAgentA2ATestServer(t *testing.T, responseText string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/agent-card.json":
			_ = json.NewEncoder(w).Encode(a2a.AgentCard{
				ProtocolVersion:    a2a.ProtocolVersion,
				Name:               "worker",
				URL:                "http://" + r.Host + "/a2a",
				Version:            "test",
				Capabilities:       a2a.AgentCapabilities{},
				DefaultInputModes:  []string{"text/plain"},
				DefaultOutputModes: []string{"text/plain"},
			})
		case "/a2a":
			var req a2a.JSONRPCRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			task := a2a.Task{
				Kind:      "task",
				ID:        a2a.NewID("task"),
				ContextID: a2a.NewID("ctx"),
				Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
				Artifacts: []a2a.Artifact{{ArtifactID: "out", Parts: []a2a.Part{{Kind: "text", Text: responseText}}}},
			}
			_ = json.NewEncoder(w).Encode(a2a.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: task})
		default:
			http.NotFound(w, r)
		}
	}))
}
