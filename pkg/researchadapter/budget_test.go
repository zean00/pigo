package researchadapter

import (
	"context"
	"testing"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

func TestToolBudgetCountsGrepAsGathering(t *testing.T) {
	budget := DefaultToolBudget()
	budget.Limits["gathering"] = 1
	first, err := budget.BeforeToolCall(context.Background(), agentcore.BeforeToolCallContext{
		ToolCall: ai.ContentBlock{Name: "grep"},
		Args:     map[string]any{"pattern": "needle"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Block {
		t.Fatalf("first result = %#v", first)
	}
	second, err := budget.BeforeToolCall(context.Background(), agentcore.BeforeToolCallContext{
		ToolCall: ai.ContentBlock{Name: "search"},
		Args:     map[string]any{"query": "pigo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Block || second.Reason == "" {
		t.Fatalf("second result = %#v", second)
	}
	if usage := budget.Usage(); usage["gathering"] != 1 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestToolBudgetIgnoresUnmappedTools(t *testing.T) {
	budget := DefaultToolBudget()
	result, err := budget.BeforeToolCall(context.Background(), agentcore.BeforeToolCallContext{
		ToolCall: ai.ContentBlock{Name: "read"},
		Args:     map[string]any{"path": "README.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Block || len(budget.Usage()) != 0 {
		t.Fatalf("result = %#v usage = %#v", result, budget.Usage())
	}
}

func TestToolBudgetNormalizesConfiguredNames(t *testing.T) {
	budget := &ToolBudget{
		Grouping: map[string]string{" Search ": " Gathering "},
		Limits:   map[string]int{" Gathering ": 1},
	}
	first, err := budget.BeforeToolCall(context.Background(), agentcore.BeforeToolCallContext{
		ToolCall: ai.ContentBlock{Name: "search"},
		Args:     map[string]any{"query": "pigo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Block {
		t.Fatalf("first result = %#v", first)
	}
	second, err := budget.BeforeToolCall(context.Background(), agentcore.BeforeToolCallContext{
		ToolCall: ai.ContentBlock{Name: "SEARCH"},
		Args:     map[string]any{"query": "pigo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Block {
		t.Fatalf("second result = %#v", second)
	}
	if usage := budget.Usage(); usage["gathering"] != 1 {
		t.Fatalf("usage = %#v", usage)
	}
}
