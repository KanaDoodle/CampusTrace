//go:build kanarpc_stream

package inferencebridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/KanaRPC-Go/inference/gateway"
	"github.com/KanaDoodle/KanaRPC-Go/inference/lab"
	"github.com/KanaDoodle/KanaRPC-Go/inference/scheduler"
	"github.com/KanaDoodle/KanaRPC-Go/inference/vllm"
	"github.com/KanaDoodle/KanaRPC-Go/rpc"
)

func TestRPCNarrationStreamsUsageAndScopesCacheByAuthenticatedOwner(t *testing.T) {
	salts := make(chan string, 2)
	backend := &lab.Backend{OnChat: func(request vllm.ChatRequest) { salts <- request.CacheSalt }, Gap: 10 * time.Millisecond}
	w, err := lab.StartWorker("w", backend, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	g, err := gateway.New(gateway.Config{Model: lab.Model, Workers: []scheduler.Worker{w.Endpoint}, MetricsTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err := g.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	server, closeServer, err := lab.StartRPC(8, g.Register)
	if err != nil {
		t.Fatal(err)
	}
	defer closeServer()
	c, err := New(Config{Address: server.Addr().String(), Model: lab.Model, ScopeKey: []byte(strings.Repeat("k", 32)), EvidenceKind: "fixture-no-model"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, owner := range []string{"owner-a", "owner-b"} {
		var text strings.Builder
		usage, err := c.Generate(context.Background(), owner, []agent.Message{{Role: "user", Content: "reviewed fixture"}}, func(value string) error { text.WriteString(value); return nil })
		if err != nil || text.String() != "fixture output" || usage.CompletionTokens != 2 {
			t.Fatal(text.String(), usage, err)
		}
	}
	a, b := <-salts, <-salts
	if a == b || len(a) != 64 || len(b) != 64 || strings.Contains(a, "owner") {
		t.Fatal("cache scope leaked or merged", a, b)
	}
}
func TestRPCNarrationRejectsToolCallsAndDoesNotExposeReasoning(t *testing.T) {
	for _, scenario := range []string{"tool_calls", "reasoning_only", "missing_summary"} {
		t.Run(scenario, func(t *testing.T) {
			server, closeServer, err := lab.StartRPC(4, func(server *rpc.Server) error {
				return server.RegisterStream(gateway.ServiceForModel(lab.Model), vllm.MethodName, func(ctx context.Context, _ json.RawMessage, out *rpc.ServerStream) error {
					chunk := vllm.ChatChunk{Model: lab.Model, Choices: []vllm.Choice{{Delta: vllm.Delta{Content: "fixture"}}}}
					if scenario == "tool_calls" {
						chunk.Choices[0].Delta.ToolCalls = json.RawMessage(`[{"function":{"name":"create_application","arguments":"{}"}}]`)
					}
					if scenario == "reasoning_only" {
						chunk.Choices[0].Delta.Content = ""
						chunk.Choices[0].Delta.ReasoningContent = "private reasoning"
					}
					raw, _ := json.Marshal(chunk)
					if err := out.Send(gateway.Event{Type: "chunk", Chunk: raw}); err != nil {
						return err
					}
					if scenario != "missing_summary" {
						return out.Send(gateway.Event{Type: "summary", Summary: &gateway.Summary{Terminal: "eof"}})
					}
					return nil
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			defer closeServer()
			c, err := New(Config{Address: server.Addr().String(), Model: lab.Model, ScopeKey: []byte(strings.Repeat("k", 32)), EvidenceKind: "fixture-no-model"})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			var output strings.Builder
			_, err = c.Generate(context.Background(), "owner", []agent.Message{{Role: "user", Content: "reviewed"}}, func(text string) error { output.WriteString(text); return nil })
			if err == nil {
				t.Fatal("unsafe or incomplete model stream accepted")
			}
			if scenario == "tool_calls" && !errors.Is(err, agent.ErrNarrationOutput) {
				t.Fatal(err)
			}
			if scenario == "reasoning_only" && output.Len() != 0 {
				t.Fatal("reasoning leaked")
			}
		})
	}
}
