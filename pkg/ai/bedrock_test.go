package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	bedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockdocument "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

type fakeBedrockClient struct {
	converse func(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

func (client fakeBedrockClient) Converse(ctx context.Context, input *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	return client.converse(ctx, input, optFns...)
}

func TestBedrockProviderReturnsTextAndToolCalls(t *testing.T) {
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")

	provider := &bedrockProvider{
		newClient: func(context.Context, bedrockClientOptions) (bedrockConverseClient, error) {
			return fakeBedrockClient{
				converse: func(_ context.Context, input *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
					if got := aws.ToString(input.ModelId); got != "us.anthropic.claude-opus-4-6-v1" {
						t.Fatalf("modelId = %q", got)
					}
					if input.ToolConfig == nil || len(input.ToolConfig.Tools) != 1 {
						t.Fatalf("toolConfig = %#v", input.ToolConfig)
					}
					if _, ok := input.ToolConfig.ToolChoice.(*bedrocktypes.ToolChoiceMemberAny); !ok {
						t.Fatalf("toolChoice = %#v", input.ToolConfig.ToolChoice)
					}
					if len(input.Messages) != 2 {
						t.Fatalf("messages len = %d", len(input.Messages))
					}

					userMessage := input.Messages[0]
					if userMessage.Role != bedrocktypes.ConversationRoleUser {
						t.Fatalf("user role = %q", userMessage.Role)
					}
					if len(userMessage.Content) != 2 {
						t.Fatalf("user content len = %d", len(userMessage.Content))
					}
					if text, ok := userMessage.Content[0].(*bedrocktypes.ContentBlockMemberText); !ok || text.Value != "calculate" {
						t.Fatalf("user text block = %#v", userMessage.Content[0])
					}
					if image, ok := userMessage.Content[1].(*bedrocktypes.ContentBlockMemberImage); !ok || image.Value.Format != bedrocktypes.ImageFormatPng {
						t.Fatalf("user image block = %#v", userMessage.Content[1])
					}

					toolMessage := input.Messages[1]
					if toolMessage.Role != bedrocktypes.ConversationRoleUser {
						t.Fatalf("tool role = %q", toolMessage.Role)
					}
					resultBlock, ok := toolMessage.Content[0].(*bedrocktypes.ContentBlockMemberToolResult)
					if !ok {
						t.Fatalf("tool result block = %#v", toolMessage.Content[0])
					}
					if got := aws.ToString(resultBlock.Value.ToolUseId); got != "tc-prev" {
						t.Fatalf("toolUseId = %q", got)
					}
					if resultBlock.Value.Status != bedrocktypes.ToolResultStatusSuccess {
						t.Fatalf("tool result status = %q", resultBlock.Value.Status)
					}

					return &bedrockruntime.ConverseOutput{
						Output: &bedrocktypes.ConverseOutputMemberMessage{
							Value: bedrocktypes.Message{
								Role: bedrocktypes.ConversationRoleAssistant,
								Content: []bedrocktypes.ContentBlock{
									&bedrocktypes.ContentBlockMemberText{Value: "I can do that."},
									&bedrocktypes.ContentBlockMemberToolUse{
										Value: bedrocktypes.ToolUseBlock{
											ToolUseId: aws.String("tc-1"),
											Name:      aws.String("math"),
											Input:     bedrockdocument.NewLazyDocument(map[string]any{"a": float64(15), "b": float64(27)}),
										},
									},
								},
							},
						},
						StopReason: bedrocktypes.StopReasonToolUse,
						Usage: &bedrocktypes.TokenUsage{
							InputTokens:  aws.Int32(5),
							OutputTokens: aws.Int32(20),
							TotalTokens:  aws.Int32(25),
						},
					}, nil
				},
			}, nil
		},
	}

	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "amazon-bedrock",
		Model:    "us.anthropic.claude-opus-4-6-v1",
		Tools: []Tool{{
			Name:        "math",
			Description: "add numbers",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "number"},
					"b": map[string]any{"type": "number"},
				},
			},
		}},
		Options: ChatOptions{
			APIKey:     "bedrock-token",
			ToolChoice: "any",
		},
		Messages: []Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "calculate"},
					map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString([]byte("png"))},
				},
			},
			{Role: "toolResult", ToolCallID: "tc-prev", Content: "15"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if result.Text != "I can do that." {
		t.Fatalf("text = %q", result.Text)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content len = %d", len(result.Content))
	}
	if result.Usage == nil || result.Usage.TotalTokens != 25 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if len(events) == 0 || events[0].Type != "start" || events[len(events)-1].Type != "done" {
		t.Fatalf("events = %#v", events)
	}
}

func TestBedrockProviderReturnsMissingCredentialsError(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "")

	provider := BedrockProvider()
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "amazon-bedrock",
		Model:    "us.anthropic.claude-opus-4-6-v1",
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "missing API key for provider: amazon-bedrock" {
		t.Fatalf("error = %q", got)
	}
}

func TestBedrockProviderMapsThinkingBlocksAndCanceledErrors(t *testing.T) {
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")

	provider := &bedrockProvider{
		newClient: func(context.Context, bedrockClientOptions) (bedrockConverseClient, error) {
			return fakeBedrockClient{
				converse: func(ctx context.Context, input *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
					if input == nil {
						t.Fatal("missing input")
					}
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					return &bedrockruntime.ConverseOutput{
						Output: &bedrocktypes.ConverseOutputMemberMessage{
							Value: bedrocktypes.Message{
								Role: bedrocktypes.ConversationRoleAssistant,
								Content: []bedrocktypes.ContentBlock{
									&bedrocktypes.ContentBlockMemberReasoningContent{
										Value: &bedrocktypes.ReasoningContentBlockMemberReasoningText{
											Value: bedrocktypes.ReasoningTextBlock{Text: aws.String("step 1")},
										},
									},
									&bedrocktypes.ContentBlockMemberReasoningContent{
										Value: &bedrocktypes.ReasoningContentBlockMemberRedactedContent{Value: []byte("opaque")},
									},
									&bedrocktypes.ContentBlockMemberText{Value: "done"},
								},
							},
						},
						StopReason: bedrocktypes.StopReasonEndTurn,
						Usage: &bedrocktypes.TokenUsage{
							InputTokens:  aws.Int32(1),
							OutputTokens: aws.Int32(2),
							TotalTokens:  aws.Int32(3),
						},
					}, nil
				},
			}, nil
		},
	}

	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "amazon-bedrock",
		Model:    "us.anthropic.claude-opus-4-6-v1",
		Options:  ChatOptions{APIKey: "bedrock-token"},
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "stop" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if len(result.Content) != 3 {
		t.Fatalf("content len = %d", len(result.Content))
	}
	first, ok := result.Content[0].(map[string]any)
	if !ok || first["type"] != "thinking" || first["thinking"] != "step 1" {
		t.Fatalf("thinking block = %#v", result.Content[0])
	}
	second, ok := result.Content[1].(map[string]any)
	if !ok || second["redacted"] != true {
		t.Fatalf("redacted block = %#v", result.Content[1])
	}

	canceledProvider := &bedrockProvider{
		newClient: func(context.Context, bedrockClientOptions) (bedrockConverseClient, error) {
			return fakeBedrockClient{
				converse: func(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
					return nil, context.Canceled
				},
			}, nil
		},
	}
	_, _, err = canceledProvider.Complete(context.Background(), CompletionRequest{
		Provider: "amazon-bedrock",
		Model:    "us.anthropic.claude-opus-4-6-v1",
		Options:  ChatOptions{APIKey: "bedrock-token"},
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
