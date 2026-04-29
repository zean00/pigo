package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestSessionProviderLoopWritesFileThroughTool(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		callCount++
		var payload ai.CompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Model != "test-model" {
			t.Fatalf("model = %q", payload.Model)
		}
		if callCount == 1 {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{
				"choices": [{
					"message": {
						"role": "assistant",
						"tool_calls": [{
							"id":"tool-write-1",
							"type":"function",
							"function": {
								"name": "write",
								"arguments": "{\"path\":\"notes.txt\",\"content\":\"hello\"}"
							}
						}]
					},
					"finish_reason": "tool_calls"
				}]
			}`)
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{
				"choices": [{
					"message": {
						"role": "assistant",
						"content": "wrote file"
					},
					"finish_reason": "stop"
				}]
		}`)
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	root := t.TempDir()
	session := NewSession(root, nil)
	if _, err := session.SetModel("openai", "test-model"); err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "make notes"); err != nil {
		t.Fatal(err)
	}

	if callCount != 2 {
		t.Fatalf("openai calls = %d", callCount)
	}

	content, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(content)) != "hello" {
		t.Fatalf("notes content = %q", string(content))
	}

	if len(session.Messages) != 4 {
		t.Fatalf("message count = %d", len(session.Messages))
	}
}
