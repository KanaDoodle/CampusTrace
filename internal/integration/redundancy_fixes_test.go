package integration

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestDocumentSaveAliasesDeduplicateAndPreserveCompatibility(t *testing.T) {
	ctx, s, q, user, _ := setup(t)
	service := &rag.Service{Store: s, Sem: make(chan struct{}, 8)}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-document-alias-secret-32bytes")}
	token, err := authn.Token(user)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Tools: &agent.Tools{Store: s, Queue: q, RAG: service}}).Handler()
	body := map[string]any{"title": "重复请求的学习笔记", "text": "Redis PEL ACK XAUTOCLAIM 消费恢复"}
	first := matchingRequest(handler, token, "/api/documents", "POST", body)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var original rag.Document
	must(t, json.Unmarshal(first.Body.Bytes(), &original))
	if original.ID == "" || original.Text != body["text"] || original.CreatedAt.IsZero() {
		t.Fatal("compatibility result lost Document fields", original)
	}
	revision, err := s.KnowledgeIdentity(ctx, user)
	must(t, err)
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			route := "/api/documents"
			if i%2 == 0 {
				route += "/import"
			}
			response := matchingRequest(handler, token, route, "POST", body)
			if response.Code != 200 {
				t.Errorf("save %s: %d %s", route, response.Code, response.Body.String())
				return
			}
			var result struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.ID != original.ID {
				t.Errorf("duplicate identity: %s %v", response.Body.String(), err)
			}
		}(i)
	}
	wg.Wait()
	again := matchingRequest(handler, token, "/api/documents", "POST", body)
	var replay rag.Document
	must(t, json.Unmarshal(again.Body.Bytes(), &replay))
	if !reflect.DeepEqual(original, replay) {
		t.Fatal("old endpoint replay changed its full Document contract", original, replay)
	}
	current, err := s.KnowledgeIdentity(ctx, user)
	must(t, err)
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents WHERE user_id=?", user).Scan(&count))
	if count != 1 || revision != current {
		t.Fatal("replay created documents or invalidated checkpoints", count, revision, current)
	}
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	foreign, _, err := service.ImportDocument(ctx, other, rag.Document{Title: body["title"].(string), Text: body["text"].(string)})
	must(t, err)
	if foreign.ID == original.ID {
		t.Fatal("deduplication crossed accounts")
	}
}

func TestReviewImportRejectsSourceChangedDuringRequest(t *testing.T) {
	ctx, s, _, user, source := setup(t)
	job, err := s.Ingest(ctx, p.Ingest{Company: "合成事务公司", Title: "Go 后端", SourceID: source, ExternalID: d.ID(), JobType: "FULL_TIME", Text: "Redis PEL 消费恢复。", FetchStatus: "SUCCESS"})
	must(t, err)
	app := linkageApplication(t, ctx, s, user, job.JobID)
	interview, err := s.SaveInterview(ctx, user, d.Interview{ApplicationID: app.ID, Round: 1, ScheduledAt: time.Now(), Result: "PENDING"})
	must(t, err)
	_, err = s.ApplyAction(ctx, user, d.ID(), "record_interview_review", []byte(d.JSON(d.Review{InterviewID: interview.ID, Questions: []string{"如何恢复 PEL？"}, Evaluation: "初始复盘", Missed: []string{}, Topics: []d.WeakCandidate{}})))
	must(t, err)
	service := &rag.Service{Store: s}
	draft, err := service.ReviewMaterialDraft(ctx, user, interview.ID)
	must(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	service.Allow = func(ctx context.Context) (bool, error) {
		close(entered)
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	must(t, err)
	defer tx.Rollback()
	var locked string
	must(t, tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&locked))
	done := make(chan error, 1)
	go func() {
		_, _, err := service.ImportReviewMaterial(ctx, user, interview.ID, draft.Origin.SourceKey, draft.Title, draft.Text)
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err = tx.ExecContext(ctx, "UPDATE reviews SET body=JSON_SET(body,'$.self_evaluation','请求进行中的复盘修正') WHERE user_id=? AND interview_id=?", user, interview.ID)
	must(t, err)
	must(t, tx.Commit())
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, p.ErrStaleInput) {
			t.Fatal("stale review imported after concurrent source update", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents WHERE user_id=?", user).Scan(&count))
	if count != 0 {
		t.Fatal("stale source wrote a document", count)
	}
}

func TestSharedChunkingAndLegacyVectorReadCompatibility(t *testing.T) {
	ctx, s, _, user, _ := setup(t)
	service := &rag.Service{Store: s}
	text := strings.Repeat("甲", 700) + strings.Repeat("乙", 200) + strings.Repeat("丙", 500) + "🦀 Redis"
	doc, err := service.Ingest(ctx, user, rag.Document{Title: "Unicode 分段", Text: text})
	must(t, err)
	stored, err := p.Many[rag.Chunk](ctx, s.DB, "SELECT body FROM chunks WHERE user_id=? AND document_id=? ORDER BY JSON_EXTRACT(body,'$.index')", user, doc.ID)
	must(t, err)
	expected := (rag.Dataset{Documents: []rag.EvalDocument{{ID: doc.ID, Title: doc.Title, Text: text}}}).Chunks()
	if len(stored) != 3 || len(stored) != len(expected) {
		t.Fatal("unexpected boundary chunks", len(stored), len(expected))
	}
	for i, c := range stored {
		if c.Vector != nil || c.Text != expected[i].Text || c.Index != i || c.EmbeddingVersion != rag.EmbeddingVersion {
			t.Fatal("new storage and evaluation diverged", c, expected[i])
		}
	}
	before, err := service.SearchDetailed(ctx, user, "Redis", 5)
	must(t, err)
	// Old JSON remains readable without migrating or deleting its hash vector.
	legacy := stored[2]
	legacy.Vector = make([]float64, 128)
	legacy.Vector[7] = 123
	_, err = s.DB.ExecContext(ctx, "UPDATE chunks SET body=? WHERE user_id=? AND id=?", d.JSON(legacy), user, legacy.ID)
	must(t, err)
	after, err := service.SearchDetailed(ctx, user, "Redis", 5)
	must(t, err)
	if len(after.Hits) == 0 || !reflect.DeepEqual(before.Hits, after.Hits) {
		t.Fatal("legacy hash changed retrieval", before.Hits, after.Hits)
	}
}
