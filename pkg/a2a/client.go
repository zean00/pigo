package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type Client struct {
	Agent     RemoteAgent
	HTTP      *http.Client
	MaxBytes  int64
	idCounter atomic.Int64
}

func NewClient(agent RemoteAgent, timeout time.Duration, maxBytes int) *Client {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if maxBytes <= 0 {
		maxBytes = 2 << 20
	}
	return &Client{
		Agent:    agent,
		HTTP:     &http.Client{Timeout: timeout},
		MaxBytes: int64(maxBytes),
	}
}

func (c *Client) FetchAgentCard(ctx context.Context) (AgentCard, error) {
	url := strings.TrimSpace(c.Agent.CardURL)
	if url == "" && c.Agent.URL != "" {
		url = cardURLFromEndpoint(c.Agent.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return AgentCard{}, err
	}
	c.applyHeaders(req)
	resp, err := c.http().Do(req)
	if err != nil {
		return AgentCard{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AgentCard{}, fmt.Errorf("fetch agent card: HTTP %d", resp.StatusCode)
	}
	var card AgentCard
	if err := json.NewDecoder(io.LimitReader(resp.Body, c.MaxBytes)).Decode(&card); err != nil {
		return AgentCard{}, err
	}
	return card, nil
}

func (c *Client) SendMessage(ctx context.Context, params MessageSendParams) (Task, error) {
	var task Task
	if err := c.call(ctx, "message/send", params, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) GetTask(ctx context.Context, id string) (Task, error) {
	var task Task
	if err := c.call(ctx, "tasks/get", TaskQueryParams{ID: id}, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) CancelTask(ctx context.Context, id string) (Task, error) {
	var task Task
	if err := c.call(ctx, "tasks/cancel", TaskIDParams{ID: id}, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) StreamMessage(ctx context.Context, params MessageSendParams, onEvent func(any)) (Task, error) {
	endpoint, err := c.endpoint(ctx)
	if err != nil {
		return Task{}, err
	}
	reqBody, err := c.requestBody("message/stream", params)
	if err != nil {
		return Task{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return Task{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.applyHeaders(req)
	resp, err := c.http().Do(req)
	if err != nil {
		return Task{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Task{}, fmt.Errorf("message/stream: HTTP %d", resp.StatusCode)
	}
	var last Task
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, c.MaxBytes))
	scanner.Buffer(make([]byte, 0, 64*1024), int(c.MaxBytes))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var response JSONRPCResponse
		if err := json.Unmarshal([]byte(payload), &response); err != nil {
			return Task{}, err
		}
		if response.Error != nil {
			return Task{}, fmt.Errorf("a2a stream error %d: %s", response.Error.Code, response.Error.Message)
		}
		result := response.Result
		if raw, err := json.Marshal(result); err == nil {
			last = applyStreamResult(last, raw)
		}
		if onEvent != nil {
			onEvent(result)
		}
	}
	if err := scanner.Err(); err != nil {
		return Task{}, err
	}
	if last.ID == "" {
		return Task{}, fmt.Errorf("message/stream returned no task")
	}
	return last, nil
}

func applyStreamResult(task Task, raw []byte) Task {
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return task
	}
	switch discriminator.Kind {
	case "task":
		var streamed Task
		if err := json.Unmarshal(raw, &streamed); err == nil && streamed.ID != "" {
			return streamed
		}
	case "status-update":
		var update TaskStatusUpdateEvent
		if err := json.Unmarshal(raw, &update); err == nil && update.TaskID != "" {
			if task.ID == "" {
				task = Task{Kind: "task", ID: update.TaskID, ContextID: update.ContextID}
			}
			task.Status = update.Status
			if task.ContextID == "" {
				task.ContextID = update.ContextID
			}
		}
	case "artifact-update":
		var update TaskArtifactUpdateEvent
		if err := json.Unmarshal(raw, &update); err == nil && update.TaskID != "" {
			if task.ID == "" {
				task = Task{Kind: "task", ID: update.TaskID, ContextID: update.ContextID}
			}
			if task.ContextID == "" {
				task.ContextID = update.ContextID
			}
			if update.Append {
				task.Artifacts = appendOrMergeArtifact(task.Artifacts, update.Artifact)
			} else {
				task.Artifacts = replaceOrAppendArtifact(task.Artifacts, update.Artifact)
			}
		}
	case "message":
		var message Message
		if err := json.Unmarshal(raw, &message); err == nil && message.MessageID != "" {
			task = taskFromMessage(task, message)
		}
	}
	return task
}

func appendOrMergeArtifact(artifacts []Artifact, artifact Artifact) []Artifact {
	for i := range artifacts {
		if artifacts[i].ArtifactID == artifact.ArtifactID && artifact.ArtifactID != "" {
			artifacts[i].Parts = append(artifacts[i].Parts, artifact.Parts...)
			if artifact.Name != "" {
				artifacts[i].Name = artifact.Name
			}
			if artifact.Description != "" {
				artifacts[i].Description = artifact.Description
			}
			if artifact.Metadata != nil {
				artifacts[i].Metadata = artifact.Metadata
			}
			return artifacts
		}
	}
	return append(artifacts, artifact)
}

func replaceOrAppendArtifact(artifacts []Artifact, artifact Artifact) []Artifact {
	for i := range artifacts {
		if artifacts[i].ArtifactID == artifact.ArtifactID && artifact.ArtifactID != "" {
			artifacts[i] = artifact
			return artifacts
		}
	}
	return append(artifacts, artifact)
}

func taskFromMessage(task Task, message Message) Task {
	if task.ID == "" {
		task.ID = message.TaskID
	}
	if task.ContextID == "" {
		task.ContextID = message.ContextID
	}
	if task.Kind == "" {
		task.Kind = "task"
	}
	task.Status = TaskStatus{State: TaskStateCompleted, Message: &message}
	task.History = append(task.History, message)
	return task
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	endpoint, err := c.endpoint(ctx)
	if err != nil {
		return err
	}
	reqBody, err := c.requestBody(method, params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	c.applyHeaders(req)
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	var response JSONRPCResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, c.MaxBytes)).Decode(&response); err != nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf("a2a error %d: %s", response.Error.Code, response.Error.Message)
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (c *Client) endpoint(ctx context.Context) (string, error) {
	if strings.TrimSpace(c.Agent.URL) != "" {
		return c.Agent.URL, nil
	}
	card, err := c.FetchAgentCard(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(card.URL) == "" {
		return "", fmt.Errorf("agent card missing url")
	}
	return card.URL, nil
}

func (c *Client) requestBody(method string, params any) ([]byte, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.idCounter.Add(1),
		Method:  method,
		Params:  raw,
	})
}

func (c *Client) applyHeaders(req *http.Request) {
	for key, value := range c.Agent.Headers {
		if strings.TrimSpace(key) != "" && value != "" {
			req.Header.Set(key, value)
		}
	}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
