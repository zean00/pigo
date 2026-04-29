package ai

import "testing"

func TestProviderAuthInfoForProvider(t *testing.T) {
	info, ok := ProviderAuthInfoForProvider("anthropic")
	if !ok {
		t.Fatal("expected provider auth info")
	}
	if info.Provider != "anthropic" {
		t.Fatalf("provider = %q", info.Provider)
	}
	if len(info.Methods) != 2 || info.Methods[0] != AuthMethodOAuth || info.Methods[1] != AuthMethodAPIKey {
		t.Fatalf("methods = %#v", info.Methods)
	}
}

func TestProviderAuthInfoConfiguredFromEnv(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	info, ok := ProviderAuthInfoForProvider("google-gemini-cli")
	if !ok {
		t.Fatal("expected provider auth info")
	}
	if !info.Configured {
		t.Fatal("expected configured auth")
	}
}

func TestProviderAuthInfosSorted(t *testing.T) {
	infos := ProviderAuthInfos()
	if len(infos) == 0 {
		t.Fatal("expected auth infos")
	}
	for i := 1; i < len(infos); i++ {
		if infos[i-1].Provider > infos[i].Provider {
			t.Fatalf("infos not sorted: %q > %q", infos[i-1].Provider, infos[i].Provider)
		}
	}
}
