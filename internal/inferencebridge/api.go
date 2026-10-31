package inferencebridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
)

type apiClient struct {
	client         *analysis.ChatClient
	kind, identity string
}

func NewAPI(shared *analysis.ChatClient, kind string) (Client, error) {
	if shared == nil || shared.HTTP == nil || shared.Sem == nil || cap(shared.Sem) == 0 || strings.TrimSpace(shared.Model) == "" {
		return nil, errors.New("incomplete model API configuration")
	}
	u, err := url.Parse(shared.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()))) {
		return nil, errors.New("model API requires HTTPS or loopback HTTP without URL credentials, query or fragment")
	}
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/chat/completions") {
		return nil, errors.New("LLM_URL must be the complete Chat Completions endpoint")
	}
	if kind == "" {
		kind = "configured-upstream"
	}
	if kind != "configured-upstream" && kind != "fixture-no-model" {
		return nil, errors.New("unknown inference evidence kind")
	}
	// Share concurrency, not mutable settings. The runner already checks global
	// rate admission before saving DISPATCHED; do not debit that quota twice.
	c := *shared
	c.Allow = nil
	c.OutputTokenLimit = 512
	h := *shared.HTTP
	if h.Timeout <= 0 || h.Timeout > 35*time.Second {
		h.Timeout = 35 * time.Second
	}
	c.HTTP = &h
	sum := sha256.Sum256([]byte("api\x00" + c.URL + "\x00" + c.Model + "\x00" + kind + "\x00512/" + agent.NarrationVersion))
	return &apiClient{client: &c, kind: kind, identity: hex.EncodeToString(sum[:])}, nil
}

func (c *apiClient) Close()               {} // No background workers; the shared HTTP pool belongs to the application.
func (c *apiClient) Identity() string     { return c.identity }
func (c *apiClient) Label() string        { return c.client.Model }
func (c *apiClient) EvidenceKind() string { return c.kind }
func (c *apiClient) Generate(ctx context.Context, owner string, messages []agent.Message, emit func(string) error) (agent.NarrationUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if owner == "" || emit == nil || len(messages) == 0 || len(messages) > 16 {
		return agent.NarrationUsage{}, errors.New("scoped reviewed consumer required")
	}
	chat := make([]map[string]string, 0, len(messages))
	for _, m := range messages {
		if len(m.Calls) > 0 || m.ToolCallID != "" || (m.Role != "system" && m.Role != "user") || !utf8.ValidString(m.Content) {
			return agent.NarrationUsage{}, errors.New("narration accepts text-only reviewed inputs")
		}
		chat = append(chat, map[string]string{"role": m.Role, "content": m.Content})
	}
	// Owner identity is used by the task boundary only. Do not send raw user IDs,
	// vLLM cache_salt, tools or gateway metadata to a third-party model API.
	u, err := c.client.StreamText(ctx, chat, emit)
	return agent.NarrationUsage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens}, err
}
