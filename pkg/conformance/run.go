package conformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

func RunAI(testCase AICase) (AIOutput, error) {
	result, events := ai.NormalizeResponse(aiFixtureResponse(testCase))
	return AIOutput{
		Case:           testCase.Name,
		Implementation: Implementation{Name: "pigo-ai", Version: "0.1.0"},
		Model:          testCase.Model,
		Events:         events,
		Result:         result,
	}, nil
}

func aiFixtureResponse(testCase AICase) ai.ScriptedResponse {
	switch testCase.Name {
	case "tool_call":
		return ai.NewToolResponse("call_1", "math_operation", map[string]any{
			"a":         float64(15),
			"b":         float64(27),
			"operation": "add",
		})
	case "empty_message":
		return ai.NewErrorResponse()
	case "thinking":
		return ai.ScriptedResponse{
			Content: []ai.ContentBlock{
				{Type: "thinking", Thinking: "brief thought"},
				{Type: "text", Text: "final answer"},
			},
			StopReason: "stop",
		}
	case "image_content":
		return ai.ScriptedResponse{
			Content:    []ai.ContentBlock{{Type: "image", Data: "iVBORw0KGgo=", MimeType: "image/png"}},
			StopReason: "stop",
		}
	default:
		return ai.NewTextResponse("Hello conformance")
	}
}

func RunAgent(testCase AgentCase) (AgentOutput, error) {
	input := agentcore.ScriptedLoopInput{
		Prompts: make([]string, 0, len(testCase.Prompts)),
		Tools:   make([]agentcore.Tool, 0, len(testCase.Context.Tools)),
		Turns:   make([]agentcore.AssistantTurn, 0, len(testCase.AssistantTurns)),
	}
	for _, prompt := range testCase.Prompts {
		input.Prompts = append(input.Prompts, MessageText(prompt))
	}
	for _, tool := range testCase.Context.Tools {
		tool := tool
		input.Tools = append(input.Tools, agentcore.Tool{
			Name: tool.Name,
			Execute: func(_ context.Context, _ ai.ContentBlock) agentcore.ToolResult {
				return agentcore.ToolResult{Text: tool.Result.Text, Details: tool.Result.Details}
			},
		})
	}
	for _, turn := range testCase.AssistantTurns {
		blocks, err := ParseTurnContent(turn.Content)
		if err != nil {
			return AgentOutput{}, err
		}
		input.Turns = append(input.Turns, agentcore.AssistantTurn{Content: blocks, StopReason: turn.StopReason})
	}
	loop, err := agentcore.RunScriptedLoop(context.Background(), input)
	if err != nil {
		return AgentOutput{}, err
	}
	return AgentOutput{
		Case:           testCase.Name,
		Implementation: Implementation{Name: "pigo-agentcore", Version: "0.1.0"},
		Model:          testCase.Model,
		Events:         eventsToAny(loop.Events),
		Messages:       messagesToAny(loop.Messages),
	}, nil
}

func RunCodingAgent(testCase CodingAgentCase) (CodingAgentOutput, error) {
	dir, err := os.MkdirTemp("", "pigo-coding-agent-*")
	if err != nil {
		return CodingAgentOutput{}, err
	}
	defer os.RemoveAll(dir)

	session, err := NewCodingAgentSessionFromCase(dir, testCase)
	if err != nil {
		return CodingAgentOutput{}, err
	}
	for _, prompt := range testCase.Prompts {
		if err := session.Prompt(context.Background(), prompt); err != nil {
			return CodingAgentOutput{}, err
		}
	}
	files := map[string]string{}
	for _, name := range expectedFileNames(testCase) {
		path, err := sessionFilePath(dir, name)
		if err != nil {
			return CodingAgentOutput{}, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				files[name] = ""
				continue
			}
			return CodingAgentOutput{}, err
		}
		files[name] = string(data)
	}

	return CodingAgentOutput{
		Case:              testCase.Name,
		Implementation:    Implementation{Name: "pigo-codingagent", Version: "0.1.0"},
		Model:             testCase.Model,
		Events:            eventsToAny(session.Events),
		Messages:          messagesToAny(session.Messages),
		SessionEntryTypes: session.SessionEntryTypes,
		Files:             files,
	}, nil
}

func expectedFileNames(testCase CodingAgentCase) []string {
	names := make([]string, 0, len(testCase.Expect.Files))
	for name := range testCase.Expect.Files {
		names = append(names, name)
	}
	return names
}

func sessionFilePath(root, name string) (string, error) {
	if name == "" {
		return "", os.ErrPermission
	}
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) {
		return "", os.ErrPermission
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(rootAbs, clean)
	relative, err := filepath.Rel(rootAbs, path)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, fmt.Sprintf("..%s", string(os.PathSeparator))) || relative == "." {
		return "", os.ErrPermission
	}
	return path, nil
}

func firstAssistantUpdate(blocks []ai.ContentBlock) string {
	if len(blocks) == 0 {
		return "done"
	}
	switch blocks[0].Type {
	case "toolCall":
		return "toolcall_start"
	case "thinking":
		return "thinking_start"
	default:
		return "text_start"
	}
}

func defaultStopReason(reason string) string {
	if reason == "" {
		return "stop"
	}
	return reason
}

func eventsToAny(events []agentcore.Event) []any {
	out := make([]any, 0, len(events))
	for _, event := range events {
		out = append(out, map[string]any(event))
	}
	return out
}

func messagesToAny(messages []agentcore.Message) []any {
	out := make([]any, 0, len(messages))
	for _, message := range messages {
		out = append(out, map[string]any(message))
	}
	return out
}
