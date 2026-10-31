//go:build kanarpc_stream

package inferencebridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/KanaRPC-Go/inference/gateway"
	"github.com/KanaDoodle/KanaRPC-Go/inference/scheduler"
	"github.com/KanaDoodle/KanaRPC-Go/inference/vllm"
	"github.com/KanaDoodle/KanaRPC-Go/rpc"
)

type rpcClient struct {
	client   *rpc.Client
	cfg      Config
	identity string
}

func New(cfg Config) (Client, error) {
	if strings.TrimSpace(cfg.Model) == "" || len(cfg.ScopeKey) < 32 {
		return nil, errors.New("invalid inference identity or scope key")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 35 * time.Second
	}
	if cfg.Timeout <= 0 || cfg.Timeout > time.Minute {
		return nil, errors.New("inference timeout out of bounds")
	}
	if cfg.EvidenceKind == "" {
		cfg.EvidenceKind = "configured-upstream"
	}
	if cfg.EvidenceKind != "configured-upstream" && cfg.EvidenceKind != "fixture-no-model" {
		return nil, errors.New("unknown inference evidence kind")
	}
	c, err := rpc.NewDirectClient(cfg.Address, rpc.ClientConfig{Timeout: cfg.Timeout})
	if err != nil {
		return nil, err
	}
	cfg.ScopeKey = append([]byte(nil), cfg.ScopeKey...)
	sum := sha256.Sum256([]byte(cfg.Address + "\x00" + cfg.Model + "\x00" + cfg.EvidenceKind + "\x00512/" + agent.NarrationVersion))
	return &rpcClient{client: c, cfg: cfg, identity: hex.EncodeToString(sum[:])}, nil
}
func (c *rpcClient) Close()               { c.client.Close() }
func (c *rpcClient) Identity() string     { return c.identity }
func (c *rpcClient) Label() string        { return c.cfg.Model }
func (c *rpcClient) EvidenceKind() string { return c.cfg.EvidenceKind }
func (c *rpcClient) Generate(ctx context.Context, user string, messages []agent.Message, emit func(string) error) (agent.NarrationUsage, error) {
	var usage agent.NarrationUsage
	if user == "" || emit == nil {
		return usage, errors.New("scoped consumer required")
	}
	chat := vllm.ChatRequest{MaxTokens: 512}
	for _, m := range messages {
		if len(m.Calls) > 0 || m.ToolCallID != "" || (m.Role != "system" && m.Role != "user") {
			return usage, errors.New("narration accepts text-only reviewed inputs")
		}
		chat.Messages = append(chat.Messages, vllm.Message{Role: m.Role, Content: m.Content})
	}
	mac := hmac.New(sha256.New, c.cfg.ScopeKey)
	_, _ = mac.Write([]byte("campustrace-narration-v1/" + user))
	chat.CacheSalt = hex.EncodeToString(mac.Sum(nil))
	stream, err := c.client.InvokeStream(ctx, gateway.ServiceForModel(c.cfg.Model), vllm.MethodName, gateway.Request{Chat: chat, Class: scheduler.Interactive})
	if err != nil {
		return usage, err
	}
	defer stream.Close()
	completed, hasText := false, false
	for {
		var event gateway.Event
		err := stream.Recv(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return usage, err
		}
		switch event.Type {
		case "scheduled":
		case "summary":
			if event.Summary == nil || event.Summary.Terminal != "eof" {
				return usage, errors.New("invalid inference summary")
			}
			completed = true
			if event.Summary.Usage != nil {
				usage.PromptTokens = event.Summary.Usage.PromptTokens
				usage.CompletionTokens = event.Summary.Usage.CompletionTokens
			}
		case "chunk":
			var chunk vllm.ChatChunk
			if json.Unmarshal(event.Chunk, &chunk) != nil || (chunk.Model != "" && chunk.Model != c.cfg.Model) || len(chunk.Choices) > 1 {
				return usage, agent.ErrNarrationOutput
			}
			for _, choice := range chunk.Choices {
				if len(choice.Delta.ToolCalls) > 0 && string(choice.Delta.ToolCalls) != "null" && string(choice.Delta.ToolCalls) != "[]" {
					return usage, agent.ErrNarrationOutput
				}
				if choice.Delta.Content != "" {
					hasText = true
					if err := emit(choice.Delta.Content); err != nil {
						return usage, err
					}
				}
			}
		default:
			return usage, errors.New("unknown inference event")
		}
	}
	if !completed || !hasText {
		return usage, errors.New("inference stream incomplete")
	}
	return usage, nil
}
