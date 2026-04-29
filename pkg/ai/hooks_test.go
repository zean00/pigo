package ai

import "testing"

func TestApplyPayloadHookKeepsPayloadOnNilReturn(t *testing.T) {
	payload := map[string]any{"model": "test"}
	got, err := applyPayloadHook(CompletionRequest{
		Options: ChatOptions{
			OnPayload: func(payload any, req CompletionRequest) (any, error) {
				return nil, nil
			},
		},
	}, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected original payload")
	}
	asMap, ok := got.(map[string]any)
	if !ok || asMap["model"] != "test" {
		t.Fatalf("payload = %#v", got)
	}
}
