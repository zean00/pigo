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
	turns := make([]agentcore.AssistantTurn, 0, len(testCase.AssistantTurns))
	tools := make([]agentcore.Tool, 0, len(testCase.Context.Tools))
	toolSpecs := make([]ai.Tool, 0, len(testCase.Context.Tools))
	for _, tool := range testCase.Context.Tools {
		tool := tool
		toolSpecs = append(toolSpecs, tool.Tool)
		tools = append(tools, agentcore.Tool{
			Name: tool.Name,
			ExecuteWithUpdate: func(_ context.Context, call ai.ContentBlock, onUpdate func(agentcore.ToolResult)) agentcore.ToolResult {
				if onUpdate != nil {
					onUpdate(agentcore.ToolResult{
						Text:    fmt.Sprintf("started:%s", call.ID),
						Details: map[string]any{"toolCallId": call.ID},
					})
				}
				return agentcore.ToolResult{
					Text:      tool.Result.Text,
					Details:   tool.Result.Details,
					Terminate: tool.Result.Terminate,
				}
			},
		})
	}
	for _, turn := range testCase.AssistantTurns {
		blocks, err := ParseTurnContent(turn.Content)
		if err != nil {
			return AgentOutput{}, err
		}
		turns = append(turns, agentcore.AssistantTurn{Content: blocks, StopReason: defaultStopReason(turn.StopReason)})
	}
	history := append([]ai.Message(nil), testCase.Context.Messages...)
	if strings.TrimSpace(testCase.Context.SystemPrompt) != "" {
		history = append([]ai.Message{{Role: "system", Content: testCase.Context.SystemPrompt}}, history...)
	}
	turnIndex := 0
	events := []agentcore.Event{}
	loop, err := agentcore.RunProviderLoop(context.Background(), agentcore.ProviderLoopInput{
		PromptMessages: testCase.Prompts,
		Tools:          tools,
		History:        history,
		Provider:       testCase.Model.Provider,
		Model:          testCase.Model.ID,
		ToolSpecs:      toolSpecs,
		ToolExecution:  agentcore.ToolExecutionMode(testCase.Options.ToolExecution),
		Options:        ai.ChatOptions{Stream: true},
		EventSink: func(event agentcore.Event) {
			events = append(events, event)
		},
		StreamFn: func(ctx context.Context, req ai.CompletionRequest) *ai.EventStream {
			stream := ai.CreateEventStream()
			go func() {
				if turnIndex >= len(turns) {
					err := fmt.Errorf("missing scripted assistant turn %d", turnIndex)
					stream.Close(ai.NormalizedResult{Role: "assistant", StopReason: "error", ErrorMessage: err.Error()}, err)
					return
				}
				turn := turns[turnIndex]
				turnIndex++
				result := ai.NormalizedResult{
					Role:       "assistant",
					Provider:   req.Provider,
					Model:      req.Model,
					StopReason: defaultStopReason(turn.StopReason),
					Text:       ai.ContentText(turn.Content),
					Content:    ai.NormalizedContent(turn.Content),
					Usage:      &ai.Usage{Input: 1, Output: 1, TotalTokens: 2},
				}
				for _, event := range ai.AttachEventPayloads(ai.AssistantEvents(turn.Content, result.StopReason), result) {
					select {
					case <-ctx.Done():
						stream.Close(result, ctx.Err())
						return
					default:
						stream.Push(event)
					}
				}
				stream.Close(result, nil)
			}()
			return stream
		},
	})
	if err != nil {
		return AgentOutput{}, err
	}
	return AgentOutput{
		Case:           testCase.Name,
		Implementation: Implementation{Name: "pigo-agentcore", Version: "0.1.0"},
		Model:          testCase.Model,
		Events:         eventsToAny(events),
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
