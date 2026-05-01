package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestCompressCommandOutputGoTestPreservesFailureLines(t *testing.T) {
	output := strings.Join([]string{
		"?   	example/skip	[no test files]",
		"--- FAIL: TestThing (0.00s)",
		"    thing_test.go:12: expected ok",
		strings.Repeat("noise\n", 150),
		"FAIL",
	}, "\n")
	result := CompressCommandOutput("go test ./...", output, 1, CommandOutputCompressionConfig{Mode: CommandCompressionForce, MaxBytes: 1200})
	if !result.Compressed {
		t.Fatal("expected compressed output")
	}
	if result.Filter != "go-test" {
		t.Fatalf("filter = %q", result.Filter)
	}
	if !strings.Contains(result.Output, "--- FAIL: TestThing") || !strings.Contains(result.Output, "thing_test.go:12") {
		t.Fatalf("output did not preserve failure context:\n%s", result.Output)
	}
}

func TestCompressCommandOutputCanDisableSpecificFilter(t *testing.T) {
	output := strings.Repeat("line\n", 300)
	result := CompressCommandOutput("go test ./...", output, 1, CommandOutputCompressionConfig{
		Mode:            CommandCompressionForce,
		MaxBytes:        300,
		DisabledFilters: []string{"go-test"},
	})
	if result.Filter != "generic" {
		t.Fatalf("filter = %q", result.Filter)
	}
}

func TestBuiltinBashToolAddsCompressionMetadata(t *testing.T) {
	tools := BuiltinToolsWithOptions(t.TempDir(), BuiltinToolOptions{
		OutputLimit: 400,
		CommandCompression: CommandOutputCompressionConfig{
			Mode:     CommandCompressionForce,
			MaxBytes: 200,
		},
	})
	var bashToolFound bool
	for _, tool := range tools {
		if tool.Name != "bash" {
			continue
		}
		bashToolFound = true
		result := tool.Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{
			"command": "for i in $(seq 1 200); do echo line-$i; done",
		}})
		if result.IsError {
			t.Fatalf("result = %#v", result)
		}
		if result.Details["compressed"] != true {
			t.Fatalf("details = %#v", result.Details)
		}
		if result.Details["compressionFilter"] == "" {
			t.Fatalf("details = %#v", result.Details)
		}
	}
	if !bashToolFound {
		t.Fatal("missing bash tool")
	}
}

func TestSessionBashCompressionCanBeDisabled(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.SetCommandCompression(CommandOutputCompressionConfig{Mode: CommandCompressionOff, MaxBytes: 1000}); err != nil {
		t.Fatal(err)
	}
	result, err := session.Bash(context.Background(), "printf short")
	if err != nil {
		t.Fatal(err)
	}
	if result.Compression["compressed"] == true {
		t.Fatalf("compression = %#v", result.Compression)
	}
	if result.Output != "short" {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestRunHeadlessSessionPreservesEnvCompressionDefault(t *testing.T) {
	t.Setenv("PIGO_COMMAND_COMPRESSION", "off")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := RunHeadlessSession(context.Background(), root, SessionInput{
		Prompts: []string{"run bash"},
		Turns: []AssistantTurn{{
			StopReason: "toolUse",
			Content: []ai.ContentBlock{{
				Type: "toolCall",
				ID:   "bash-1",
				Name: "bash",
				Arguments: map[string]any{
					"command": "printf output",
				},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range result.Messages {
		if message["role"] != "toolResult" {
			continue
		}
		details, _ := message["details"].(map[string]any)
		if details["compressionMode"] != CommandCompressionOff {
			t.Fatalf("details = %#v", details)
		}
		return
	}
	t.Fatalf("missing tool result message: %#v", result.Messages)
}
