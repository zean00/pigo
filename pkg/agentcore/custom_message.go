package agentcore

type CustomMessage struct {
	Role       string
	CustomType string
	Content    any
	Display    bool
	Details    any
}

func NewCustomMessage(customType string, content any, display bool, details any) Message {
	return Message{
		"role":       "custom",
		"customType": customType,
		"content":    content,
		"display":    display,
		"details":    details,
	}
}

func IsCustomMessage(message Message) bool {
	role, _ := message["role"].(string)
	return role == "custom"
}

func AsCustomMessage(message Message) (CustomMessage, bool) {
	if !IsCustomMessage(message) {
		return CustomMessage{}, false
	}
	customType, _ := message["customType"].(string)
	display, _ := message["display"].(bool)
	return CustomMessage{
		Role:       "custom",
		CustomType: customType,
		Content:    message["content"],
		Display:    display,
		Details:    message["details"],
	}, true
}
