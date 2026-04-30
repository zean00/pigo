package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/badlogic/pigo/pkg/ai"
)

type parityReport struct {
	OK      bool              `json:"ok"`
	Cases   []caseReport      `json:"cases"`
	Live    []liveSmokeReport `json:"live,omitempty"`
	Summary map[string]int    `json:"summary"`
	Errors  []string          `json:"errors,omitempty"`
}

type caseReport struct {
	Case        string   `json:"case"`
	Kind        string   `json:"kind"`
	OK          bool     `json:"ok"`
	Differences []string `json:"differences,omitempty"`
}

type liveSmokeReport struct {
	Implementation string   `json:"implementation"`
	Provider       string   `json:"provider"`
	Model          string   `json:"model"`
	OK             bool     `json:"ok"`
	StopReason     string   `json:"stopReason,omitempty"`
	TextContains   bool     `json:"textContains"`
	EventTypes     []string `json:"eventTypes,omitempty"`
	Error          string   `json:"error,omitempty"`
}

func main() {
	root := flag.String("root", ".", "pigo repository root")
	piMono := flag.String("pi-mono", "../pi-mono", "pi-mono repository path")
	casesDir := flag.String("cases", "testdata/conformance", "conformance fixture directory")
	liveOpenRouter := flag.Bool("live-openrouter", false, "run a live OpenRouter smoke check using OPENROUTER_API_KEY")
	liveModel := flag.String("live-model", "openai/gpt-4o-mini", "OpenRouter model for live smoke")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pigoRoot, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}
	piMonoRoot, err := filepath.Abs(*piMono)
	if err != nil {
		log.Fatal(err)
	}
	fixtureDir := *casesDir
	if !filepath.IsAbs(fixtureDir) {
		fixtureDir = filepath.Join(pigoRoot, fixtureDir)
	}

	report := parityReport{OK: true, Summary: map[string]int{}}
	casePaths, err := fixturePaths(fixtureDir)
	if err != nil {
		log.Fatal(err)
	}
	for _, path := range casePaths {
		result := runCase(ctx, pigoRoot, piMonoRoot, path)
		report.Cases = append(report.Cases, result)
		report.Summary[result.Kind]++
		if !result.OK {
			report.OK = false
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %s", result.Case, strings.Join(result.Differences, "; ")))
		}
	}

	if *liveOpenRouter {
		live := runOpenRouterSmoke(ctx, pigoRoot, piMonoRoot, *liveModel)
		report.Live = append(report.Live, live...)
		for _, item := range live {
			if !item.OK {
				report.OK = false
				report.Errors = append(report.Errors, fmt.Sprintf("live %s: %s", item.Implementation, item.Error))
			}
		}
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
	if !report.OK {
		os.Exit(1)
	}
}

func fixturePaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

func runCase(ctx context.Context, pigoRoot, piMonoRoot, path string) caseReport {
	kind, name, err := classifyFixture(path)
	if err != nil {
		return caseReport{Case: filepath.Base(path), Kind: "unknown", OK: false, Differences: []string{err.Error()}}
	}
	goOutput, err := runJSONCommand(ctx, pigoRoot, goRunner(kind), "--case", path)
	if err != nil {
		return caseReport{Case: name, Kind: kind, OK: false, Differences: []string{"pigo run failed: " + err.Error()}}
	}
	if err := runVerifier(ctx, piMonoRoot, kind, path, goOutput); err != nil {
		return caseReport{Case: name, Kind: kind, OK: false, Differences: []string{"pi-mono verifier rejected pigo output: " + err.Error()}}
	}
	if kind == "ai" {
		return caseReport{Case: name, Kind: kind, OK: true}
	}
	tsOutput, err := runJSONCommand(ctx, piMonoRoot, "npm", "exec", "--", "tsx", tsRunner(kind), "--case", path)
	if err != nil {
		return caseReport{Case: name, Kind: kind, OK: false, Differences: []string{"pi-mono run failed: " + err.Error()}}
	}

	goCanon := canonicalOutput(kind, goOutput)
	tsCanon := canonicalOutput(kind, tsOutput)
	if reflect.DeepEqual(goCanon, tsCanon) {
		return caseReport{Case: name, Kind: kind, OK: true}
	}
	return caseReport{
		Case: name,
		Kind: kind,
		OK:   false,
		Differences: []string{
			"canonical output mismatch",
			firstJSONDiff(goCanon, tsCanon),
		},
	}
}

func runVerifier(ctx context.Context, piMonoRoot, kind, casePath string, output map[string]any) error {
	tmp, err := os.CreateTemp("", "pigo-parity-output-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	verifier := "packages/ai-conformance/src/verify-cli.ts"
	if kind == "agent" {
		verifier = "packages/ai-conformance/src/agent-verify-cli.ts"
	}
	if kind == "coding-agent" {
		verifier = "packages/ai-conformance/src/coding-agent-verify-cli.ts"
	}
	cmd := exec.CommandContext(ctx, "npm", "exec", "--", "tsx", verifier, "--case", casePath, "--output", tmp.Name())
	cmd.Dir = piMonoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func classifyFixture(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", "", err
	}
	name, _ := raw["name"].(string)
	if _, ok := raw["assistantTurns"]; !ok {
		return "ai", name, nil
	}
	prompts, _ := raw["prompts"].([]any)
	if len(prompts) > 0 {
		if _, ok := prompts[0].(string); ok {
			return "coding-agent", name, nil
		}
	}
	return "agent", name, nil
}

func goRunner(kind string) string {
	switch kind {
	case "agent":
		return "./cmd/pigo-agent-conformance"
	case "coding-agent":
		return "./cmd/pigo-coding-agent-conformance"
	default:
		return "./cmd/pigo-ai-conformance"
	}
}

func tsRunner(kind string) string {
	switch kind {
	case "agent":
		return "packages/ai-conformance/src/agent-cli.ts"
	case "coding-agent":
		return "packages/ai-conformance/src/coding-agent-cli.ts"
	default:
		return "packages/ai-conformance/src/cli.ts"
	}
}

func runJSONCommand(ctx context.Context, dir, name string, args ...string) (map[string]any, error) {
	commandArgs := args
	commandName := name
	if strings.HasPrefix(name, "./cmd/") {
		commandName = "go"
		commandArgs = append([]string{"run", name}, args...)
	}
	cmd := exec.CommandContext(ctx, commandName, commandArgs...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	return decoded, nil
}

func canonicalOutput(kind string, output map[string]any) map[string]any {
	out := map[string]any{
		"case":  output["case"],
		"model": output["model"],
	}
	switch kind {
	case "ai":
		out["events"] = canonicalAssistantEvents(asSlice(output["events"]))
		out["result"] = canonicalMessage(output["result"])
	case "agent":
		out["messages"] = canonicalMessages(asSlice(output["messages"]))
	case "coding-agent":
		out["messages"] = canonicalMessages(asSlice(output["messages"]))
		out["sessionEntryTypes"] = output["sessionEntryTypes"]
		out["files"] = output["files"]
	}
	return out
}

func canonicalAssistantEvents(events []any) []any {
	out := []any{}
	for _, event := range events {
		object := asMap(event)
		item := map[string]any{"type": object["type"]}
		copyIfPresent(item, object, "contentIndex")
		copyIfPresent(item, object, "reason")
		if tool := asMap(object["toolCall"]); len(tool) > 0 {
			item["toolCall"] = canonicalContentBlock(tool)
		}
		out = append(out, item)
	}
	return out
}

func canonicalAgentEvents(kind string, events []any) []any {
	out := []any{}
	for _, event := range events {
		object := asMap(event)
		eventType, _ := object["type"].(string)
		if kind == "coding-agent" && strings.HasPrefix(eventType, "session_") {
			continue
		}
		if eventType == "tool_execution_update" {
			continue
		}
		item := map[string]any{"type": eventType}
		assistantEventType, _ := object["assistantEventType"].(string)
		if assistantEventType == "start" {
			continue
		}
		if assistantEventType != "" {
			item["assistantEventType"] = assistantEventType
		}
		copyIfPresent(item, object, "toolName")
		copyIfPresent(item, object, "toolCallId")
		copyIfPresent(item, object, "messageRole")
		copyIfPresent(item, object, "toolResultCount")
		copyIfPresent(item, object, "isError")
		out = append(out, item)
	}
	return out
}

func canonicalMessages(messages []any) []any {
	out := make([]any, 0, len(messages))
	for _, message := range messages {
		out = append(out, canonicalMessage(message))
	}
	return out
}

func canonicalMessage(value any) map[string]any {
	object := asMap(value)
	if len(object) == 0 {
		return nil
	}
	role, _ := object["role"].(string)
	out := map[string]any{"role": role}
	copyIfPresent(out, object, "stopReason")
	if text, ok := object["text"].(string); ok {
		out["text"] = canonicalText(text)
	}
	copyIfPresent(out, object, "toolCallId")
	copyIfPresent(out, object, "toolName")
	copyIfPresent(out, object, "isError")
	if content := asSlice(object["content"]); content != nil {
		blocks := make([]any, 0, len(content))
		for _, block := range content {
			blocks = append(blocks, canonicalContentBlock(asMap(block)))
		}
		out["content"] = blocks
	}
	return out
}

func canonicalText(text string) string {
	text = strings.TrimRight(text, "\n")
	if strings.Contains(text, "no such file or directory") {
		return "ENOENT"
	}
	return text
}

func canonicalContentBlock(block map[string]any) map[string]any {
	out := map[string]any{}
	copyIfPresent(out, block, "type")
	copyIfPresent(out, block, "text")
	copyIfPresent(out, block, "thinking")
	copyIfPresent(out, block, "name")
	copyIfPresent(out, block, "arguments")
	copyIfPresent(out, block, "hasId")
	copyIfPresent(out, block, "mimeType")
	if data, ok := block["data"].(string); ok {
		out["dataLength"] = len(data)
	}
	return out
}

func copyIfPresent(dst, src map[string]any, key string) {
	if value, ok := src[key]; ok && value != nil {
		dst[key] = value
	}
}

func asMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return nil
}

func asSlice(value any) []any {
	if value == nil {
		return nil
	}
	if slice, ok := value.([]any); ok {
		return slice
	}
	return nil
}

func firstJSONDiff(left, right any) string {
	leftData, _ := json.MarshalIndent(left, "", "  ")
	rightData, _ := json.MarshalIndent(right, "", "  ")
	if bytes.Equal(leftData, rightData) {
		return "values differ but JSON rendering matched"
	}
	leftLines := strings.Split(string(leftData), "\n")
	rightLines := strings.Split(string(rightData), "\n")
	limit := len(leftLines)
	if len(rightLines) < limit {
		limit = len(rightLines)
	}
	for i := 0; i < limit; i++ {
		if leftLines[i] != rightLines[i] {
			return fmt.Sprintf("first difference at line %d: pigo=%q pi-mono=%q", i+1, leftLines[i], rightLines[i])
		}
	}
	return fmt.Sprintf("length differs: pigo=%d lines pi-mono=%d lines", len(leftLines), len(rightLines))
}

func runOpenRouterSmoke(ctx context.Context, pigoRoot, piMonoRoot, model string) []liveSmokeReport {
	const provider = "openrouter"
	const sentinel = "pigo-parity-smoke-0426"
	if strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) == "" {
		return []liveSmokeReport{{
			Implementation: "openrouter",
			Provider:       provider,
			Model:          model,
			OK:             false,
			Error:          "OPENROUTER_API_KEY is not set",
		}}
	}
	return []liveSmokeReport{
		runPigoOpenRouterSmoke(ctx, provider, model, sentinel),
		runPiMonoOpenRouterSmoke(ctx, piMonoRoot, provider, model, sentinel),
	}
}

func runPigoOpenRouterSmoke(ctx context.Context, provider, model, sentinel string) liveSmokeReport {
	result, events, err := ai.Complete(ctx, ai.CompletionRequest{
		Provider: provider,
		Model:    model,
		Messages: []ai.Message{{
			Role:    "user",
			Content: "Reply with exactly: " + sentinel,
		}},
		Options: ai.ChatOptions{Stream: true, MaxTokens: 32},
	})
	report := liveSmokeReport{
		Implementation: "pigo",
		Provider:       provider,
		Model:          model,
		StopReason:     result.StopReason,
		TextContains:   strings.Contains(result.Text, sentinel),
		EventTypes:     eventTypes(events),
	}
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.OK = report.TextContains && result.StopReason != "error"
	if !report.OK && report.Error == "" {
		report.Error = "response did not contain sentinel"
	}
	return report
}

func runPiMonoOpenRouterSmoke(ctx context.Context, piMonoRoot, provider, model, sentinel string) liveSmokeReport {
	tmp, err := os.CreateTemp("", "pigo-openrouter-smoke-*.json")
	if err != nil {
		return liveSmokeReport{Implementation: "pi-mono", Provider: provider, Model: model, Error: err.Error()}
	}
	defer os.Remove(tmp.Name())
	if err := json.NewEncoder(tmp).Encode(map[string]any{
		"name":  "openrouter_live_smoke",
		"model": map[string]any{"provider": provider, "id": model},
		"context": map[string]any{
			"messages": []map[string]any{{"role": "user", "content": "Reply with exactly: " + sentinel}},
		},
		"options": map[string]any{"maxTokens": 32},
		"expect":  map[string]any{"textContains": []string{sentinel}, "usage": "presentOrError"},
	}); err != nil {
		return liveSmokeReport{Implementation: "pi-mono", Provider: provider, Model: model, Error: err.Error()}
	}
	if err := tmp.Close(); err != nil {
		return liveSmokeReport{Implementation: "pi-mono", Provider: provider, Model: model, Error: err.Error()}
	}
	output, err := runJSONCommand(ctx, piMonoRoot, "npm", "exec", "--", "tsx", "packages/ai-conformance/src/cli.ts", "--case", tmp.Name())
	report := liveSmokeReport{Implementation: "pi-mono", Provider: provider, Model: model}
	if err != nil {
		report.Error = err.Error()
		return report
	}
	result := asMap(output["result"])
	report.StopReason, _ = result["stopReason"].(string)
	text, _ := result["text"].(string)
	report.TextContains = strings.Contains(text, sentinel)
	report.EventTypes = eventTypesFromAny(asSlice(output["events"]))
	report.OK = report.TextContains && report.StopReason != "error"
	if !report.OK {
		report.Error = "response did not contain sentinel"
	}
	return report
}

func eventTypes(events []ai.NormalizedEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Type)
	}
	return out
}

func eventTypesFromAny(events []any) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		eventType, _ := asMap(event)["type"].(string)
		if eventType != "" {
			out = append(out, eventType)
		}
	}
	return out
}
