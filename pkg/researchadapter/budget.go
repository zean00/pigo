package researchadapter

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/badlogic/pigo/pkg/agentcore"
)

type ToolBudget struct {
	Grouping map[string]string `json:"grouping,omitempty"`
	Limits   map[string]int    `json:"limits,omitempty"`

	mu    sync.Mutex
	usage map[string]int
}

func DefaultToolBudget() *ToolBudget {
	return &ToolBudget{
		Grouping: map[string]string{
			"search":          "gathering",
			"security_search": "gathering",
			"grep":            "gathering",
			"scrape":          "scrape",
		},
		Limits: map[string]int{
			"gathering": 4,
			"scrape":    4,
		},
		usage: map[string]int{},
	}
}

func (b *ToolBudget) BeforeToolCall(_ context.Context, input agentcore.BeforeToolCallContext) (agentcore.BeforeToolCallResult, error) {
	if b == nil {
		return agentcore.BeforeToolCallResult{Args: input.Args}, nil
	}
	category := b.category(input.ToolCall.Name)
	if category == "" {
		return agentcore.BeforeToolCallResult{Args: input.Args}, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.usage == nil {
		b.usage = map[string]int{}
	}
	limit := b.Limits[category]
	used := b.usage[category]
	if limit > 0 && used >= limit {
		return agentcore.BeforeToolCallResult{
			Block:  true,
			Reason: fmt.Sprintf("research %s tool budget exhausted (%d/%d)", category, used, limit),
			Args:   input.Args,
		}, nil
	}
	b.usage[category] = used + 1
	return agentcore.BeforeToolCallResult{Args: input.Args}, nil
}

func (b *ToolBudget) Usage() map[string]int {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]int, len(b.usage))
	for key, value := range b.usage {
		out[key] = value
	}
	return out
}

func (b *ToolBudget) category(toolName string) string {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if b.Grouping == nil {
		return ""
	}
	return b.Grouping[name]
}
