package codingagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

type SessionEntry struct {
	Type             string               `json:"type"`
	Version          int                  `json:"version,omitempty"`
	ID               string               `json:"id,omitempty"`
	ParentID         string               `json:"parentId,omitempty"`
	Timestamp        string               `json:"timestamp"`
	CWD              string               `json:"cwd,omitempty"`
	ParentSession    string               `json:"parentSession,omitempty"`
	Message          agentcore.Message    `json:"message,omitempty"`
	Provider         string               `json:"provider,omitempty"`
	ModelID          string               `json:"modelId,omitempty"`
	Level            string               `json:"level,omitempty"`
	Name             string               `json:"name,omitempty"`
	UserText         string               `json:"userText,omitempty"`
	TargetID         string               `json:"targetId,omitempty"`
	Label            string               `json:"label,omitempty"`
	CustomType       string               `json:"customType,omitempty"`
	Data             any                  `json:"data,omitempty"`
	Summary          string               `json:"summary,omitempty"`
	FromID           string               `json:"fromId,omitempty"`
	FirstKeptEntryID string               `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int                  `json:"tokensBefore,omitempty"`
	OAuthProvider    string               `json:"oauthProvider,omitempty"`
	OAuthCredentials *ai.OAuthCredentials `json:"-"`
	UsageLedger      *UsageLedgerEntry    `json:"usageLedger,omitempty"`
	UsageQuota       *UsageQuotaConfig    `json:"usageQuota,omitempty"`
}

type SessionStore struct {
	Path string
}

func NewSessionStore(path string) *SessionStore {
	return &SessionStore{Path: path}
}

func (s *SessionStore) Append(entry SessionEntry) error {
	if s == nil || s.Path == "" {
		return nil
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	entry.OAuthCredentials = nil
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(s.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *SessionStore) ReadEntries() ([]SessionEntry, error) {
	if s == nil || s.Path == "" {
		return nil, nil
	}
	file, err := os.Open(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	entries := []SessionEntry{}
	scanner := bufio.NewScanner(file)
	for i := 1; scanner.Scan(); i++ {
		rawLine := strings.TrimSpace(scanner.Text())
		if rawLine == "" {
			continue
		}
		var entry SessionEntry
		if err := json.Unmarshal([]byte(rawLine), &entry); err != nil {
			return nil, fmt.Errorf("invalid session entry at line %d: %w", i, err)
		}
		if entry.Timestamp == "" {
			entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
