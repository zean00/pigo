package ai

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

var validJSONEscapes = map[byte]struct{}{
	'"':  {},
	'\\': {},
	'/':  {},
	'b':  {},
	'f':  {},
	'n':  {},
	'r':  {},
	't':  {},
	'u':  {},
}

func isJSONStringControl(r rune) bool {
	return r >= 0x00 && r <= 0x1f
}

func escapeJSONStringControl(r rune) string {
	switch r {
	case '\b':
		return "\\b"
	case '\f':
		return "\\f"
	case '\n':
		return "\\n"
	case '\r':
		return "\\r"
	case '\t':
		return "\\t"
	default:
		return "\\u" + strings.ToUpper(paddedHex16(uint16(r)))
	}
}

func paddedHex16(value uint16) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{
		digits[(value>>12)&0xf],
		digits[(value>>8)&0xf],
		digits[(value>>4)&0xf],
		digits[value&0xf],
	})
}

func RepairJSON(input string) string {
	var out strings.Builder
	out.Grow(len(input) + 8)
	inString := false

	for i := 0; i < len(input); i++ {
		ch := input[i]
		if !inString {
			out.WriteByte(ch)
			if ch == '"' {
				inString = true
			}
			continue
		}

		if ch == '"' {
			out.WriteByte(ch)
			inString = false
			continue
		}

		if ch == '\\' {
			if i+1 >= len(input) {
				out.WriteString("\\\\")
				continue
			}
			next := input[i+1]
			if next == 'u' && i+5 < len(input) && isHex4(input[i+2:i+6]) {
				out.WriteString(input[i : i+6])
				i += 5
				continue
			}
			if _, ok := validJSONEscapes[next]; ok {
				out.WriteByte('\\')
				out.WriteByte(next)
				i++
				continue
			}
			out.WriteString("\\\\")
			continue
		}

		r, size := utf8.DecodeRuneInString(input[i:])
		if r == utf8.RuneError && size == 1 {
			out.WriteByte(ch)
			continue
		}
		if isJSONStringControl(r) {
			out.WriteString(escapeJSONStringControl(r))
		} else {
			out.WriteString(input[i : i+size])
		}
		i += size - 1
	}

	return out.String()
}

func isHex4(value string) bool {
	if len(value) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		ch := value[i]
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

func ParseJSONWithRepair[T any](input string) (T, error) {
	var value T
	if err := json.Unmarshal([]byte(input), &value); err == nil {
		return value, nil
	}
	repaired := RepairJSON(input)
	err := json.Unmarshal([]byte(repaired), &value)
	return value, err
}

func ParseStreamingJSON[T any](partial string) T {
	var zero T
	if strings.TrimSpace(partial) == "" {
		return zero
	}
	if value, err := ParseJSONWithRepair[T](partial); err == nil {
		return value
	}
	if candidate, ok := completePartialJSON(partial); ok {
		if value, err := ParseJSONWithRepair[T](candidate); err == nil {
			return value
		}
	}
	if candidate, ok := completePartialJSON(RepairJSON(partial)); ok {
		if value, err := ParseJSONWithRepair[T](candidate); err == nil {
			return value
		}
	}
	return zero
}

func completePartialJSON(input string) (string, bool) {
	var out strings.Builder
	out.Grow(len(input) + 8)
	stack := make([]byte, 0, 8)
	inString := false
	escape := false

	for _, r := range input {
		out.WriteRune(r)
		if inString {
			if escape {
				escape = false
				continue
			}
			if r == '\\' {
				escape = true
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}':
			if len(stack) == 0 || stack[len(stack)-1] != '}' {
				return "", false
			}
			stack = stack[:len(stack)-1]
		case ']':
			if len(stack) == 0 || stack[len(stack)-1] != ']' {
				return "", false
			}
			stack = stack[:len(stack)-1]
		}
	}

	if escape {
		out.WriteByte('\\')
	}
	if inString {
		out.WriteByte('"')
	}
	for i := len(stack) - 1; i >= 0; i-- {
		out.WriteByte(stack[i])
	}
	return out.String(), true
}
