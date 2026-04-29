package ai

import "testing"

func TestValidateToolArgumentsCoercesPrimitiveValues(t *testing.T) {
	arguments, err := ValidateToolArguments(Tool{
		Name: "echo",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"count":   map[string]any{"type": "integer"},
				"enabled": map[string]any{"type": "boolean"},
			},
			"required":             []any{"count", "enabled"},
			"additionalProperties": false,
		},
	}, ContentBlock{
		Type: "toolCall",
		Name: "echo",
		Arguments: map[string]any{
			"count":   "4",
			"enabled": "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := arguments["count"]; got != float64(4) {
		t.Fatalf("count = %#v", got)
	}
	if got := arguments["enabled"]; got != true {
		t.Fatalf("enabled = %#v", got)
	}
}

func TestValidateToolArgumentsRejectsUnknownProperty(t *testing.T) {
	_, err := ValidateToolArguments(Tool{
		Name: "echo",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
			"required":             []any{"value"},
			"additionalProperties": false,
		},
	}, ContentBlock{
		Type: "toolCall",
		Name: "echo",
		Arguments: map[string]any{
			"value": "ok",
			"extra": true,
		},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}
