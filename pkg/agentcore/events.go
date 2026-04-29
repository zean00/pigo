package agentcore

type EventType string

const (
	EventAgentStart          EventType = "agent_start"
	EventAgentEnd            EventType = "agent_end"
	EventTurnStart           EventType = "turn_start"
	EventTurnEnd             EventType = "turn_end"
	EventMessageStart        EventType = "message_start"
	EventMessageUpdate       EventType = "message_update"
	EventMessageEnd          EventType = "message_end"
	EventToolExecutionStart  EventType = "tool_execution_start"
	EventToolExecutionUpdate EventType = "tool_execution_update"
	EventToolExecutionEnd    EventType = "tool_execution_end"
)

type AgentStartEvent struct {
	Type EventType
}

type AgentEndEvent struct {
	Type         EventType
	MessageCount int
}

type TurnStartEvent struct {
	Type EventType
}

type TurnEndEvent struct {
	Type            EventType
	Message         Message
	ToolResults     []Message
	MessageRole     string
	ToolResultCount int
}

type MessageEvent struct {
	Type    EventType
	Message Message
}

type MessageUpdateEvent struct {
	Type                  EventType
	Message               Message
	AssistantEventType    string
	AssistantMessageEvent map[string]any
}

type ToolExecutionStartEvent struct {
	Type       EventType
	ToolCallID string
	ToolName   string
	Args       map[string]any
}

type ToolExecutionUpdateEvent struct {
	Type          EventType
	ToolCallID    string
	ToolName      string
	Args          map[string]any
	PartialResult map[string]any
}

type ToolExecutionEndEvent struct {
	Type       EventType
	ToolCallID string
	ToolName   string
	Result     map[string]any
	IsError    bool
	Text       string
}

func TypedEvent(event Event) any {
	switch EventType(eventType(event)) {
	case EventAgentStart:
		return AgentStartEvent{Type: EventAgentStart}
	case EventAgentEnd:
		return AgentEndEvent{Type: EventAgentEnd, MessageCount: eventInt(event["messageCount"])}
	case EventTurnStart:
		return TurnStartEvent{Type: EventTurnStart}
	case EventTurnEnd:
		return TurnEndEvent{
			Type:            EventTurnEnd,
			Message:         eventMessage(event["message"]),
			ToolResults:     eventMessages(event["toolResults"]),
			MessageRole:     eventString(event["messageRole"]),
			ToolResultCount: eventInt(event["toolResultCount"]),
		}
	case EventMessageStart:
		return MessageEvent{Type: EventMessageStart, Message: eventMessage(event["message"])}
	case EventMessageEnd:
		return MessageEvent{Type: EventMessageEnd, Message: eventMessage(event["message"])}
	case EventMessageUpdate:
		return MessageUpdateEvent{
			Type:                  EventMessageUpdate,
			Message:               eventMessage(event["message"]),
			AssistantEventType:    eventString(event["assistantEventType"]),
			AssistantMessageEvent: eventMap(event["assistantMessageEvent"]),
		}
	case EventToolExecutionStart:
		return ToolExecutionStartEvent{
			Type:       EventToolExecutionStart,
			ToolCallID: eventString(event["toolCallId"]),
			ToolName:   eventString(event["toolName"]),
			Args:       eventMap(event["args"]),
		}
	case EventToolExecutionUpdate:
		return ToolExecutionUpdateEvent{
			Type:          EventToolExecutionUpdate,
			ToolCallID:    eventString(event["toolCallId"]),
			ToolName:      eventString(event["toolName"]),
			Args:          eventMap(event["args"]),
			PartialResult: eventMap(event["partialResult"]),
		}
	case EventToolExecutionEnd:
		return ToolExecutionEndEvent{
			Type:       EventToolExecutionEnd,
			ToolCallID: eventString(event["toolCallId"]),
			ToolName:   eventString(event["toolName"]),
			Result:     eventMap(event["result"]),
			IsError:    eventBool(event["isError"]),
			Text:       eventString(event["text"]),
		}
	default:
		return event
	}
}

func eventString(value any) string {
	text, _ := value.(string)
	return text
}

func eventBool(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func eventInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func eventMap(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func eventMessage(value any) Message {
	message, _ := value.(Message)
	return cloneMessage(message)
}

func eventMessages(value any) []Message {
	messages, _ := value.([]Message)
	return cloneMessages(messages)
}
