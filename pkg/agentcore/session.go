package agentcore

import "context"

type AgentSession struct {
	Agent *Agent
}

func NewAgentSession(options AgentOptions) *AgentSession {
	return &AgentSession{Agent: NewAgent(options)}
}

func WrapAgentSession(agent *Agent) *AgentSession {
	return &AgentSession{Agent: agent}
}

func (session *AgentSession) State() AgentState {
	if session == nil || session.Agent == nil {
		return AgentState{}
	}
	return session.Agent.State()
}

func (session *AgentSession) Messages() []Message {
	return session.State().Messages
}

func (session *AgentSession) ReplaceMessages(messages []Message) {
	if session == nil || session.Agent == nil {
		return
	}
	session.Agent.SetMessages(messages)
}

func (session *AgentSession) Prompt(ctx context.Context, prompt string) error {
	if session == nil || session.Agent == nil {
		return nil
	}
	return session.Agent.Prompt(ctx, prompt)
}

func (session *AgentSession) PromptMessages(ctx context.Context, messages []Message) error {
	if session == nil || session.Agent == nil {
		return nil
	}
	return session.Agent.PromptMessages(ctx, messages)
}

func (session *AgentSession) Continue(ctx context.Context) error {
	if session == nil || session.Agent == nil {
		return nil
	}
	return session.Agent.Continue(ctx)
}

func (session *AgentSession) Steer(message Message) {
	if session == nil || session.Agent == nil {
		return
	}
	session.Agent.Steer(message)
}

func (session *AgentSession) FollowUp(message Message) {
	if session == nil || session.Agent == nil {
		return
	}
	session.Agent.FollowUp(message)
}

func (session *AgentSession) Abort() {
	if session == nil || session.Agent == nil {
		return
	}
	session.Agent.Abort()
}

func (session *AgentSession) WaitForIdle() {
	if session == nil || session.Agent == nil {
		return
	}
	session.Agent.WaitForIdle()
}

func (session *AgentSession) Subscribe(listener func(Event, context.Context)) func() {
	if session == nil || session.Agent == nil {
		return func() {}
	}
	return session.Agent.Subscribe(listener)
}
