package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	bedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockdocument "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithybearer "github.com/aws/smithy-go/auth/bearer"
)

type bedrockConverseClient interface {
	Converse(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

type bedrockProvider struct {
	newClient func(context.Context, bedrockClientOptions) (bedrockConverseClient, error)
}

type bedrockClientOptions struct {
	BaseURL     string
	BearerToken string
	HTTPClient  *http.Client
	Region      string
	Profile     string
	SkipAuth    bool
}

func BedrockProvider() ChatProvider {
	return &bedrockProvider{newClient: newBedrockClient}
}

func (provider *bedrockProvider) Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	if req.Options.Stream {
		return NormalizedResult{}, nil, errors.New("streaming is not supported for amazon-bedrock yet")
	}
	if strings.TrimSpace(req.Model) == "" {
		return NormalizedResult{}, nil, errors.New("model is required")
	}

	bearerToken := strings.TrimSpace(req.Options.APIKey)
	if bearerToken == "" {
		bearerToken = strings.TrimSpace(os.Getenv("AWS_BEARER_TOKEN_BEDROCK"))
	}
	profile := strings.TrimSpace(os.Getenv("AWS_PROFILE"))
	skipAuth := strings.TrimSpace(os.Getenv("AWS_BEDROCK_SKIP_AUTH")) == "1"
	if bearerToken == "" && !hasBedrockCredentials() && !skipAuth {
		return NormalizedResult{}, nil, fmt.Errorf("missing API key for provider: %s", canonicalProviderName(req.Provider))
	}

	baseURL := strings.TrimSpace(req.Options.BaseURL)
	client, err := provider.newClient(ctx, bedrockClientOptions{
		BaseURL:     baseURL,
		BearerToken: bearerToken,
		HTTPClient:  bedrockHTTPClient(req.Options.HTTPClient, req.Options.Timeout),
		Region:      resolveBedrockRegion(baseURL, profile),
		Profile:     profile,
		SkipAuth:    skipAuth,
	})
	if err != nil {
		return NormalizedResult{}, nil, err
	}

	input, err := toBedrockInput(req)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	output, err := client.Converse(ctx, input)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: err.Error()}, nil, err
		}
		return NormalizedResult{}, nil, fmt.Errorf("call amazon bedrock API: %w", err)
	}

	result, err := bedrockOutputToResult(output)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
}

func newBedrockClient(ctx context.Context, opts bedrockClientOptions) (bedrockConverseClient, error) {
	loadOptions := make([]func(*awsconfig.LoadOptions) error, 0, 4)
	if opts.Region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(opts.Region))
	}
	if opts.Profile != "" {
		loadOptions = append(loadOptions, awsconfig.WithSharedConfigProfile(opts.Profile))
	}
	if opts.HTTPClient != nil {
		loadOptions = append(loadOptions, awsconfig.WithHTTPClient(opts.HTTPClient))
	}
	if opts.BearerToken != "" {
		loadOptions = append(loadOptions, awsconfig.WithBearerAuthTokenProvider(
			smithybearer.StaticTokenProvider{Token: smithybearer.Token{Value: opts.BearerToken}},
		))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := bedrockruntime.NewFromConfig(cfg, func(options *bedrockruntime.Options) {
		if opts.BaseURL != "" {
			options.BaseEndpoint = aws.String(strings.TrimSpace(opts.BaseURL))
		}
		switch {
		case opts.SkipAuth:
			options.AuthSchemePreference = []string{"noAuth"}
		case opts.BearerToken != "":
			options.AuthSchemePreference = []string{"httpBearerAuth"}
		}
	})
	return client, nil
}

func bedrockHTTPClient(httpClient *http.Client, timeout time.Duration) *http.Client {
	if httpClient == nil {
		if timeout <= 0 {
			return nil
		}
		return &http.Client{Timeout: timeout}
	}
	if timeout <= 0 {
		return httpClient
	}
	cloned := *httpClient
	cloned.Timeout = timeout
	return &cloned
}

func resolveBedrockRegion(baseURL, profile string) string {
	if region := strings.TrimSpace(os.Getenv("AWS_REGION")); region != "" {
		return region
	}
	if region := strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION")); region != "" {
		return region
	}
	if region := bedrockRegionFromBaseURL(baseURL); region != "" {
		return region
	}
	if strings.TrimSpace(profile) != "" {
		return ""
	}
	return "us-east-1"
}

func bedrockRegionFromBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	for _, prefix := range []string{"bedrock-runtime.", "bedrock."} {
		if !strings.HasPrefix(host, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(host, prefix)
		if idx := strings.Index(remainder, "."); idx > 0 {
			return remainder[:idx]
		}
	}
	return ""
}

func toBedrockInput(req CompletionRequest) (*bedrockruntime.ConverseInput, error) {
	messages, err := toBedrockMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	input := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(req.Model),
		Messages: messages,
	}
	if prompt := strings.TrimSpace(extractSystemPrompt(req.Messages)); prompt != "" {
		input.System = []bedrocktypes.SystemContentBlock{
			&bedrocktypes.SystemContentBlockMemberText{Value: prompt},
		}
	}
	if req.Options.Temperature != nil || req.Options.MaxTokens > 0 {
		config := &bedrocktypes.InferenceConfiguration{}
		if req.Options.Temperature != nil {
			temp := float32(*req.Options.Temperature)
			config.Temperature = &temp
		}
		if req.Options.MaxTokens > 0 {
			maxTokens := int32(req.Options.MaxTokens)
			config.MaxTokens = &maxTokens
		}
		input.InferenceConfig = config
	}
	if toolConfig := toBedrockToolConfiguration(req.Tools, req.Options.ToolChoice); toolConfig != nil {
		input.ToolConfig = toolConfig
	}
	return input, nil
}

func toBedrockMessages(messages []Message) ([]bedrocktypes.Message, error) {
	out := make([]bedrocktypes.Message, 0, len(messages))
	for _, message := range messages {
		switch {
		case strings.EqualFold(message.Role, "system"):
			continue
		case strings.EqualFold(message.Role, "user"):
			content, err := bedrockUserContent(message)
			if err != nil {
				return nil, err
			}
			if len(content) > 0 {
				out = append(out, bedrocktypes.Message{
					Role:    bedrocktypes.ConversationRoleUser,
					Content: content,
				})
			}
		case strings.EqualFold(message.Role, "assistant"):
			content := bedrockAssistantContent(message)
			if len(content) > 0 {
				out = append(out, bedrocktypes.Message{
					Role:    bedrocktypes.ConversationRoleAssistant,
					Content: content,
				})
			}
		case strings.EqualFold(message.Role, "toolResult"):
			content := bedrockToolResultContent(message)
			if len(content) > 0 {
				out = append(out, bedrocktypes.Message{
					Role:    bedrocktypes.ConversationRoleUser,
					Content: content,
				})
			}
		}
	}
	return out, nil
}

func bedrockUserContent(message Message) ([]bedrocktypes.ContentBlock, error) {
	blocks := messageContentBlocks(message.Content)
	if len(blocks) == 0 {
		text := strings.TrimSpace(MessageText(message))
		if text == "" {
			return nil, nil
		}
		return []bedrocktypes.ContentBlock{
			&bedrocktypes.ContentBlockMemberText{Value: text},
		}, nil
	}

	content := make([]bedrocktypes.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				content = append(content, &bedrocktypes.ContentBlockMemberText{Value: block.Text})
			}
		case "image":
			imageBlock, err := bedrockImageBlock(block)
			if err != nil {
				return nil, err
			}
			if imageBlock != nil {
				content = append(content, &bedrocktypes.ContentBlockMemberImage{Value: *imageBlock})
			}
		}
	}
	return content, nil
}

func bedrockAssistantContent(message Message) []bedrocktypes.ContentBlock {
	blocks := messageContentBlocks(message.Content)
	if len(blocks) == 0 {
		text := strings.TrimSpace(MessageText(message))
		if text == "" {
			return nil
		}
		return []bedrocktypes.ContentBlock{
			&bedrocktypes.ContentBlockMemberText{Value: text},
		}
	}

	content := make([]bedrocktypes.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				content = append(content, &bedrocktypes.ContentBlockMemberText{Value: block.Text})
			}
		case "thinking":
			if block.Redacted {
				content = append(content, &bedrocktypes.ContentBlockMemberReasoningContent{
					Value: &bedrocktypes.ReasoningContentBlockMemberRedactedContent{Value: []byte(block.Thinking)},
				})
				continue
			}
			if strings.TrimSpace(block.Thinking) != "" {
				content = append(content, &bedrocktypes.ContentBlockMemberReasoningContent{
					Value: &bedrocktypes.ReasoningContentBlockMemberReasoningText{
						Value: bedrocktypes.ReasoningTextBlock{Text: aws.String(block.Thinking)},
					},
				})
			}
		case "toolCall":
			if strings.TrimSpace(block.ID) == "" || strings.TrimSpace(block.Name) == "" {
				continue
			}
			arguments := block.Arguments
			if arguments == nil {
				arguments = map[string]any{}
			}
			content = append(content, &bedrocktypes.ContentBlockMemberToolUse{
				Value: bedrocktypes.ToolUseBlock{
					ToolUseId: aws.String(block.ID),
					Name:      aws.String(block.Name),
					Input:     bedrockdocument.NewLazyDocument(arguments),
				},
			})
		}
	}
	return content
}

func bedrockToolResultContent(message Message) []bedrocktypes.ContentBlock {
	toolUseID := strings.TrimSpace(message.ToolCallID)
	if toolUseID == "" {
		return nil
	}
	contentText := strings.TrimSpace(MessageText(message))
	if contentText == "" {
		contentText = "{}"
	}
	toolResult := bedrocktypes.ToolResultBlock{
		ToolUseId: aws.String(toolUseID),
		Content: []bedrocktypes.ToolResultContentBlock{
			&bedrocktypes.ToolResultContentBlockMemberText{Value: contentText},
		},
	}
	if message.IsError {
		toolResult.Status = bedrocktypes.ToolResultStatusError
	} else {
		toolResult.Status = bedrocktypes.ToolResultStatusSuccess
	}
	return []bedrocktypes.ContentBlock{
		&bedrocktypes.ContentBlockMemberToolResult{Value: toolResult},
	}
}

func bedrockImageBlock(block ContentBlock) (*bedrocktypes.ImageBlock, error) {
	data := strings.TrimSpace(block.Data)
	mimeType := strings.TrimSpace(block.MimeType)
	if data == "" || mimeType == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("decode bedrock image content: %w", err)
		}
	}
	format, ok := bedrockImageFormat(mimeType)
	if !ok {
		return nil, fmt.Errorf("unsupported bedrock image media type: %s", mimeType)
	}
	return &bedrocktypes.ImageBlock{
		Format: format,
		Source: &bedrocktypes.ImageSourceMemberBytes{Value: decoded},
	}, nil
}

func bedrockImageFormat(mimeType string) (bedrocktypes.ImageFormat, bool) {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return bedrocktypes.ImageFormatPng, true
	case "image/jpeg", "image/jpg":
		return bedrocktypes.ImageFormatJpeg, true
	case "image/gif":
		return bedrocktypes.ImageFormatGif, true
	case "image/webp":
		return bedrocktypes.ImageFormatWebp, true
	default:
		return "", false
	}
}

func toBedrockToolConfiguration(tools []Tool, choice string) *bedrocktypes.ToolConfiguration {
	if len(tools) == 0 {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(choice), "none") {
		return nil
	}

	result := &bedrocktypes.ToolConfiguration{
		Tools: make([]bedrocktypes.Tool, 0, len(tools)),
	}
	for _, tool := range tools {
		parameters := tool.Parameters
		if parameters == nil {
			parameters = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": true,
			}
		}
		result.Tools = append(result.Tools, &bedrocktypes.ToolMemberToolSpec{
			Value: bedrocktypes.ToolSpecification{
				Name:        aws.String(tool.Name),
				Description: aws.String(strings.TrimSpace(tool.Description)),
				InputSchema: &bedrocktypes.ToolInputSchemaMemberJson{Value: bedrockdocument.NewLazyDocument(parameters)},
			},
		})
	}

	switch normalized := strings.TrimSpace(choice); normalized {
	case "", "auto":
		result.ToolChoice = &bedrocktypes.ToolChoiceMemberAuto{Value: bedrocktypes.AutoToolChoice{}}
	case "any":
		result.ToolChoice = &bedrocktypes.ToolChoiceMemberAny{Value: bedrocktypes.AnyToolChoice{}}
	default:
		result.ToolChoice = &bedrocktypes.ToolChoiceMemberTool{
			Value: bedrocktypes.SpecificToolChoice{Name: aws.String(normalized)},
		}
	}
	return result
}

func bedrockOutputToResult(output *bedrockruntime.ConverseOutput) (NormalizedResult, error) {
	if output == nil {
		return NormalizedResult{}, errors.New("empty amazon bedrock response")
	}

	blocks := make([]ContentBlock, 0)
	if message, ok := output.Output.(*bedrocktypes.ConverseOutputMemberMessage); ok {
		for _, block := range message.Value.Content {
			normalized, err := normalizeBedrockBlock(block)
			if err != nil {
				return NormalizedResult{}, err
			}
			if normalized != nil {
				blocks = append(blocks, *normalized)
			}
		}
	}

	result := NormalizedResult{
		Role:       "assistant",
		StopReason: mapBedrockStopReason(output.StopReason),
		Text:       ContentText(blocks),
		Content:    NormalizedContent(blocks),
	}
	if output.Usage != nil {
		result.Usage = &Usage{
			Input:       int32Value(output.Usage.InputTokens),
			Output:      int32Value(output.Usage.OutputTokens),
			CacheRead:   int32Value(output.Usage.CacheReadInputTokens),
			CacheWrite:  int32Value(output.Usage.CacheWriteInputTokens),
			TotalTokens: int32Value(output.Usage.TotalTokens),
		}
	}
	return result, nil
}

func normalizeBedrockBlock(block bedrocktypes.ContentBlock) (*ContentBlock, error) {
	switch typed := block.(type) {
	case *bedrocktypes.ContentBlockMemberText:
		return &ContentBlock{Type: "text", Text: typed.Value}, nil
	case *bedrocktypes.ContentBlockMemberToolUse:
		arguments, err := bedrockDocumentMap(typed.Value.Input)
		if err != nil {
			return nil, fmt.Errorf("decode bedrock tool input: %w", err)
		}
		return &ContentBlock{
			Type:      "toolCall",
			ID:        strings.TrimSpace(aws.ToString(typed.Value.ToolUseId)),
			Name:      strings.TrimSpace(aws.ToString(typed.Value.Name)),
			Arguments: arguments,
		}, nil
	case *bedrocktypes.ContentBlockMemberReasoningContent:
		switch reasoning := typed.Value.(type) {
		case *bedrocktypes.ReasoningContentBlockMemberReasoningText:
			return &ContentBlock{Type: "thinking", Thinking: aws.ToString(reasoning.Value.Text)}, nil
		case *bedrocktypes.ReasoningContentBlockMemberRedactedContent:
			return &ContentBlock{Type: "thinking", Thinking: string(reasoning.Value), Redacted: true}, nil
		}
	}
	return nil, nil
}

func bedrockDocumentMap(value bedrockdocument.Interface) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	data, err := value.MarshalSmithyDocument()
	if err != nil {
		return nil, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err == nil {
		if parsed == nil {
			return map[string]any{}, nil
		}
		return parsed, nil
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return map[string]any{}, nil
	}
	if mapped, ok := raw.(map[string]any); ok {
		return mapped, nil
	}
	return map[string]any{"value": raw}, nil
}

func mapBedrockStopReason(reason bedrocktypes.StopReason) string {
	switch reason {
	case bedrocktypes.StopReasonToolUse:
		return "toolUse"
	case bedrocktypes.StopReasonMaxTokens, bedrocktypes.StopReasonModelContextWindowExceeded:
		return "length"
	case bedrocktypes.StopReasonGuardrailIntervened, bedrocktypes.StopReasonContentFiltered, bedrocktypes.StopReasonMalformedModelOutput, bedrocktypes.StopReasonMalformedToolUse:
		return "error"
	case bedrocktypes.StopReasonEndTurn, bedrocktypes.StopReasonStopSequence, "":
		return "stop"
	default:
		return "stop"
	}
}

func int32Value(value *int32) int {
	if value == nil {
		return 0
	}
	return int(*value)
}
