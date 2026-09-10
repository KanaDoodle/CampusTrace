package agent

import (
	"context"
	"encoding/json"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
	"testing"
	"time"
)

type releaseModel struct {
	calls []Call
	n     int
}

func (m *releaseModel) Next(context.Context, []Message, []Definition) (Reply, error) {
	m.n++
	if m.n == 1 {
		return Reply{Calls: m.calls}, nil
	}
	return Reply{}, nil
}

type releaseTools struct {
	n    int
	size int
}

func (t *releaseTools) Definitions() []Definition { return nil }
func (t *releaseTools) Execute(context.Context, string, string, json.RawMessage) (any, error) {
	t.n++
	return map[string]any{"verified_facts": []d.ProjectFact{{ID: "f", Verified: true, Kind: "IMPLEMENTED", Claim: strings.Repeat("界", t.size)}}}, nil
}
func TestReleaseOutputBudgetsAndCanonicalDedup(t *testing.T) {
	for _, size := range []int{100, 2000, 2 << 20} {
		ts := &releaseTools{size: size}
		m := &releaseModel{calls: []Call{{ID: "one", Name: "search_jobs", Args: json.RawMessage(`{"query":"go"}`)}, {ID: "two", Name: "search_jobs", Args: json.RawMessage(`{ "query" : "go" }`)}, {ID: "two", Name: "search_jobs", Args: json.RawMessage(`{"query":"other"}`)}}}
		r := &Runtime{Model: m, Tools: ts, MaxModels: 3, MaxTools: 8, Deadline: time.Second * 5, ToolTimeout: time.Second, MaxToolResultBytes: 4096, MaxFactsBytes: 6000, MaxAnswerBytes: 1024, MaxFinalBytes: 8192}
		out := r.Run(context.Background(), "u", "s", "q", func(e Event) error {
			if e.Type == "final" && len(d.JSON(e))+len("event: final\ndata: \n\n") > 8192 {
				t.Error("SSE budget exceeded")
			}
			return nil
		})
		if ts.n != 1 {
			t.Fatal("canonical duplicate executed", ts.n)
		}
		if len(d.JSON(out.Facts)) > 6000 || len(out.Answer) > 1024 || len(d.JSON(out)) > 8192 {
			t.Fatal("aggregate budget exceeded")
		}
	}
}

func TestReleaseAnswerAndAggregateBudgets(t *testing.T) {
	for _, mode := range []string{"answer", "facts", "final"} {
		ts := &releaseTools{size: 2000}
		calls := []Call{{ID: "one", Name: "get_project_facts", Args: json.RawMessage(`{}`)}}
		if mode == "facts" {
			for i := 0; i < 8; i++ {
				calls = append(calls, Call{ID: fmt.Sprint(i), Name: "search_jobs", Args: json.RawMessage(fmt.Sprintf(`{"query":"go%d"}`, i))})
			}
		}
		r := &Runtime{Model: &releaseModel{calls: calls}, Tools: ts, MaxModels: 3, MaxTools: 12, Deadline: time.Second * 5, ToolTimeout: time.Second, MaxToolResultBytes: 10000, MaxFactsBytes: 12000, MaxAnswerBytes: 1024, MaxFinalBytes: 16000}
		if mode == "final" {
			r.MaxAnswerBytes = 10000
			r.MaxFinalBytes = 2048
		}
		out := r.Run(context.Background(), "u", "s", "q", func(e Event) error {
			if e.Type == "final" && len(d.JSON(e))+len("event: final\ndata: \n\n") > r.MaxFinalBytes {
				t.Error("final exceeded")
			}
			return nil
		})
		if out.Terminal != "OUTPUT_LIMIT" {
			t.Fatal(mode, out.Terminal)
		}
		if len(out.Answer) > r.MaxAnswerBytes || len(d.JSON(out.Facts)) > r.MaxFactsBytes {
			t.Fatal("output exceeded")
		}
	}
}
