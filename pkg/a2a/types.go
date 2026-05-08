package a2a

import "encoding/json"

const (
	ProtocolVersion = "0.3.0"

	TaskStateSubmitted = "submitted"
	TaskStateWorking   = "working"
	TaskStateCompleted = "completed"
	TaskStateFailed    = "failed"
	TaskStateCanceled  = "canceled"
	TaskStateRejected  = "rejected"

	RoleUser  = "user"
	RoleAgent = "agent"
)

type AgentCard struct {
	ProtocolVersion                   string             `json:"protocolVersion"`
	Name                              string             `json:"name"`
	Description                       string             `json:"description,omitempty"`
	URL                               string             `json:"url"`
	PreferredTransport                string             `json:"preferredTransport,omitempty"`
	AdditionalInterfaces              []AgentInterface   `json:"additionalInterfaces,omitempty"`
	Provider                          *AgentProvider     `json:"provider,omitempty"`
	Version                           string             `json:"version"`
	DocumentationURL                  string             `json:"documentationUrl,omitempty"`
	Capabilities                      AgentCapabilities  `json:"capabilities"`
	SecuritySchemes                   map[string]any     `json:"securitySchemes,omitempty"`
	Security                          []map[string][]any `json:"security,omitempty"`
	DefaultInputModes                 []string           `json:"defaultInputModes"`
	DefaultOutputModes                []string           `json:"defaultOutputModes"`
	Skills                            []AgentSkill       `json:"skills"`
	SupportsAuthenticatedExtendedCard bool               `json:"supportsAuthenticatedExtendedCard,omitempty"`
}

type AgentProvider struct {
	Organization string `json:"organization,omitempty"`
	URL          string `json:"url,omitempty"`
}

type AgentInterface struct {
	URL       string `json:"url"`
	Transport string `json:"transport"`
}

type AgentCapabilities struct {
	Streaming              bool `json:"streaming,omitempty"`
	PushNotifications      bool `json:"pushNotifications,omitempty"`
	StateTransitionHistory bool `json:"stateTransitionHistory,omitempty"`
}

type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

type Part struct {
	Kind     string         `json:"kind"`
	Text     string         `json:"text,omitempty"`
	File     *FilePart      `json:"file,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type FilePart struct {
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
	URI      string `json:"uri,omitempty"`
}

type Message struct {
	Kind      string         `json:"kind"`
	MessageID string         `json:"messageId"`
	Role      string         `json:"role"`
	Parts     []Part         `json:"parts"`
	TaskID    string         `json:"taskId,omitempty"`
	ContextID string         `json:"contextId,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Task struct {
	Kind      string         `json:"kind"`
	ID        string         `json:"id"`
	ContextID string         `json:"contextId"`
	Status    TaskStatus     `json:"status"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
	History   []Message      `json:"history,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type TaskStatus struct {
	State     string   `json:"state"`
	Message   *Message `json:"message,omitempty"`
	Timestamp string   `json:"timestamp,omitempty"`
}

type Artifact struct {
	ArtifactID  string         `json:"artifactId"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parts       []Part         `json:"parts"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type MessageSendParams struct {
	Message       Message                   `json:"message"`
	Configuration *MessageSendConfiguration `json:"configuration,omitempty"`
	Metadata      map[string]any            `json:"metadata,omitempty"`
}

type MessageSendConfiguration struct {
	AcceptedOutputModes []string `json:"acceptedOutputModes,omitempty"`
	Blocking            *bool    `json:"blocking,omitempty"`
}

type TaskQueryParams struct {
	ID            string         `json:"id"`
	HistoryLength *int           `json:"historyLength,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type TaskIDParams struct {
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type TaskStatusUpdateEvent struct {
	Kind      string         `json:"kind"`
	TaskID    string         `json:"taskId"`
	ContextID string         `json:"contextId"`
	Status    TaskStatus     `json:"status"`
	Final     bool           `json:"final"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type TaskArtifactUpdateEvent struct {
	Kind      string         `json:"kind"`
	TaskID    string         `json:"taskId"`
	ContextID string         `json:"contextId"`
	Artifact  Artifact       `json:"artifact"`
	Append    bool           `json:"append,omitempty"`
	LastChunk bool           `json:"lastChunk,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func NewTextMessage(role, text string) Message {
	return Message{
		Kind:      "message",
		MessageID: NewID("msg"),
		Role:      role,
		Parts:     []Part{{Kind: "text", Text: text}},
	}
}

func TextFromParts(parts []Part) string {
	out := ""
	for _, part := range parts {
		if part.Kind == "text" {
			if out != "" {
				out += "\n"
			}
			out += part.Text
		}
	}
	return out
}
