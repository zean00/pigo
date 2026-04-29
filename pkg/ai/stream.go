package ai

import "context"

type EventStream struct {
	events chan NormalizedEvent
	done   chan streamResult
}

type streamResult struct {
	result NormalizedResult
	err    error
}

func (stream *EventStream) Events() <-chan NormalizedEvent {
	return stream.events
}

func (stream *EventStream) Result() (NormalizedResult, error) {
	result := <-stream.done
	return result.result, result.err
}

func Stream(ctx context.Context, req CompletionRequest) *EventStream {
	req.Options.Stream = true
	stream := &EventStream{
		events: make(chan NormalizedEvent, 32),
		done:   make(chan streamResult, 1),
	}

	go func() {
		defer close(stream.events)
		result, events, err := Complete(ctx, req)
		if err != nil {
			if result.Role == "" {
				stopReason := "error"
				if ctx.Err() != nil {
					stopReason = "aborted"
				}
				result = NormalizedResult{
					Role:         "assistant",
					StopReason:   stopReason,
					ErrorMessage: err.Error(),
				}
			}
			if len(events) == 0 {
				events = []NormalizedEvent{
					{Type: "start"},
					{Type: "error", Reason: result.StopReason, ErrorMessage: result.ErrorMessage},
				}
			}
		}
		for _, event := range events {
			select {
			case <-ctx.Done():
				stream.done <- streamResult{result: result, err: err}
				close(stream.done)
				return
			case stream.events <- event:
			}
		}
		stream.done <- streamResult{result: result, err: err}
		close(stream.done)
	}()

	return stream
}
