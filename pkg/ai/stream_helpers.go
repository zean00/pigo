package ai

import (
	"bufio"
	"io"
	"strings"
)

type sseEvent struct {
	Event string
	Data  string
}

func scanSSE(body io.Reader, fn func(event sseEvent) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentEvent string
	var dataLines []string
	flush := func() error {
		if currentEvent == "" && len(dataLines) == 0 {
			return nil
		}
		event := sseEvent{
			Event: strings.TrimSpace(currentEvent),
			Data:  strings.Join(dataLines, "\n"),
		}
		currentEvent = ""
		dataLines = nil
		if strings.TrimSpace(event.Data) == "" {
			return nil
		}
		return fn(event)
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}
