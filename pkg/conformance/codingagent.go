package conformance

import (
	"github.com/badlogic/pigo/pkg/codingagent"
)

func NewCodingAgentSessionFromCase(root string, testCase CodingAgentCase) (*codingagent.Session, error) {
	for name, content := range testCase.Workspace.Files {
		if err := codingagent.WriteWorkspaceFile(root, name, content); err != nil {
			return nil, err
		}
	}
	turns := make([]codingagent.AssistantTurn, 0, len(testCase.AssistantTurns))
	for _, turn := range testCase.AssistantTurns {
		blocks, err := ParseTurnContent(turn.Content)
		if err != nil {
			return nil, err
		}
		turns = append(turns, codingagent.AssistantTurn{Content: blocks, StopReason: defaultStopReason(turn.StopReason)})
	}
	return codingagent.NewSession(root, turns), nil
}
