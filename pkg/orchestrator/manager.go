package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/badlogic/pigo/pkg/a2a"
)

type StoreFunc func(Run) error

type Manager struct {
	Config Config
	A2A    a2a.Config
	Agents []Agent
	Store  StoreFunc

	mu      sync.Mutex
	runs    map[string]Run
	cancels map[string]context.CancelFunc
}

func NewManager(config Config, a2aConfig a2a.Config, agents []Agent, store StoreFunc) *Manager {
	return &Manager{
		Config:  config.Normalized(),
		A2A:     a2aConfig.Normalized(),
		Agents:  agents,
		Store:   store,
		runs:    map[string]Run{},
		cancels: map[string]context.CancelFunc{},
	}
}

func (m *Manager) Delegate(ctx context.Context, req RunRequest) (Run, error) {
	if req.Goal == "" {
		req.Goal = reqMessage(req)
	}
	if len(req.Steps) == 0 {
		req.Steps = []TaskSpec{{Agent: req.Agent, Skill: req.Skill, Message: req.Goal}}
	}
	return m.Start(ctx, req)
}

func (m *Manager) Start(ctx context.Context, req RunRequest) (Run, error) {
	req.Goal = strings.TrimSpace(req.Goal)
	if req.Goal == "" {
		return Run{}, fmt.Errorf("missing orchestration goal")
	}
	config := m.configFor(req)
	run := Run{
		RunID:     a2a.NewID("orch"),
		Goal:      req.Goal,
		State:     StateRunning,
		CreatedAt: now(),
		UpdatedAt: now(),
		Tasks:     buildTasks(req),
	}
	if len(run.Tasks) == 0 {
		run.Tasks = []Task{{TaskID: "task-1", Message: req.Goal, Agent: req.Agent, Skill: req.Skill, State: StatePending}}
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	m.setCancel(run.RunID, cancel)
	defer m.clearCancel(run.RunID)
	m.save(run)
	run = m.execute(ctx, run, config)
	run.UpdatedAt = now()
	if run.State == StateRunning {
		run.State = StateCompleted
	}
	run.Result = reduce(run)
	m.save(run)
	return run, nil
}

func (m *Manager) Get(runID string) (Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[strings.TrimSpace(runID)]
	return run, ok
}

func (m *Manager) Restore(run Run) {
	if strings.TrimSpace(run.RunID) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs == nil {
		m.runs = map[string]Run{}
	}
	m.runs[run.RunID] = run
}

func (m *Manager) List() []Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	runs := make([]Run, 0, len(m.runs))
	for _, run := range m.runs {
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt < runs[j].CreatedAt })
	return runs
}

func (m *Manager) Cancel(runID string) (Run, error) {
	runID = strings.TrimSpace(runID)
	m.mu.Lock()
	cancel := m.cancels[runID]
	run, ok := m.runs[runID]
	m.mu.Unlock()
	if !ok {
		return Run{}, fmt.Errorf("orchestration run %q not found", runID)
	}
	if cancel != nil {
		cancel()
	}
	run.State = StateCanceled
	run.UpdatedAt = now()
	m.save(run)
	return run, nil
}

func (m *Manager) execute(ctx context.Context, run Run, config Config) Run {
	for {
		if ctx.Err() != nil {
			run.State = StateCanceled
			run.Errors = append(run.Errors, ctx.Err().Error())
			return run
		}
		ready := readyTaskIndexes(run.Tasks)
		if len(ready) == 0 {
			break
		}
		if len(ready) > config.MaxParallel {
			ready = ready[:config.MaxParallel]
		}
		results := make(chan taskResult, len(ready))
		for _, idx := range ready {
			run.Tasks[idx].State = StateRunning
			go func(task Task) {
				results <- m.executeTask(ctx, task)
			}(run.Tasks[idx])
		}
		for range ready {
			result := <-results
			for i := range run.Tasks {
				if run.Tasks[i].TaskID != result.Task.TaskID {
					continue
				}
				run.Tasks[i] = result.Task
				if result.Artifact.ArtifactID != "" {
					run.Artifacts = append(run.Artifacts, result.Artifact)
					run.Tasks[i].ArtifactIDs = append(run.Tasks[i].ArtifactIDs, result.Artifact.ArtifactID)
				}
				if result.Task.Error != "" {
					run.Errors = append(run.Errors, result.Task.Error)
				}
			}
		}
		run.UpdatedAt = now()
		m.save(run)
	}
	for _, task := range run.Tasks {
		if task.State == StateFailed && !task.Optional {
			run.State = StateFailed
			return run
		}
		if task.State == StatePending || task.State == StateRunning {
			run.State = StateFailed
			run.Errors = append(run.Errors, "orchestration stalled with unresolved task "+task.TaskID)
			return run
		}
	}
	run.State = StateCompleted
	return run
}

type taskResult struct {
	Task     Task
	Artifact Artifact
}

func (m *Manager) executeTask(ctx context.Context, task Task) taskResult {
	agent, err := m.route(task)
	if err != nil {
		task.State = StateFailed
		task.Error = err.Error()
		if task.Optional {
			task.State = StateCompleted
		}
		return taskResult{Task: task}
	}
	task.Agent = agent.Name
	remote, ok := m.remoteAgent(agent.Name)
	if !ok {
		task.State = StateFailed
		task.Error = "missing A2A remote agent " + agent.Name
		if task.Optional {
			task.State = StateCompleted
		}
		return taskResult{Task: task}
	}
	client := a2a.NewClient(remote, m.A2A.Timeout, m.A2A.MaxResponseBytes)
	message := a2a.NewTextMessage(a2a.RoleUser, task.Message)
	if task.Skill != "" {
		message.Metadata = map[string]any{"skill": task.Skill}
	}
	blocking := true
	params := a2a.MessageSendParams{Message: message, Configuration: &a2a.MessageSendConfiguration{Blocking: &blocking}}
	card, err := client.FetchAgentCard(ctx)
	if err != nil {
		return failedTask(task, err, task.Optional)
	}
	var a2aTask a2a.Task
	if card.Capabilities.Streaming {
		a2aTask, err = client.StreamMessage(ctx, params, nil)
	} else {
		a2aTask, err = client.SendMessage(ctx, params)
	}
	if err != nil {
		return failedTask(task, err, task.Optional)
	}
	task.A2ATaskID = a2aTask.ID
	task.State = StateCompleted
	if a2aTask.Status.State == a2a.TaskStateFailed || a2aTask.Status.State == a2a.TaskStateCanceled || a2aTask.Status.State == a2a.TaskStateRejected {
		task.State = StateFailed
		task.Error = "A2A task finished with state " + a2aTask.Status.State
		if task.Optional {
			task.State = StateCompleted
		}
	}
	text := a2a.TaskText(a2aTask)
	artifact := Artifact{ArtifactID: a2a.NewID("artifact"), TaskID: task.TaskID, Agent: task.Agent, Text: text}
	return taskResult{Task: task, Artifact: artifact}
}

func failedTask(task Task, err error, optional bool) taskResult {
	task.State = StateFailed
	task.Error = err.Error()
	if optional {
		task.State = StateCompleted
	}
	return taskResult{Task: task}
}

func (m *Manager) route(task Task) (Agent, error) {
	if task.Agent != "" {
		name := a2a.ToolSafeName(task.Agent)
		if !m.agentAllowed(name) {
			return Agent{}, fmt.Errorf("A2A agent %q is not enabled for orchestration", name)
		}
		for _, agent := range m.Agents {
			if a2a.ToolSafeName(agent.Name) == name {
				return agent, nil
			}
		}
		return Agent{Name: name}, nil
	}
	text := strings.ToLower(task.Skill + " " + task.Message)
	for _, agent := range m.Agents {
		if !m.agentAllowed(agent.Name) {
			continue
		}
		if matchesAgent(agent, text) {
			return agent, nil
		}
	}
	for _, remote := range m.A2A.Agents {
		if !m.agentAllowed(remote.Name) {
			continue
		}
		return Agent{Name: remote.Name}, nil
	}
	return Agent{}, fmt.Errorf("no eligible A2A agent")
}

func (m *Manager) agentAllowed(name string) bool {
	allowed := m.Config.Normalized().Agents
	if len(allowed) == 0 {
		return true
	}
	name = a2a.ToolSafeName(name)
	for _, candidate := range allowed {
		if a2a.ToolSafeName(candidate) == name {
			return true
		}
	}
	return false
}

func matchesAgent(agent Agent, text string) bool {
	if text == "" {
		return true
	}
	values := append([]string{agent.Name, agent.Description}, agent.Skills...)
	values = append(values, agent.Tags...)
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func (m *Manager) remoteAgent(name string) (a2a.RemoteAgent, bool) {
	name = a2a.ToolSafeName(name)
	for _, agent := range m.A2A.Agents {
		if agent.Name == name {
			return agent, true
		}
	}
	return a2a.RemoteAgent{}, false
}

func (m *Manager) save(run Run) {
	m.mu.Lock()
	if m.runs == nil {
		m.runs = map[string]Run{}
	}
	m.runs[run.RunID] = run
	m.mu.Unlock()
	if m.Store != nil {
		_ = m.Store(run)
	}
}

func (m *Manager) setCancel(runID string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancels[runID] = cancel
}

func (m *Manager) clearCancel(runID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cancels, runID)
}

func (m *Manager) configFor(req RunRequest) Config {
	config := m.Config.Normalized()
	if req.MaxParallel > 0 {
		config.MaxParallel = req.MaxParallel
	}
	if req.TimeoutMillis > 0 {
		config.Timeout = time.Duration(req.TimeoutMillis) * time.Millisecond
		config.TimeoutMillis = req.TimeoutMillis
	}
	return config
}

func buildTasks(req RunRequest) []Task {
	out := []Task{}
	for i, step := range req.Steps {
		taskID := strings.TrimSpace(step.ID)
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", i+1)
		}
		out = append(out, Task{
			TaskID:    taskID,
			Agent:     step.Agent,
			Skill:     step.Skill,
			Message:   firstNonEmpty(step.Message, req.Goal),
			DependsOn: append([]string(nil), step.DependsOn...),
			Optional:  step.Optional,
			State:     StatePending,
		})
	}
	return out
}

func readyTaskIndexes(tasks []Task) []int {
	done := map[string]bool{}
	for _, task := range tasks {
		if task.State == StateCompleted || (task.State == StateFailed && task.Optional) {
			done[task.TaskID] = true
		}
	}
	out := []int{}
	for i, task := range tasks {
		if task.State != StatePending {
			continue
		}
		ready := true
		for _, dependency := range task.DependsOn {
			if !done[dependency] {
				ready = false
				break
			}
		}
		if ready {
			out = append(out, i)
		}
	}
	return out
}

func reduce(run Run) string {
	if len(run.Artifacts) == 0 {
		return strings.Join(run.Errors, "\n")
	}
	var b strings.Builder
	b.WriteString("# Orchestration Result\n\n")
	for _, artifact := range run.Artifacts {
		b.WriteString("## ")
		b.WriteString(firstNonEmpty(artifact.Agent, artifact.TaskID))
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(artifact.Text))
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func reqMessage(req RunRequest) string {
	for _, step := range req.Steps {
		if strings.TrimSpace(step.Message) != "" {
			return step.Message
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
