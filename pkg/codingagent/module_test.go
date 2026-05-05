package codingagent

import (
	"context"
	"fmt"
	"testing"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

func TestRegisterModuleRejectsDuplicateID(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	module := sessionModule{id: "duplicate", register: func(*ModuleRegistry) error { return nil }}
	if err := session.RegisterModule(module); err != nil {
		t.Fatalf("register module: %v", err)
	}
	if err := session.RegisterModule(module); err == nil {
		t.Fatal("expected duplicate module id error")
	}
}

func TestRegisterModuleRollsBackPartialRegistrationOnError(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	err := session.RegisterModule(sessionModule{
		id: "broken",
		register: func(registry *ModuleRegistry) error {
			registry.RegisterToolProvider(func(*Session) ([]agentcore.Tool, []ai.Tool) {
				return []agentcore.Tool{{Name: "broken_tool"}}, []ai.Tool{{Name: "broken_tool"}}
			})
			if err := registry.RegisterConfigOption(ModuleConfigOption{ID: "broken_config"}); err != nil {
				return err
			}
			if err := registry.RegisterRPCHandler("broken_rpc", func(_ context.Context, _ *Session, command rpcCommand) rpcResponse {
				return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}
			}); err != nil {
				return err
			}
			if err := registry.RegisterSessionEntryHandler("broken_entry", ModuleSessionEntryHandler{VisibleInTree: false}); err != nil {
				return err
			}
			return fmt.Errorf("boom")
		},
	})
	if err == nil {
		t.Fatal("expected module registration error")
	}
	if containsString(specNames(session.toolSpecs()), "broken_tool") {
		t.Fatalf("failed module left tool registered: %#v", specNames(session.toolSpecs()))
	}
	if _, ok := session.ConfigOption("broken_config"); ok {
		t.Fatal("failed module left config option registered")
	}
	if _, ok := session.ensureModuleRegistry().RPCHandler("broken_rpc"); ok {
		t.Fatal("failed module left rpc handler registered")
	}
	if _, ok := session.ensureModuleRegistry().SessionEntryHandler("broken_entry"); ok {
		t.Fatal("failed module left session entry handler registered")
	}
	if err := session.RegisterModule(sessionModule{id: "broken", register: func(*ModuleRegistry) error { return nil }}); err != nil {
		t.Fatalf("module id was not reusable after rollback: %v", err)
	}
}

func TestModuleToolProviderPersistsAcrossNewSession(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	err := session.RegisterModule(sessionModule{
		id: "custom_tool",
		register: func(registry *ModuleRegistry) error {
			registry.RegisterToolProvider(func(*Session) ([]agentcore.Tool, []ai.Tool) {
				return []agentcore.Tool{{Name: "custom_tool"}}, []ai.Tool{{Name: "custom_tool"}}
			})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("register module: %v", err)
	}
	session.NewSession()
	if !containsString(specNames(session.toolSpecs()), "custom_tool") {
		t.Fatalf("custom module tool was not preserved after NewSession: %#v", specNames(session.toolSpecs()))
	}
}

func TestModuleConfigOptionDrivesRPCHandler(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if _, ok := session.ConfigOption("command_compression"); !ok {
		t.Fatal("expected command compression config option")
	}
	response := (&RPCServer{Session: session}).handle(context.Background(), rpcCommand{
		ID:   "1",
		Type: "set_command_compression",
		Mode: CommandCompressionOff,
	})
	if !response.Success {
		t.Fatalf("rpc failed: %s", response.Error)
	}
	if session.GetCommandCompression().Mode != CommandCompressionOff {
		t.Fatalf("unexpected compression mode: %s", session.GetCommandCompression().Mode)
	}
}

func TestModuleSessionEntryHandlerControlsMetadataLeafAndTree(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	applied := 0
	err := session.RegisterModule(sessionModule{
		id: "audit_entries",
		register: func(registry *ModuleRegistry) error {
			return registry.RegisterSessionEntryHandler("audit", ModuleSessionEntryHandler{
				VisibleInTree: false,
				AffectsLeaf:   false,
				Apply: func(*Session, SessionEntry) {
					applied++
				},
				ApplyAfterBranch: func(session *Session, entry SessionEntry, _ map[string]struct{}) {
					applied++
				},
			})
		},
	})
	if err != nil {
		t.Fatalf("register module: %v", err)
	}
	if err := session.appendEntry(SessionEntry{Type: "message", Message: agentcore.Message{"role": "user", "text": "hi"}}); err != nil {
		t.Fatalf("append message: %v", err)
	}
	leafID := session.leafID
	if err := session.appendMetadataEntry(SessionEntry{Type: "audit"}); err != nil {
		t.Fatalf("append audit metadata: %v", err)
	}
	if session.leafID != leafID {
		t.Fatalf("metadata entry advanced leaf: got %q want %q", session.leafID, leafID)
	}
	if len(session.Tree()) != 1 {
		t.Fatalf("metadata entry appeared in tree: %#v", session.Tree())
	}
	if applied != 1 {
		t.Fatalf("metadata handler applied %d times, want 1", applied)
	}
}
