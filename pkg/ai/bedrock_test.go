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
	"github.com/aws/smithy-go/middleware"
)

type fakeBedrockClient struct {
	converse       func(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	converseStream func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

func (client fakeBedrockClient) Converse(ctx context.Context, input *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	return client.converse(ctx, input, optFns...)
}

func (client fakeBedrockClient) ConverseStream(ctx context.Context, input *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	return client.converseStream(ctx, input, optFns...)
}

type fakeBedrockStreamReader struct {
	events chan bedrocktypes.ConverseStreamOutput
	err    error
}

func (reader *fakeBedrockStreamReader) Events() <-chan bedrocktypes.ConverseStreamOutput {
	return reader.events
}
func (reader *fakeBedrockStreamReader) Close() error { return nil }
func (reader *fakeBedrockStreamReader) Err() error   { return reader.err }

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
			APIKey:         "bedrock-token",
			ToolChoice:     "any",
			CacheRetention: CacheRetentionNone,
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
		Options: ChatOptions{
			APIKey:         "bedrock-token",
			CacheRetention: CacheRetentionNone,
		},
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestBedrockProviderStreamingResponse(t *testing.T) {
	events := make(chan bedrocktypes.ConverseStreamOutput, 4)
	events <- &bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: bedrocktypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &bedrocktypes.ContentBlockDeltaMemberText{Value: "hello"},
		},
	}
	events <- &bedrocktypes.ConverseStreamOutputMemberMessageStop{
		Value: bedrocktypes.MessageStopEvent{StopReason: bedrocktypes.StopReasonEndTurn},
	}
	events <- &bedrocktypes.ConverseStreamOutputMemberMetadata{
		Value: bedrocktypes.ConverseStreamMetadataEvent{
			Usage: &bedrocktypes.TokenUsage{
				InputTokens:  aws.Int32(1),
				OutputTokens: aws.Int32(1),
				TotalTokens:  aws.Int32(2),
			},
		},
	}
	close(events)

	stream := bedrockruntime.NewConverseStreamEventStream(func(stream *bedrockruntime.ConverseStreamEventStream) {
		stream.Reader = &fakeBedrockStreamReader{events: events}
	})

	result, _, err := bedrockEventStreamToResult(stream)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.StopReason != "stop" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
}

func TestBedrockProviderAddsCachePointsAndHooks(t *testing.T) {
	payloadHookCalled := false
	responseHookCalled := false

	provider := &bedrockProvider{
		newClient: func(context.Context, bedrockClientOptions) (bedrockConverseClient, error) {
			return fakeBedrockClient{
				converse: func(_ context.Context, input *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
					if input == nil {
						t.Fatal("missing input")
					}
					if input.RequestMetadata["user"] != "alice" || input.RequestMetadata["hooked"] != "yes" {
						t.Fatalf("request metadata = %#v", input.RequestMetadata)
					}
					if len(input.System) != 2 {
						t.Fatalf("system len = %d", len(input.System))
					}
					if _, ok := input.System[1].(*bedrocktypes.SystemContentBlockMemberCachePoint); !ok {
						t.Fatalf("system cache point = %#v", input.System[1])
					}
					if len(input.Messages) != 1 || len(input.Messages[0].Content) != 2 {
						t.Fatalf("messages = %#v", input.Messages)
					}
					if _, ok := input.Messages[0].Content[1].(*bedrocktypes.ContentBlockMemberCachePoint); !ok {
						t.Fatalf("message cache point = %#v", input.Messages[0].Content[1])
					}
					if input.ToolConfig == nil || len(input.ToolConfig.Tools) != 2 {
						t.Fatalf("tool config = %#v", input.ToolConfig)
					}
					if _, ok := input.ToolConfig.Tools[1].(*bedrocktypes.ToolMemberCachePoint); !ok {
						t.Fatalf("tool cache point = %#v", input.ToolConfig.Tools[1])
					}

					return &bedrockruntime.ConverseOutput{
						Output: &bedrocktypes.ConverseOutputMemberMessage{
							Value: bedrocktypes.Message{
								Role: bedrocktypes.ConversationRoleAssistant,
								Content: []bedrocktypes.ContentBlock{
									&bedrocktypes.ContentBlockMemberText{Value: "ok"},
								},
							},
						},
						StopReason:     bedrocktypes.StopReasonEndTurn,
						Usage:          &bedrocktypes.TokenUsage{InputTokens: aws.Int32(1), OutputTokens: aws.Int32(1), TotalTokens: aws.Int32(2)},
						ResultMetadata: middleware.Metadata{},
					}, nil
				},
			}, nil
		},
	}

	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "amazon-bedrock",
		Model:    "us.anthropic.claude-opus-4-6-v1",
		Tools:    []Tool{{Name: "math", Parameters: map[string]any{"type": "object"}}},
		Options: ChatOptions{
			APIKey:         "bedrock-token",
			CacheRetention: CacheRetentionLong,
			Metadata:       map[string]any{"user": "alice"},
			OnPayload: func(payload any, req CompletionRequest) (any, error) {
				payloadHookCalled = true
				input, ok := payload.(*bedrockruntime.ConverseInput)
				if !ok {
					t.Fatalf("payload type = %T", payload)
				}
				if input.RequestMetadata == nil {
					input.RequestMetadata = map[string]string{}
				}
				input.RequestMetadata["hooked"] = "yes"
				return input, nil
			},
			OnResponse: func(response ProviderResponse, req CompletionRequest) error {
				responseHookCalled = true
				if response.Status != 200 {
					t.Fatalf("status = %d", response.Status)
				}
				return nil
			},
		},
		Messages: []Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !payloadHookCalled || !responseHookCalled {
		t.Fatalf("payloadHookCalled=%v responseHookCalled=%v", payloadHookCalled, responseHookCalled)
	}
	if result.Text != "ok" {
		t.Fatalf("text = %q", result.Text)
	}
}

func TestBedrockProviderRetriesThrottlingErrors(t *testing.T) {
	attempts := 0
	provider := &bedrockProvider{
		newClient: func(context.Context, bedrockClientOptions) (bedrockConverseClient, error) {
			return fakeBedrockClient{
				converse: func(_ context.Context, input *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
					attempts++
					if input == nil {
						t.Fatal("missing input")
					}
					if attempts == 1 {
						return nil, &bedrocktypes.ThrottlingException{Message: aws.String("Please retry in 1ms")}
					}
					return &bedrockruntime.ConverseOutput{
						Output: &bedrocktypes.ConverseOutputMemberMessage{
							Value: bedrocktypes.Message{
								Role: bedrocktypes.ConversationRoleAssistant,
								Content: []bedrocktypes.ContentBlock{
									&bedrocktypes.ContentBlockMemberText{Value: "ok"},
								},
							},
						},
						StopReason: bedrocktypes.StopReasonEndTurn,
						Usage: &bedrocktypes.TokenUsage{
							InputTokens:  aws.Int32(1),
							OutputTokens: aws.Int32(1),
							TotalTokens:  aws.Int32(2),
						},
					}, nil
				},
			}, nil
		},
	}

	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "amazon-bedrock",
		Model:    "us.anthropic.claude-opus-4-6-v1",
		Options: ChatOptions{
			APIKey:     "bedrock-token",
			MaxRetries: 1,
		},
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d", attempts)
	}
	if result.Text != "ok" {
		t.Fatalf("text = %q", result.Text)
	}
}
