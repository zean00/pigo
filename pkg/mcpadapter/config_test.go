package mcpadapter

import "testing"

func TestParseConfigSupportsTransports(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"servers": {
			"local": {"type":"stdio","command":"node","args":["server.js"],"env":{"A":"B"},"cwd":"/tmp"},
			"http": {"type":"http","url":"http://example.test/mcp","headers":{"Authorization":"Bearer x"}},
			"sse": {"type":"sse","url":"http://example.test/sse"}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Servers["local"].Command != "node" || cfg.Servers["local"].Args[0] != "server.js" || cfg.Servers["local"].Env["A"] != "B" {
		t.Fatalf("stdio config = %#v", cfg.Servers["local"])
	}
	if cfg.Servers["http"].Headers["Authorization"] != "Bearer x" {
		t.Fatalf("http config = %#v", cfg.Servers["http"])
	}
	if cfg.Servers["sse"].URL == "" {
		t.Fatalf("sse config = %#v", cfg.Servers["sse"])
	}
}

func TestMakeToolNameSanitizesAndHashes(t *testing.T) {
	if got := MakeToolName("my server", "read:file"); got != "mcp__my_server__read_file" {
		t.Fatalf("tool name = %q", got)
	}
	got := MakeToolName("server with a very very very very long name", "tool with a very very very very very very long name")
	if len(got) > 64 {
		t.Fatalf("hashed tool name too long: %d %q", len(got), got)
	}
	if got[:5] != "mcp__" {
		t.Fatalf("hashed tool name = %q", got)
	}
}

func TestConfigFromACPServers(t *testing.T) {
	cfg := ConfigFromACPServers([]ACPServer{{
		Name:    "local",
		Command: "cmd",
		Args:    []string{"a"},
		Env:     []NameValue{{Name: "K", Value: "V"}},
	}, {
		Type:    "http",
		Name:    "remote",
		URL:     "http://example.test",
		Headers: []NameValue{{Name: "H", Value: "V"}},
	}})
	if cfg.Servers["local"].Type != "stdio" || cfg.Servers["local"].Env["K"] != "V" {
		t.Fatalf("local = %#v", cfg.Servers["local"])
	}
	if cfg.Servers["remote"].Headers["H"] != "V" {
		t.Fatalf("remote = %#v", cfg.Servers["remote"])
	}
}
