package inferencebridge

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
)

type Config struct {
	Address, Model, EvidenceKind string
	ScopeKey                     []byte
	Timeout                      time.Duration
}
type Client interface {
	agent.NarrationClient
	Close()
}

// shared is the existing daily API client. Its semaphore is reused, while the
// narration runner owns its own consent, global rate check and durable attempt.
func FromEnvironment(scopeKey []byte, shared *analysis.ChatClient) (Client, error) {
	mode := strings.TrimSpace(os.Getenv("NARRATION_BACKEND"))
	if mode == "" {
		mode = "api"
	}
	if mode == "disabled" {
		return nil, nil
	}
	if mode != "api" && mode != "rpc" {
		return nil, errors.New("NARRATION_BACKEND must be api, rpc or disabled")
	}
	address, model := os.Getenv("INFERENCE_RPC_ADDRESS"), os.Getenv("INFERENCE_MODEL")
	if mode == "api" {
		if address != "" || model != "" {
			return nil, errors.New("RPC configuration requires explicit NARRATION_BACKEND=rpc; clear INFERENCE_RPC_ADDRESS and INFERENCE_MODEL for daily API mode")
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
	if address == "" || model == "" || len(scopeKey) < 32 {
		return nil, errors.New("incomplete inference RPC configuration")
	}
	return New(Config{Address: address, Model: model, ScopeKey: scopeKey, Timeout: 35 * time.Second, EvidenceKind: os.Getenv("INFERENCE_BACKEND_KIND")})
}
