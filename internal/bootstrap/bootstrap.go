package bootstrap

import (
	"context"
	"encoding/json"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func Root() (context.Context, context.CancelFunc) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

type App struct {
	Config  config.Config
	Store   *p.Store
	Queue   *pipeline.Queue
	Tools   *agent.Tools
	Agent   *agent.Runtime
	Metrics *observability.Metrics
}

func Open(ctx context.Context) (*App, error) {
	c := config.Load()
	s, err := p.Open(ctx, c.DSN)
	if err != nil {
		return nil, err
	}
	if raw := os.Getenv("RANKING_WEIGHTS"); raw != "" {
		if err = json.Unmarshal([]byte(raw), &s.Weights); err != nil {
			s.DB.Close()
			return nil, err
		}
		if err = rules.ValidateWeights(s.Weights); err != nil {
			s.DB.Close()
			return nil, err
		}
	}
	r := redis.NewClient(&redis.Options{Addr: c.Redis, ContextTimeoutEnabled: true})
	if err = r.Ping(ctx).Err(); err != nil {
		s.DB.Close()
		r.Close()
		return nil, err
	}
	q := &pipeline.Queue{R: r, Prefix: "ct:"}
	rg := &rag.Service{Store: s, Sem: make(chan struct{}, c.EmbeddingConcurrency), Allow: func(ctx context.Context) (bool, error) { return q.Allow(ctx, "embedding:global", 120, time.Minute) }}
	tools := &agent.Tools{Store: s, RAG: rg, Queue: q}
	var model agent.Model = agent.DemoModel{}
	if c.LLMURL != "" {
		client := analysis.NewChat(c.LLMURL, c.LLMKey, c.LLMModel, c.LLMConcurrency)
		client.Allow = func(ctx context.Context) (bool, error) { return q.Allow(ctx, "llm:global", 30, time.Minute) }
		model = agent.LiveModel{Client: client}
	}
	runtime := &agent.Runtime{MaxToolResultBytes: config.Int("AGENT_MAX_TOOL_RESULT_BYTES", 32768), MaxFactsBytes: config.Int("AGENT_MAX_FACTS_BYTES", 98304), MaxAnswerBytes: config.Int("AGENT_MAX_ANSWER_BYTES", 32768), MaxFinalBytes: config.Int("AGENT_MAX_FINAL_BYTES", 163840), Model: model, Tools: tools, R: r, Prefix: q.Prefix, MaxModels: config.Int("AGENT_MAX_MODEL_CALLS", 4), MaxTools: config.Int("AGENT_MAX_TOOL_CALLS", 8), Deadline: time.Duration(config.Int("AGENT_DEADLINE_SECONDS", 35)) * time.Second, ToolTimeout: time.Duration(config.Int("AGENT_TOOL_TIMEOUT_SECONDS", 8)) * time.Second}
	return &App{c, s, q, tools, runtime, observability.New()}, nil
}
func (a *App) Close() { a.Store.DB.Close(); a.Queue.R.Close() }
