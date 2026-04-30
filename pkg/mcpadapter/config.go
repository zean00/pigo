package mcpadapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Servers map[string]ServerConfig `json:"servers,omitempty"`
}

type ServerConfig struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func LoadConfig(root string) (Config, string, error) {
	if value := strings.TrimSpace(os.Getenv("PI_MCP_CONFIG_JSON")); value != "" {
		cfg, err := ParseConfig([]byte(value))
		return cfg, "PI_MCP_CONFIG_JSON", err
	}
	if path := strings.TrimSpace(os.Getenv("PI_MCP_CONFIG")); path != "" {
		cfg, err := loadConfigFile(path)
		return cfg, path, err
	}
	candidates := []string{}
	if root != "" {
		candidates = append(candidates, filepath.Join(root, ".pi", "mcp.json"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".pi", "agent", "mcp.json"))
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			cfg, err := loadConfigFile(path)
			return cfg, path, err
		}
	}
	return Config{}, "", nil
}

func loadConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseConfig(data)
}

func ParseConfig(data []byte) (Config, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, err
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return Config{}, fmt.Errorf("MCP config must be an object")
	}
	serversRaw, ok := obj["servers"]
	if !ok || serversRaw == nil {
		return Config{}, nil
	}
	serversObj, ok := serversRaw.(map[string]any)
	if !ok {
		return Config{}, fmt.Errorf("MCP config servers must be an object")
	}
	cfg := Config{Servers: map[string]ServerConfig{}}
	for name, value := range serversObj {
		serverObj, ok := value.(map[string]any)
		if !ok {
			return Config{}, fmt.Errorf("MCP server %q must be an object", name)
		}
		server, err := parseServerConfig(name, serverObj)
		if err != nil {
			return Config{}, err
		}
		cfg.Servers[name] = server
	}
	return cfg, nil
}

func parseServerConfig(name string, obj map[string]any) (ServerConfig, error) {
	serverType := strings.TrimSpace(asString(obj["type"]))
	if serverType == "" {
		serverType = "stdio"
	}
	server := ServerConfig{Type: serverType}
	switch serverType {
	case "stdio":
		server.Command = strings.TrimSpace(asString(obj["command"]))
		if server.Command == "" {
			return ServerConfig{}, fmt.Errorf("MCP stdio server %q command must be a non-empty string", name)
		}
		args, err := optionalStringSlice(obj["args"], "args", name)
		if err != nil {
			return ServerConfig{}, err
		}
		env, err := optionalStringMap(obj["env"], "env", name)
		if err != nil {
			return ServerConfig{}, err
		}
		cwd := strings.TrimSpace(asString(obj["cwd"]))
		if _, ok := obj["cwd"]; ok && cwd == "" {
			return ServerConfig{}, fmt.Errorf("MCP stdio server %q cwd must be a string", name)
		}
		server.Args = args
		server.Env = env
		server.Cwd = cwd
	case "http", "sse":
		server.URL = strings.TrimSpace(asString(obj["url"]))
		if server.URL == "" {
			return ServerConfig{}, fmt.Errorf("MCP %s server %q url must be a non-empty string", serverType, name)
		}
		headers, err := optionalStringMap(obj["headers"], "headers", name)
		if err != nil {
			return ServerConfig{}, err
		}
		server.Headers = headers
	default:
		return ServerConfig{}, fmt.Errorf("MCP server %q has unsupported type %q", name, serverType)
	}
	return server, nil
}

func optionalStringSlice(value any, field, server string) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("MCP server %q %s must be an array of strings", server, field)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("MCP server %q %s must be an array of strings", server, field)
		}
		out = append(out, text)
	}
	return out, nil
}

func optionalStringMap(value any, field, server string) (map[string]string, error) {
	if value == nil {
		return nil, nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("MCP server %q %s must be an object with string values", server, field)
	}
	out := map[string]string{}
	for key, raw := range obj {
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("MCP server %q %s must be an object with string values", server, field)
		}
		out[key] = text
	}
	return out, nil
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
