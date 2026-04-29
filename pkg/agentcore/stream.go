package agentcore

import (
	"context"
	"fmt"
	"sync"

	"github.com/badlogic/pigo/pkg/ai"
)

type AgentEventStream struct {
	events     chan Event
	done       chan agentStreamResult
	resultOnce sync.Once
	closeOnce  sync.Once
	closeMu    sync.RWMutex
	closed     bool
	result     agentStreamResult
}

type TypedAgentEventStream struct {
	source *AgentEventStream
	events chan any
	once   sync.Once
}

type agentStreamResult struct {
	messages []Message
	err      error
}

func CreateAgentEventStream() *AgentEventStream {
	return &AgentEventStream{
		events: make(chan Event, 32),
		done:   make(chan agentStreamResult, 1),
	}
}

func (stream *AgentEventStream) Events() <-chan Event {
	return stream.events
}

func (stream *AgentEventStream) TypedEvents() <-chan any {
	typed := &TypedAgentEventStream{source: stream, events: make(chan any, 32)}
	typed.start()
	return typed.events
}

func (stream *AgentEventStream) Result() ([]Message, error) {
	stream.resultOnce.Do(func() {
		stream.result = <-stream.done
	})
	return cloneMessages(stream.result.messages), stream.result.err
}

func (stream *TypedAgentEventStream) Events() <-chan any {
	stream.start()
	return stream.events
}

func (stream *TypedAgentEventStream) Result() ([]Message, error) {
	return stream.source.Result()
}

func (stream *TypedAgentEventStream) start() {
	stream.once.Do(func() {
		go func() {
			defer close(stream.events)
			for event := range stream.source.Events() {
				stream.events <- TypedEvent(event)
			}
		}()
	})
}

func (stream *AgentEventStream) Push(event Event) bool {
	stream.closeMu.RLock()
	defer stream.closeMu.RUnlock()
	if stream.closed {
		return false
	}
	stream.events <- cloneEvent(event)
	return true
}

func (stream *AgentEventStream) Close(messages []Message, err error) {
	stream.closeOnce.Do(func() {
		stream.closeMu.Lock()
		stream.closed = true
		close(stream.events)
		stream.closeMu.Unlock()
		stream.done <- agentStreamResult{messages: cloneMessages(messages), err: err}
		close(stream.done)
	})
}

func RunProviderLoopStream(ctx context.Context, input ProviderLoopInput) *AgentEventStream {
	stream := CreateAgentEventStream()
	userSink := input.EventSink
	input.EventSink = func(event Event) {
		if userSink != nil {
			userSink(event)
		}
		stream.Push(event)
	}
	go func() {
		result, err := RunProviderLoop(ctx, input)
		stream.Close(result.Messages, err)
	}()
	return stream
}

func AgentLoop(ctx context.Context, prompts []Message, input ProviderLoopInput) *AgentEventStream {
	messages, err := messagesToAI(prompts)
	stream := CreateAgentEventStream()
	if err != nil {
		go stream.Close(nil, err)
		return stream
	}
	input.PromptMessages = messages
	return RunProviderLoopStream(ctx, input)
}

func AgentLoopContinue(ctx context.Context, input ProviderLoopInput) *AgentEventStream {
	return RunProviderLoopStream(ctx, input)
}

func messagesToAI(messages []Message) ([]ai.Message, error) {
	out := sessionMessagesToAI(messages)
	if len(out) != len(messages) {
		return nil, fmt.Errorf("prompt messages must be convertible to LLM messages")
	}
	return out, nil
}
