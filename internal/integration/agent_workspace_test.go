package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type contextModel struct{ messages []agent.Message }

func (m *contextModel) Next(_ context.Context, ms []agent.Message, _ []agent.Definition) (agent.Reply, error) {
	if ms[len(ms)-1].Role == "tool" {
		return agent.Reply{Text: "untrusted model answer"}, nil
	}
	m.messages = append([]agent.Message{}, ms...)
	return agent.Reply{Calls: []agent.Call{{ID: "memory-query", Name: "search_memories", Args: json.RawMessage(`{"query":""}`)}}}, nil
}
func TestAgentMemoryReviewedWritesAndForget(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	tools := &agent.Tools{Store: s, Queue: q}
	in := p.MemoryInput{Kind: "PREFERENCE", Content: "主投 Go 后端，优先上海", Scope: "选岗"}
	pending, e := tools.Propose(ctx, u, "remember_memory", json.RawMessage(d.JSON(in)))
	must(t, e)
	rows, e := s.AgentMemories(ctx, u)
	must(t, e)
	if len(rows) != 0 {
		t.Fatal("proposal mutated memory")
	}
	result, e := tools.Confirm(ctx, u, pending.ID)
	must(t, e)
	rows, e = s.AgentMemories(ctx, u)
	must(t, e)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	q.R.Del(ctx, q.Prefix+"pending:"+pending.ID)
	replay, e := tools.Confirm(ctx, u, pending.ID)
	must(t, e)
	if d.JSON(result) != d.JSON(replay) {
		t.Fatal("receipt not replayed")
	}
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, e)
	if _, e = tools.Confirm(ctx, other, pending.ID); e == nil {
		t.Fatal("cross-user receipt")
	}
	if e = s.ForgetMemory(ctx, other, rows[0].ID, rows[0].Version); !errors.Is(e, p.ErrNotFound) {
		t.Fatal(e)
	}
	var ok, conflict atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := rows[0].MemoryInput
			in.Content = "优先 Go 平台岗"
			_, e := s.SaveMemory(ctx, u, in)
			if e == nil {
				ok.Add(1)
			} else if errors.Is(e, p.ErrConflict) {
				conflict.Add(1)
			} else {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || conflict.Load() != 1 {
		t.Fatal(ok.Load(), conflict.Load())
	}
	fresh, e := s.AgentMemories(ctx, u)
	must(t, e)
	must(t, s.ForgetMemory(ctx, u, fresh[0].ID, fresh[0].Version))
	after, e := s.AgentMemories(ctx, u)
	must(t, e)
	if len(after) != 0 {
		t.Fatal(after)
	}
	var stored string
	must(t, s.DB.QueryRowContext(ctx, "SELECT body FROM agent_memories WHERE id=?", fresh[0].ID).Scan(&stored))
	receipt, e := tools.Confirm(ctx, u, pending.ID)
	must(t, e)
	if strings.Contains(string(receipt), "合成") || strings.Contains(string(receipt), "主投 Go 后端") {
		t.Fatal("forgotten receipt retained text")
	}
	if strings.Contains(stored, "Go 平台") {
		t.Fatal("forgotten text retained")
	}
	if _, e = s.SaveMemory(ctx, u, p.MemoryInput{Kind: "PREFERENCE", Content: "person@example.com"}); !errors.Is(e, p.ErrValidation) {
		t.Fatal("contact accepted", e)
	}
}
func TestAgentContextBindsCorrectionsAndExplicitContinuation(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER", GraduationYear: 2027, Languages: []string{"Go"}}))
	note, e := s.SaveMemory(ctx, u, p.MemoryInput{Kind: "CORRECTION", Content: "张小明的项目没有线上压测结果，不声称生产规模"})
	must(t, e)
	tools := &agent.Tools{Store: s, Queue: q, MaskName: "张小明"}
	model := &contextModel{}
	runtime := agent.Runtime{Store: s, Tools: tools, Model: model, R: q.R, Prefix: q.Prefix, MaxModels: 3, MaxTools: 3, Deadline: time.Second * 5, ToolTimeout: time.Second}
	first := runtime.Run(ctx, u, "session-one", "查询我的记忆", nil)
	if first.TaskID == "" || first.Terminal != "COMPLETED" {
		t.Fatal(first)
	}
	text := d.JSON(model.messages)
	if strings.Contains(text, "张小明") || !strings.Contains(text, "没有线上压测") {
		t.Fatal("memory not masked or absent", text)
	}
	runtime.ResumeTask = first.TaskID
	next := runtime.Run(ctx, u, "new-session", "继续选岗", nil)
	if next.MemoryNotice != "" || !strings.Contains(d.JSON(model.messages), "User explicitly continued") {
		t.Fatal("task not resumed", next)
	}
	note.Content = "允许异地，先选平台后端岗"
	note, e = s.SaveMemory(ctx, u, note.MemoryInput)
	must(t, e)
	runtime.MaxFinalBytes = 512
	stale := runtime.Run(ctx, u, "session-one", "继续选岗", nil)
	if stale.MemoryNotice == "" || strings.Contains(d.JSON(model.messages), "User explicitly continued") {
		t.Fatal("stale context replayed", stale)
	}
	if size := len(d.JSON(agent.Event{Type: "final", Data: stale})) + len("event: final\ndata: \n\n"); size > 512 {
		t.Fatal("task metadata exceeded final output budget", size)
	}
	must(t, s.ForgetMemory(ctx, u, note.ID, note.Version))
	tasks, e := s.AgentTasks(ctx, u)
	must(t, e)
	if len(tasks) != 0 {
		t.Fatal("forgotten contexts survived")
	}
	_, e = s.AgentTask(ctx, u, first.TaskID)
	if !errors.Is(e, p.ErrNotFound) {
		t.Fatal(e)
	}
}

func TestAgentWorkspaceHTTPOwnerAndDisabledPractice(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	authn := auth.Service{Store: s, Secret: []byte("agent-test-secret-longer-than-32-bytes")}
	tok, e := authn.Token(u)
	must(t, e)
	api := &transport.API{Store: s, Queue: q, Auth: authn}
	h := api.Handler()
	res := matchingRequest(h, tok, "/api/agent/memories", "POST", p.MemoryInput{Kind: "DECISION", Content: "先准备 Go 并发，再复习数据库"})
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	var note p.AgentMemory
	must(t, json.Unmarshal(res.Body.Bytes(), &note))
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, e)
	ot, e := authn.Token(other)
	must(t, e)
	res = matchingRequest(h, ot, "/api/agent/memories/"+note.ID, "DELETE", map[string]any{"version": note.Version})
	if res.Code != http.StatusNotFound {
		t.Fatal(res.Code, res.Body.String())
	}
	res = matchingRequest(h, tok, "/api/practice/capabilities", "GET", nil)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"enabled":false`) {
		t.Fatal(res.Body.String())
	}
}
