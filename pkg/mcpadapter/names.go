package mcpadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func SanitizeToolSegment(value string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if valid {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(builder.String(), "_")
	if out == "" {
		return "unnamed"
	}
	return out
}

func MakeToolName(serverName, toolName string) string {
	server := SanitizeToolSegment(serverName)
	tool := SanitizeToolSegment(toolName)
	full := "mcp__" + server + "__" + tool
	if len(full) <= 64 {
		return full
	}
	sum := sha256.Sum256([]byte(serverName + "\x00" + toolName))
	hash := hex.EncodeToString(sum[:])[:10]
	prefix := "mcp__" + hash + "__"
	maxTail := 64 - len(prefix)
	if maxTail < 1 {
		return prefix[:64]
	}
	if len(tool) > maxTail {
		tool = tool[len(tool)-maxTail:]
		tool = strings.Trim(tool, "_")
	}
	if tool == "" {
		tool = "tool"
	}
	if len(tool) > maxTail {
		tool = tool[:maxTail]
	}
	return prefix + tool
}
