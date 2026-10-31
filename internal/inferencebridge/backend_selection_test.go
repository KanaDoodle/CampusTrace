package inferencebridge

import "testing"

func cleanBackendEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"NARRATION_BACKEND", "LLM_URL", "LLM_API_KEY", "LLM_MODEL", "LLM_CONCURRENCY", "INFERENCE_RPC_ADDRESS", "INFERENCE_MODEL", "INFERENCE_BACKEND_KIND"} {
		t.Setenv(key, "")
	}
}

func TestDailyAPIEnabledWithoutUnpublishedSDK(t *testing.T) {
	cleanBackendEnvironment(t)
	t.Setenv("LLM_URL", "https://api.example.invalid/v1/chat/completions")
	t.Setenv("LLM_MODEL", "synthetic-model")
	t.Setenv("LLM_API_KEY", "synthetic-key")
	c, err := FromEnvironment([]byte("synthetic-scope-secret-long-enough"), nil)
	if err != nil || c == nil {
		t.Fatalf("configured daily API must support durable narration in the default build: client=%v err=%v", c, err)
	}
	c.Close()
}

func TestLegacyRPCConfigurationRequiresExplicitBackend(t *testing.T) {
	cleanBackendEnvironment(t)
	t.Setenv("INFERENCE_RPC_ADDRESS", "127.0.0.1:9094")
	t.Setenv("INFERENCE_MODEL", "synthetic-model")
	c, err := FromEnvironment([]byte("synthetic-scope-secret-long-enough"), nil)
	if c != nil {
		c.Close()
	}
	if err == nil {
		t.Fatal("RPC experiments must require NARRATION_BACKEND=rpc")
	}
}

func TestBackendConfigurationMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, mode, endpoint, model, address string
		wantEnabled, wantError               bool
	}{
		{"offline", "", "", "", "", false, false},
		{"daily-api", "api", "https://example.invalid/v1/chat/completions", "model", "", true, false},
		{"missing-model", "api", "https://example.invalid/v1/chat/completions", "", "", false, true},
		{"missing-url", "api", "", "model", "", false, true},
		{"disabled", "disabled", "https://example.invalid/v1/chat/completions", "model", "127.0.0.1:9094", false, false},
		{"invalid-mode", "auto", "", "", "", false, true},
		{"rpc-incomplete", "rpc", "https://example.invalid/v1/chat/completions", "model", "", false, true},
		{"no-implicit-rpc", "api", "https://example.invalid/v1/chat/completions", "model", "127.0.0.1:9094", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanBackendEnvironment(t)
			t.Setenv("NARRATION_BACKEND", tc.mode)
			t.Setenv("LLM_URL", tc.endpoint)
			t.Setenv("LLM_MODEL", tc.model)
			t.Setenv("INFERENCE_RPC_ADDRESS", tc.address)
			c, err := FromEnvironment([]byte("synthetic-scope-secret-long-enough"), nil)
			if c != nil {
				defer c.Close()
			}
			if (c != nil) != tc.wantEnabled || (err != nil) != tc.wantError {
				t.Fatal(c, err)
			}
		})
	}
}
