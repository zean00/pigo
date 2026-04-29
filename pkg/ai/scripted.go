package ai

type ScriptedResponse struct {
	Content    []ContentBlock
	StopReason string
}

func NewTextResponse(text string) ScriptedResponse {
	return ScriptedResponse{
		Content:    []ContentBlock{{Type: "text", Text: text}},
		StopReason: "stop",
	}
}

func NewToolResponse(id, name string, args map[string]any) ScriptedResponse {
	return ScriptedResponse{
		Content:    []ContentBlock{{Type: "toolCall", ID: id, Name: name, Arguments: args}},
		StopReason: "toolUse",
	}
}

func NewErrorResponse() ScriptedResponse {
	return ScriptedResponse{StopReason: "error"}
}

func NormalizeResponse(response ScriptedResponse) (NormalizedResult, []NormalizedEvent) {
	stopReason := response.StopReason
	if stopReason == "" {
		stopReason = "stop"
	}
	result := NormalizedResult{
		Role:       "assistant",
		StopReason: stopReason,
		Text:       ContentText(response.Content),
		Content:    NormalizedContent(response.Content),
		Usage:      &Usage{Input: 1, Output: 1, CacheRead: 0, CacheWrite: 0, TotalTokens: 2},
	}
	if stopReason == "error" || stopReason == "aborted" {
		result.Usage = nil
	}
	return result, AssistantEvents(response.Content, stopReason)
}
