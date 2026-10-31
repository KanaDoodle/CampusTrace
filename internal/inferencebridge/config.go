package inferencebridge

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
)

type Client interface {
	agent.NarrationClient
	Close()
}

// shared is the existing daily API client. Its semaphore is reused, while the
// narration runner owns its own consent, global rate check and durable attempt.
func FromEnvironment(_ []byte, shared *analysis.ChatClient) (Client, error) {
	mode := strings.TrimSpace(os.Getenv("NARRATION_BACKEND"))
	if mode == "" {
		mode = "api"
	}
	if mode == "disabled" {
		return nil, nil
	}
	if mode != "api" {
		return nil, errors.New("NARRATION_BACKEND must be api or disabled; RPC/vLLM experiments are archived")
	}
	address, model := os.Getenv("INFERENCE_RPC_ADDRESS"), os.Getenv("INFERENCE_MODEL")
	if address != "" || model != "" {
		return nil, errors.New("RPC inference is archived; clear INFERENCE_RPC_ADDRESS and INFERENCE_MODEL for daily API mode")
	}
	if shared == nil {
		endpoint, key, model := os.Getenv("LLM_URL"), os.Getenv("LLM_API_KEY"), os.Getenv("LLM_MODEL")
		if endpoint == "" && key == "" && model == "" {
			return nil, nil
		}
		n := 2
		if raw := os.Getenv("LLM_CONCURRENCY"); raw != "" {
			var err error
			n, err = strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return nil, errors.New("LLM_CONCURRENCY must be positive")
			}
		}
		shared = analysis.NewChat(endpoint, key, model, n)
	}
	return NewAPI(shared, os.Getenv("INFERENCE_BACKEND_KIND"))
}
