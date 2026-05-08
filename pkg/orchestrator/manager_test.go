package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/badlogic/pigo/pkg/a2a"
)

func TestManagerRunsDependentTasksAndReducesArtifacts(t *testing.T) {
	server := newA2ATestServer(t, map[string]string{
		"one": "first result",
		"two": "second result",
	})
	defer server.Close()

	manager := NewManager(Config{Enabled: true, MaxParallel: 2}, a2a.Config{
		Enabled: true,
		Agents:  []a2a.RemoteAgent{{Name: "worker", URL: server.URL + "/a2a", CardURL: server.URL + "/.well-known/agent-card.json", AllowInsecure: true}},
	}, []Agent{{Name: "worker", Tags: []string{"build"}}}, nil)

	run, err := manager.Start(context.Background(), RunRequest{
		Goal: "build feature",
		Steps: []TaskSpec{
			{ID: "one", Agent: "worker", Message: "one"},
			{ID: "two", Agent: "worker", Message: "two", DependsOn: []string{"one"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != StateCompleted {
		t.Fatalf("state = %s", run.State)
	}
	if len(run.Artifacts) != 2 {
		t.Fatalf("artifacts = %#v", run.Artifacts)
	}
	if run.Result == "" || !contains(run.Result, "first result") || !contains(run.Result, "second result") {
		t.Fatalf("unexpected result: %q", run.Result)
	}
}

func TestManagerFailsWhenNoAgentCanRoute(t *testing.T) {
	manager := NewManager(Config{Enabled: true}, a2a.Config{}, nil, nil)
	run, err := manager.Start(context.Background(), RunRequest{Goal: "do work"})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != StateFailed {
		t.Fatalf("state = %s", run.State)
	}
}

func newA2ATestServer(t *testing.T, responses map[string]string) *httptest.Server {
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
			var params a2a.MessageSendParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Fatal(err)
			}
			text := a2a.TextFromParts(params.Message.Parts)
			reply := responses[text]
			task := a2a.Task{
				Kind:      "task",
				ID:        a2a.NewID("task"),
				ContextID: a2a.NewID("ctx"),
				Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
				Artifacts: []a2a.Artifact{{ArtifactID: "out", Parts: []a2a.Part{{Kind: "text", Text: reply}}}},
			}
			_ = json.NewEncoder(w).Encode(a2a.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: task})
		default:
			http.NotFound(w, r)
		}
	}))
}

func contains(value, needle string) bool {
	return bytes.Contains([]byte(value), []byte(needle))
}
