package mcpadapter

import "strings"

type ACPServer struct {
	Type    string      `json:"type,omitempty"`
	Name    string      `json:"name"`
	Command string      `json:"command,omitempty"`
	Args    []string    `json:"args,omitempty"`
	Env     []NameValue `json:"env,omitempty"`
	URL     string      `json:"url,omitempty"`
	Headers []NameValue `json:"headers,omitempty"`
}

type NameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func ConfigFromACPServers(servers []ACPServer) Config {
	cfg := Config{Servers: map[string]ServerConfig{}}
	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			continue
		}
		serverType := strings.TrimSpace(server.Type)
		if serverType == "" {
			serverType = "stdio"
		}
		mapped := ServerConfig{Type: serverType}
		switch serverType {
		case "stdio":
			mapped.Command = server.Command
			mapped.Args = append([]string(nil), server.Args...)
			mapped.Env = nameValuesToMap(server.Env)
		case "http", "sse":
			mapped.URL = server.URL
			mapped.Headers = nameValuesToMap(server.Headers)
		default:
			continue
		}
		cfg.Servers[name] = mapped
	}
	return cfg
}

func nameValuesToMap(values []NameValue) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, item := range values {
		out[item.Name] = item.Value
	}
	return out
}
