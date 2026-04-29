package ai

func SanitizeSurrogates(text string) string {
	if text == "" {
		return ""
	}
	data := []byte(text)
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if i+2 < len(data) && data[i] == 0xED && data[i+1] >= 0xA0 && data[i+1] <= 0xBF && data[i+2] >= 0x80 && data[i+2] <= 0xBF {
			i += 3
			continue
		}
		out = append(out, data[i])
		i++
	}
	return string(out)
}
