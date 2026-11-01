package agent

// LangGraph owns routing and persistent graph checkpoints. This bridge exposes
// one guarded operation at a time; it never runs the legacy Go loop.
import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

const PreparationGraphVersion = "preparation-langgraph-v1"

type PreparationGraphClient struct {
	framework  map[string]string
	URL, Token string
	HTTP       *http.Client
	mu         sync.Mutex
	sessions   map[string]*preparationGraphSession
}
type preparationGraphSession struct {
	closed     bool
	mu         sync.Mutex
	ctx        context.Context
	user       string
	v          *p.AgentExecution
	model      Model
	tools      Toolset
	checkpoint func() error
}

// Graph checkpoints hold orchestration metadata, never material bodies,
// model messages, returned plans or credentials. SQL remains the only context authority.
type preparationGraphStatus struct {
	ProviderState string `json:"provider_state"`
	Question      string `json:"question,omitempty"`
	ModelCalls    int    `json:"model_calls"`
	ToolCalls     int    `json:"tool_calls"`
	SearchCalls   int    `json:"search_calls"`
}
type preparationGraphView struct {
	Sequence    int                    `json:"sequence"`
	Phase       string                 `json:"phase"`
	Pending     string                 `json:"pending,omitempty"`
	Preparation preparationGraphStatus `json:"preparation"`
}

func preparationGraphSnapshot(v *p.AgentExecution) preparationGraphView {
	out := preparationGraphView{Sequence: v.Preparation.GraphSequence, Phase: v.State, Preparation: preparationGraphStatus{ProviderState: v.Preparation.ProviderState, ModelCalls: v.Preparation.ModelCalls, ToolCalls: v.Preparation.ToolCalls, SearchCalls: v.Preparation.SearchCalls}}
	if v.Preparation.Question != "" {
		out.Preparation.Question = "请在申请准备页面补充本轮信息"
	}
	if v.Next < len(v.Calls) {
		out.Pending = v.Calls[v.Next].Name
	}
	return out
}
func NewPreparationGraphClient(url, token string) *PreparationGraphClient {
	return &PreparationGraphClient{URL: strings.TrimRight(url, "/"), Token: token, HTTP: &http.Client{Timeout: 38 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, sessions: map[string]*preparationGraphSession{}}
}
func PreparationGraphFromEnvironment() *PreparationGraphClient {
	url := os.Getenv("PREPARATION_GRAPH_URL")
	if url == "" {
		url = "http://127.0.0.1:18110"
	}
	token := os.Getenv("PREPARATION_SERVICE_TOKEN")
	if token == "" {
		token = "local-preparation-service-change-this-2027"
	}
	return NewPreparationGraphClient(url, token)
}
func (g *PreparationGraphClient) Health(ctx context.Context) error {
	call, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(call, "GET", g.URL+"/healthz", nil)
	if err != nil {
		return p.ErrBackendUnavailable
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return p.ErrBackendUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return p.ErrBackendUnavailable
	}
	var health struct {
		Executor     string `json:"executor"`
		Version      string `json:"graph_version"`
		LangGraph    string `json:"langgraph_version"`
		Checkpointer string `json:"checkpointer_version"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health) != nil || health.Executor != "langgraph" || health.Version != PreparationGraphVersion {
		return p.ErrBackendUnavailable
	}
	g.mu.Lock()
	g.framework = map[string]string{"langgraph": health.LangGraph, "langgraph-checkpoint-sqlite": health.Checkpointer}
	g.mu.Unlock()
	return nil
}
func (g *PreparationGraphClient) Framework() map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]string{}
	for key, value := range g.framework {
		out[key] = value
	}
	return out
}
func (g *PreparationGraphClient) Run(ctx context.Context, user string, v *p.AgentExecution, model Model, tools Toolset, checkpoint func() error) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	capability := hex.EncodeToString(key)
	session := &preparationGraphSession{ctx: ctx, user: user, v: v, model: model, tools: tools, checkpoint: checkpoint}
	g.mu.Lock()
	g.sessions[capability] = session
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(g.sessions, capability); g.mu.Unlock() }()
	raw, _ := json.Marshal(map[string]any{"execution_id": v.ID, "parent_id": v.Preparation.Input.ParentID, "sequence": v.Preparation.GraphSequence})
	req, err := http.NewRequestWithContext(ctx, "POST", g.URL+"/run", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("X-Execution-Capability", capability)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.HTTP.Do(req)
	// Wait for a cancelled callback to leave the SQL checkpoint before the caller
	// performs cleanup. Both request cancellation and the outer lease cancel it.
	session.mu.Lock()
	session.closed = true
	defer session.mu.Unlock()
	if err != nil {
		return p.ErrBackendUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return p.ErrBackendUnavailable
	}
	var result struct {
		Status  string `json:"status"`
		Version string `json:"graph_version"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result) != nil || result.Version != PreparationGraphVersion || (result.Status != "complete" && result.Status != "interrupted" && result.Status != "checkpoint_complete") {
		return p.ErrBackendUnavailable
	}
	if v.State == "RUNNING" {
		v.State = "FAILED"
		v.Answer = "图执行状态与业务回执不一致，请查看诊断后重新核对建立任务。"
		return p.ErrConflict
	}
	return nil
}

// InternalStep requires an unguessable per-attempt capability. Owner, model,
// material scope, lease and credentials come from the active Go session only.
func (g *PreparationGraphClient) InternalStep(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	capability := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	g.mu.Lock()
	s := g.sessions[capability]
	g.mu.Unlock()
	if s == nil {
		http.Error(w, "invalid execution capability", 401)
		return
	}
	var in struct {
		Action   string `json:"action"`
		Sequence int    `json:"sequence"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1025))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.Sequence < 0 || (in.Action != "sync" && in.Action != "model" && in.Action != "tool") {
		http.Error(w, "invalid operation", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		http.Error(w, "execution stopped", 409)
		return
	}
	// Replayed nodes receive the durable receipt, not another provider call/read.
	if in.Action == "sync" || in.Sequence < s.v.Preparation.GraphSequence {
		json.NewEncoder(w).Encode(preparationGraphSnapshot(s.v))
		return
	}
	if in.Sequence != s.v.Preparation.GraphSequence || s.v.State != "RUNNING" {
		http.Error(w, "stale graph cursor", 409)
		return
	}
	if s.v.Preparation.ProviderState != "READY" {
		http.Error(w, "model outcome unknown", 409)
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	// Fence every operation before dispatch, including reads and model admission.
	if err := s.checkpoint(); err != nil {
		http.Error(w, "stale execution", 409)
		return
	}
	var checkpointErr error
	persist := func() error {
		err := s.checkpoint()
		if err != nil {
			checkpointErr = err
		}
		return err
	}
	err := runPreparationStep(ctx, s.user, s.v, s.model, s.tools, persist, in.Action)
	if checkpointErr != nil {
		http.Error(w, "checkpoint not durable", 409)
		return
	}
	if err != nil && s.v.State == "RUNNING" {
		s.v.State = "PAUSED"
		s.v.Answer = "执行中断，已完成步骤已保存。"
		if s.v.Preparation.ProviderState == "DISPATCHED" {
			s.v.Preparation.ProviderState = "UNKNOWN"
		}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.v.Answer = "本轮未完成，请检查执行过程后继续。"
		}
		// A failed SQL fence must never be turned into a successful graph response.
		if saveErr := s.checkpoint(); saveErr != nil {
			http.Error(w, "execution fenced", 409)
			return
		}
	}
	json.NewEncoder(w).Encode(preparationGraphSnapshot(s.v))
}
