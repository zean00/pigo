package a2aadapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/a2a"
	"github.com/badlogic/pigo/pkg/ai"
	"github.com/badlogic/pigo/pkg/codingagent"
)

func TestServerAgentCardAndMessageSend(t *testing.T) {
	server := httptest.NewServer(New(ServerOptions{
		Name: "test-agent",
		NewSession: func() *codingagent.Session {
			return codingagent.NewSession(t.TempDir(), []codingagent.AssistantTurn{{
				Content: []ai.ContentBlock{{Type: "text", Text: "hello from pigo"}},
			}})
		},
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var card a2a.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	if card.Name != "test-agent" || !card.Capabilities.Streaming {
		t.Fatalf("unexpected card: %#v", card)
	}

	task := sendMessage(t, server.URL+"/a2a", "say hello")
	if task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("unexpected task state %q", task.Status.State)
	}
	if text := a2a.TaskText(task); text != "hello from pigo" {
		t.Fatalf("unexpected task text %q", text)
	}
}

func TestServerMessageStreamEmitsTaskLifecycle(t *testing.T) {
	server := httptest.NewServer(New(ServerOptions{
		NewSession: func() *codingagent.Session {
			return codingagent.NewSession(t.TempDir(), []codingagent.AssistantTurn{{
				Content: []ai.ContentBlock{{Type: "text", Text: "streamed response"}},
			}})
		},
	}))
	defer server.Close()

	params := a2a.MessageSendParams{Message: a2a.NewTextMessage(a2a.RoleUser, "stream")}
	rawParams, _ := json.Marshal(params)
	rawRequest, _ := json.Marshal(a2a.JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "message/stream", Params: rawParams})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/a2a", bytes.NewReader(rawRequest))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("unexpected content type %q", got)
	}
	scanner := bufio.NewScanner(resp.Body)
	var sawTask, sawArtifact, sawFinal bool
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var response a2a.JSONRPCResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &response); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(response.Result)
		if strings.Contains(string(raw), `"kind":"task"`) {
			sawTask = true
		}
		if strings.Contains(string(raw), `"kind":"artifact-update"`) {
			sawArtifact = true
		}
		if strings.Contains(string(raw), `"final":true`) {
			sawFinal = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawTask || !sawArtifact || !sawFinal {
		t.Fatalf("missing stream events task=%v artifact=%v final=%v", sawTask, sawArtifact, sawFinal)
	}
}

func TestClientStreamMessageReturnsCompletedTask(t *testing.T) {
	server := httptest.NewServer(New(ServerOptions{
		NewSession: func() *codingagent.Session {
			return codingagent.NewSession(t.TempDir(), []codingagent.AssistantTurn{{
				Content: []ai.ContentBlock{{Type: "text", Text: "remote answer"}},
			}})
		},
	}))
	defer server.Close()

	client := a2a.NewClient(a2a.RemoteAgent{
		Name:          "remote",
		URL:           server.URL + "/a2a",
		CardURL:       server.URL + "/.well-known/agent-card.json",
		AllowInsecure: true,
	}, 0, 0)
	task, err := client.StreamMessage(context.Background(), a2a.MessageSendParams{
		Message: a2a.NewTextMessage(a2a.RoleUser, "delegate"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("unexpected task state %q", task.Status.State)
	}
	if text := a2a.TaskText(task); text != "remote answer" {
		t.Fatalf("unexpected task text %q", text)
	}
}

func sendMessage(t *testing.T, endpoint, text string) a2a.Task {
	t.Helper()
	params := a2a.MessageSendParams{Message: a2a.NewTextMessage(a2a.RoleUser, text)}
	rawParams, _ := json.Marshal(params)
	rawRequest, _ := json.Marshal(a2a.JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "message/send", Params: rawParams})
	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(rawRequest))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var response a2a.JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil {
		t.Fatalf("unexpected rpc error: %#v", response.Error)
	}
	rawTask, _ := json.Marshal(response.Result)
	var task a2a.Task
	if err := json.Unmarshal(rawTask, &task); err != nil {
		t.Fatal(err)
	}
	return task
}
