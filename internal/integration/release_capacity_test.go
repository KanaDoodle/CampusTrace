package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/redis/go-redis/v9"
	"strings"
	"testing"
	"time"
)

type reModel struct {
	calls      []agent.Call
	step       int
	maxMessage int
}

func (m *reModel) Next(ctx context.Context, ms []agent.Message, defs []agent.Definition) (agent.Reply, error) {
	for _, s := range ms {
		if s.Role == "tool" && len(s.Content) > m.maxMessage {
			m.maxMessage = len(s.Content)
		}
	}
	m.step++
	if m.step == 1 {
		return agent.Reply{Calls: m.calls}, nil
	}
	return agent.Reply{}, nil
}

type reLargeTools struct {
	n    int
	size int
}

func (t *reLargeTools) Definitions() []agent.Definition { return nil }
func (t *reLargeTools) Execute(context.Context, string, string, json.RawMessage) (any, error) {
	t.n++
	return map[string]any{"verified_facts": []d.ProjectFact{{ID: "f", Verified: true, Kind: "IMPLEMENTED", Claim: strings.Repeat("x", t.size)}}}, nil
}
func TestReleaseAgentDuplicatesAndBudgets(t *testing.T) {
	for _, size := range []int{23000, 2 << 20} {
		ts := &reLargeTools{size: size}
		m := &reModel{}
		for i := 0; i < 8; i++ {
			m.calls = append(m.calls, agent.Call{ID: "duplicate", Name: "get_project_facts", Args: json.RawMessage(`{}`)})
		}
		r := &agent.Runtime{Model: m, Tools: ts, MaxModels: 3, MaxTools: 8, Deadline: time.Second * 10, ToolTimeout: time.Second}
		finalBytes := 0
		out := r.Run(context.Background(), "u", "s", "q", func(e agent.Event) error {
			if e.Type == "final" {
				finalBytes = len(d.JSON(e.Data))
			}
			return nil
		})
		t.Logf("single_data=%d duplicate executions=%d facts_bytes=%d answer_bytes=%d final_bytes=%d max_model_tool_bytes=%d", size, ts.n, len(d.JSON(out.Facts)), len(out.Answer), finalBytes, m.maxMessage)
		if ts.n != 1 {
			t.Errorf("same call ID and same args executed %d times", ts.n)
		}
		if finalBytes > 256000 {
			t.Error("SSE final exceeds aggregate budget")
		}
	}
	ctx, s, q, u, src := setup(t)
	in := ingest(t, ctx, s, src)
	o, e := s.Ingest(ctx, in)
	must(t, e)
	m := &reModel{calls: []agent.Call{{ID: "dup", Name: "create_application", Args: json.RawMessage(d.JSON(p.CreateArgs{JobID: o.JobID}))}, {ID: "dup", Name: "create_application", Args: json.RawMessage(d.JSON(p.CreateArgs{JobID: o.JobID}))}}}
	ts := &agent.Tools{Store: s, Queue: q}
	r := &agent.Runtime{Model: m, Tools: ts, MaxModels: 3, MaxTools: 8, Deadline: time.Second * 5, ToolTimeout: time.Second}
	out := r.Run(ctx, u, "s", "q", nil)
	keys, e := q.R.Keys(ctx, q.Prefix+"pending:*").Result()
	must(t, e)
	t.Logf("duplicate proposal executed=%d pending_keys=%d", out.Executed, len(keys))
	if len(keys) != 1 {
		t.Error("duplicate pending actions created")
	}
}
func TestReleasePELScanAndClaimWait(t *testing.T) {
	ctx, _, q, _, _ := setup(t)
	must(t, q.Init(ctx))
	for i := 0; i < 200; i++ {
		must(t, q.Publish(ctx, p.NewTask("ANALYZE", d.ID())))
	}
	xs, e := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: "old", Streams: []string{q.Stream(), ">"}, Count: 200}).Result()
	must(t, e)
	all := xs[0].Messages
	tail := []string{}
	for _, m := range all[190:] {
		tail = append(tail, m.ID)
	}
	must(t, q.R.Do(ctx, append([]any{"XCLAIM", q.Stream(), q.Group(), "old", 0}, append(stringsToAny(tail), "IDLE", 2000)...)...).Err())
	claimedTail := map[string]bool{}
	for i := 0; i < 100 && len(claimedTail) < 10; i++ {
		got, e := q.Recover(ctx, "new", time.Second)
		must(t, e)
		if len(got) > 1 {
			t.Fatal("claimed more than one execution slot")
		}
		for _, m := range got {
			claimedTail[m.ID] = true
			must(t, q.Ack(ctx, m.ID))
		}
	}
	if len(claimedTail) != 10 {
		t.Fatalf("tail scan got %d of 10", len(claimedTail))
	}
	// All 16 messages are claimed at once but their execution slots are serial.
	small := &pipeline.Queue{R: q.R, Prefix: q.Prefix + "wait:"}
	must(t, small.Init(ctx))
	for i := 0; i < 16; i++ {
		must(t, small.Publish(ctx, p.NewTask("ANALYZE", d.ID())))
	}
	z, e := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: small.Group(), Consumer: "a", Streams: []string{small.Stream(), ">"}, Count: 16}).Result()
	must(t, e)
	ids := []string{}
	for _, m := range z[0].Messages {
		ids = append(ids, m.ID)
	}
	must(t, q.R.Do(ctx, append([]any{"XCLAIM", small.Stream(), small.Group(), "a", 0}, append(stringsToAny(ids), "IDLE", 2000)...)...).Err())
	claimed, e := small.Recover(ctx, "b", 100*time.Millisecond)
	must(t, e)
	if len(claimed) != 1 {
		t.Fatalf("serial worker claimed %d, want one", len(claimed))
	}
	pending, e := q.R.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: small.Stream(), Group: small.Group(), Start: "-", End: "+", Count: 20, Consumer: "b"}).Result()
	must(t, e)
	if len(pending) != 1 {
		t.Fatal("waiting batch assigned to busy worker", len(pending))
	}
}
func stringsToAny(xs []string) []any {
	out := []any{}
	for _, s := range xs {
		out = append(out, s)
	}
	return out
}
func TestReleaseRAGBoundaryAndIndex(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	doc := rag.Document{ID: d.ID(), Title: "fixture", Text: "synthetic", CreatedAt: time.Now()}
	_, e := s.DB.ExecContext(ctx, "INSERT INTO documents(id,user_id,body) VALUES(?,?,?)", doc.ID, u, d.JSON(doc))
	must(t, e)
	prefix := d.ID()[:12]
	tx, e := s.DB.BeginTx(ctx, nil)
	must(t, e)
	defer tx.Rollback()
	stmt, e := tx.PrepareContext(ctx, "INSERT INTO chunks(id,user_id,document_id,body) VALUES(?,?,?,?)")
	must(t, e)
	defer stmt.Close()
	for i := 0; i <= 10000; i++ {
		word := "unrelated"
		if i == 10000 {
			word = "needleunique"
		}
		c := rag.Chunk{ID: fmt.Sprintf("%s%020d", prefix, i), DocumentID: doc.ID, Title: "fixture", Text: word, Vector: rag.Embed(word), EmbeddingVersion: rag.EmbeddingVersion, Index: i}
		_, e = stmt.ExecContext(ctx, c.ID, u, doc.ID, d.JSON(c))
		must(t, e)
	}
	must(t, tx.Commit())
	r := &rag.Service{Store: s, Sem: make(chan struct{}, 2)}
	hits, e := r.Search(ctx, u, "needleunique", 5)
	if !errors.Is(e, rag.ErrCapacity) {
		t.Fatalf("expected explicit capacity error, hits=%d err=%v", len(hits), e)
	}
	if _, err := r.Ingest(ctx, u, rag.Document{Title: "over-capacity", Text: "one more chunk"}); !errors.Is(err, rag.ErrCapacity) {
		t.Fatal("ingest exceeded capacity", err)
	}
	var indexes int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='chunks' AND column_name='user_id' AND seq_in_index=1").Scan(&indexes))
	t.Logf("10001st matching chunk hits=%d owner-leading indexes=%d", len(hits), indexes)
	if indexes == 0 {
		t.Error("no owner-leading chunk index")
	}
	other, e := s.NewUser(ctx, d.ID()+"@rag.test", "unused")
	must(t, e)
	a, e := r.Ingest(ctx, other, rag.Document{Title: "duplicate", Text: "duplicatecontent"})
	must(t, e)
	b, e := r.Ingest(ctx, other, rag.Document{Title: "duplicate", Text: "duplicatecontent"})
	must(t, e)
	dups, e := r.Search(ctx, other, "duplicatecontent", 5)
	must(t, e)
	ownerHits, e := r.Search(ctx, u, "duplicatecontent", 5)
	if !errors.Is(e, rag.ErrCapacity) {
		t.Fatal(e)
	}
	t.Logf("duplicate document IDs different=%v duplicate hits=%d cross-owner hits=%d", a.ID != b.ID, len(dups), len(ownerHits))
	if len(ownerHits) != 0 {
		t.Error("owner filter violated")
	}
}
