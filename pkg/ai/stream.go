package ai

import (
	"context"
	"sync"
)

type EventStream struct {
	events     chan NormalizedEvent
	done       chan streamResult
	resultOnce sync.Once
	closeOnce  sync.Once
	closeMu    sync.RWMutex
	closed     bool
	result     streamResult
}

type streamResult struct {
	result NormalizedResult
	err    error
}

func (stream *EventStream) Events() <-chan NormalizedEvent {
	return stream.events
}

func (stream *EventStream) Result() (NormalizedResult, error) {
	stream.resultOnce.Do(func() {
		stream.result = <-stream.done
	})
	return stream.result.result, stream.result.err
}

func CreateEventStream() *EventStream {
	return &EventStream{
		events: make(chan NormalizedEvent, 32),
		done:   make(chan streamResult, 1),
	}
}

func (stream *EventStream) Push(event NormalizedEvent) bool {
	stream.closeMu.RLock()
	defer stream.closeMu.RUnlock()
	if stream.closed {
		return false
	}
	stream.events <- event
	return true
}

func (stream *EventStream) Close(result NormalizedResult, err error) {
	stream.closeOnce.Do(func() {
		stream.closeMu.Lock()
		stream.closed = true
		close(stream.events)
		stream.closeMu.Unlock()
		stream.done <- streamResult{result: result, err: err}
		close(stream.done)
	})
}

func Stream(ctx context.Context, req CompletionRequest) *EventStream {
	req.Options.Stream = true
	stream := CreateEventStream()

	go func() {
		result, events, err := Complete(ctx, req)
		if err != nil {
			if result.StopReason == "" {
				stopReason := "error"
				if ctx.Err() != nil {
					stopReason = "aborted"
				}
				result.StopReason = stopReason
				if result.ErrorMessage == "" {
					result.ErrorMessage = err.Error()
				}
			}
			result = FillResultMetadata(result, req)
			if len(events) == 0 {
				events = []NormalizedEvent{
					{Type: "start"},
					{Type: "error", Reason: result.StopReason, ErrorMessage: result.ErrorMessage},
				}
			}
		}
		result = FillResultMetadata(result, req)
		for _, event := range AttachEventPayloads(events, result) {
			select {
			case <-ctx.Done():
				stream.Close(result, err)
				return
			case stream.events <- event:
			}
		}
		stream.Close(result, err)
	}()

	return stream
}

func errorEventStream(ctx context.Context, err error) *EventStream {
	stream := CreateEventStream()
	go func() {
		stopReason := "error"
		if ctx.Err() != nil {
			stopReason = "aborted"
		}
		result := NormalizedResult{
			Role:         "assistant",
			StopReason:   stopReason,
			ErrorMessage: err.Error(),
		}
		for _, event := range AttachEventPayloads([]NormalizedEvent{
			{Type: "start"},
			{Type: "error", Reason: stopReason, ErrorMessage: err.Error()},
		}, result) {
			stream.events <- event
		}
		stream.Close(result, err)
	}()
	return stream
}
