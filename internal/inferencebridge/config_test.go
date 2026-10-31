package inferencebridge

import "testing"

func TestOptionalConfigurationDoesNotSilentlyEnableAnotherProvider(t *testing.T) {
	cleanBackendEnvironment(t)
	t.Setenv("INFERENCE_RPC_ADDRESS", "")
	t.Setenv("INFERENCE_MODEL", "")
	client, err := FromEnvironment(nil, nil)
	if client != nil || err != nil {
		t.Fatalf("unconfigured service must stay disabled: %v %v", client, err)
	}
	t.Setenv("INFERENCE_RPC_ADDRESS", "127.0.0.1:1")
	if _, err = FromEnvironment([]byte("synthetic-scope-secret-long-enough"), nil); err == nil {
		t.Fatal("partial configuration silently accepted")
	}
	t.Setenv("NARRATION_BACKEND", "rpc")
	t.Setenv("INFERENCE_MODEL", "synthetic-model")
	if _, err = FromEnvironment([]byte("short"), nil); err == nil {
		t.Fatal("archived RPC backend was enabled")
	}
}
