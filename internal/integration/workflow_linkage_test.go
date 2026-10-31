package integration

import (
	"context"
	"encoding/json"
	"net/http"
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

func linkageApplication(t *testing.T, ctx context.Context, s *p.Store, user, job string) d.Application {
	t.Helper()
	must(t, s.SaveProfile(ctx, user, d.Profile{Languages: []string{"Go"}, Degree: "MASTER", GraduationYear: 2027}))
	body, err := s.ApplyAction(ctx, user, d.ID(), "record_submitted_application", []byte(d.JSON(p.CreateArgs{JobID: job})))
	must(t, err)
	var app d.Application
	must(t, json.Unmarshal(body, &app))
	return app
}

func TestWorkflowLinkageReviewMaterialProvenanceDedupAndFencing(t *testing.T) {
	ctx, s, q, user, source := setup(t)
	job, err := s.Ingest(ctx, p.Ingest{Company: "合成联动公司", Title: "Go 后端", SourceID: source, ExternalID: d.ID(), JobType: "FULL_TIME", Text: "使用 Redis Streams 与 Go 开发，关注 PEL 和 XAUTOCLAIM。", FetchStatus: "SUCCESS"})
	must(t, err)
	app := linkageApplication(t, ctx, s, user, job.JobID)
	interview, err := s.SaveInterview(ctx, user, d.Interview{ApplicationID: app.ID, Round: 2, ScheduledAt: time.Now(), Result: "PENDING"})
	must(t, err)
	_, err = s.ApplyAction(ctx, user, d.ID(), "record_interview_review", []byte(d.JSON(d.Review{InterviewID: interview.ID, Questions: []string{"XAUTOCLAIM 如何恢复 Redis PEL？"}, Evaluation: "需要复习重领边界", Missed: []string{"Redis PEL 重领"}, FollowUp: "复习持久化后确认", Topics: []d.WeakCandidate{}})))
	must(t, err)
	service := &rag.Service{Store: s, Sem: make(chan struct{}, 8)}
	tools := &agent.Tools{Store: s, Queue: q, RAG: service}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-workflow-links-secret-32bytes")}
	token, err := authn.Token(user)
	must(t, err)
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	foreign, err := authn.Token(other)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Tools: tools}).Handler()
	path := "/api/interviews/" + interview.ID
	preview := matchingRequest(handler, token, path+"/learning-draft", "GET", nil)
	if preview.Code != 200 {
		t.Fatal(preview.Code, preview.Body.String())
	}
	var draft rag.ReviewMaterialDraft
	must(t, json.Unmarshal(preview.Body.Bytes(), &draft))
	if draft.Origin.ApplicationID != app.ID || !strings.Contains(draft.Text, "XAUTOCLAIM") {
		t.Fatal(draft)
	}
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents WHERE user_id=?", user).Scan(&count))
	if count != 0 {
		t.Fatal("preview wrote a document")
	}
	if got := matchingRequest(handler, foreign, path+"/learning-draft", "GET", nil); got.Code != 404 {
		t.Fatal(got.Code)
	}
	// A task paused before its first read must not reuse pre-import knowledge.
	paused := &preparationPause{tools: tools, pause: true}
	runner := agent.SkillRunner{Store: s, Tools: paused}
	task, err := runner.Start(ctx, user, agent.SkillInput{SkillID: "application-prep", ApplicationID: app.ID, RequestKey: d.ID()})
	must(t, err)
	input := map[string]any{"source_key": draft.Origin.SourceKey, "title": draft.Title, "text": draft.Text + "\n补充学习：保存后再确认。"}
	saved := matchingRequest(handler, token, path+"/learning-material", "POST", input)
	if saved.Code != 200 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	var receipt struct {
		ID     string `json:"id"`
		Reused bool   `json:"reused"`
	}
	must(t, json.Unmarshal(saved.Body.Bytes(), &receipt))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc, reused, err := service.ImportReviewMaterial(ctx, user, interview.ID, draft.Origin.SourceKey, draft.Title, input["text"].(string))
			if err != nil || !reused || doc.ID != receipt.ID {
				t.Errorf("duplicate import: %s %v %v", doc.ID, reused, err)
			}
		}()
	}
	wg.Wait()
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents WHERE user_id=?", user).Scan(&count))
	if count != 1 {
		t.Fatal("duplicate documents", count)
	}
	paused.pause = false
	task, err = runner.Resume(ctx, user, task.ID)
	must(t, err)
	if task.State != "STALE" {
		t.Fatal("import did not fence old preparation", task.State)
	}
	doc := matchingRequest(handler, token, "/api/documents/"+receipt.ID, "GET", nil)
	if doc.Code != 200 || !strings.Contains(doc.Body.String(), draft.Origin.SourceKey) {
		t.Fatal(doc.Code, doc.Body.String())
	}
	for _, route := range []string{"/api/documents", "/api/documents/import"} {
		got := matchingRequest(handler, token, route, "POST", map[string]any{"title": "伪造来源", "text": "学习材料", "origin": draft.Origin})
		if got.Code != 400 {
			t.Fatal("generic import accepted forged provenance", route, got.Code)
		}
	}
	if got := matchingRequest(handler, foreign, path+"/learning-material", "POST", input); got.Code != 404 {
		t.Fatal("foreign review import", got.Code)
	}
	if got := matchingRequest(handler, foreign, "/api/documents/"+receipt.ID, "GET", nil); got.Code != 404 {
		t.Fatal("foreign document read", got.Code)
	}
	result, err := tools.PrepareApplication(ctx, user, app.ID)
	must(t, err)
	if !strings.Contains(d.JSON(result), receipt.ID) {
		t.Fatal("preparation did not retrieve imported review", d.JSON(result))
	}
	must(t, service.DeleteDocument(ctx, user, receipt.ID))
	again, _, err := service.ImportReviewMaterial(ctx, user, interview.ID, draft.Origin.SourceKey, draft.Title, input["text"].(string))
	must(t, err)
	if again.ID == receipt.ID {
		t.Fatal("deleted document resurrected")
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE reviews SET body=JSON_SET(body,'$.self_evaluation','更新后的实际复盘') WHERE interview_id=? AND user_id=?", interview.ID, user)
	must(t, err)
	stale := matchingRequest(handler, token, path+"/learning-material", "POST", input)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "REVIEW_MATERIAL_CHANGED") {
		t.Fatal(stale.Code, stale.Body.String())
	}
}

func TestWorkflowLinkageRemindersReadCurrentOwnedApplication(t *testing.T) {
	ctx, s, _, user, source := setup(t)
	must(t, s.SetAgentFeed(ctx, user, true))
	job, err := s.Ingest(ctx, p.Ingest{Company: "合成提醒公司", Title: "后端", SourceID: source, ExternalID: d.ID(), JobType: "FULL_TIME", Text: "Go 与 Redis 后端。", FetchStatus: "SUCCESS"})
	must(t, err)
	app := linkageApplication(t, ctx, s, user, job.JobID)
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	foreign := linkageApplication(t, ctx, s, other, job.JobID)
	must(t, s.RefreshAgentFeed(ctx, user))
	feed, err := s.AgentTodos(ctx, user)
	must(t, err)
	linked := func(feed p.AgentFeed) p.AgentTodo {
		for _, item := range feed.Items {
			if item.JobID == job.JobID {
				return item
			}
		}
		return p.AgentTodo{}
	}
	item := linked(feed)
	if len(item.Applications) != 1 || item.Applications[0].ID != app.ID || strings.Contains(d.JSON(feed), foreign.ID) {
		t.Fatal(feed)
	}
	notice := d.Notification{ID: d.ID(), UserID: user, Type: "JD_CHANGED", EntityType: "JOB", EntityID: job.JobID, Title: "岗位已变化", Body: "核对留档", CreatedAt: time.Now().UTC()}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO notifications(id,user_id,dedup_key,created_at,body) VALUES(?,?,?,?,?)", notice.ID, user, d.Hash(notice.ID), notice.CreatedAt, d.JSON(notice))
	must(t, err)
	notices, err := s.Notifications(ctx, user)
	must(t, err)
	if len(notices) != 1 || len(notices[0].Applications) != 1 || notices[0].Applications[0].ID != app.ID {
		t.Fatal(notices)
	}
	_, err = s.ApplyAction(ctx, user, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, State: "WITHDRAWN", Version: app.Version})))
	must(t, err)
	feed, err = s.AgentTodos(ctx, user)
	must(t, err)
	if linked(feed).Applications[0].State != "WITHDRAWN" {
		t.Fatal("projection used old event-time state", feed)
	}
	notices, err = s.Notifications(ctx, user)
	must(t, err)
	if notices[0].Applications[0].State != "WITHDRAWN" || notices[0].ReadAt != nil {
		t.Fatal("read mutated notification", notices)
	}
	var raw string
	must(t, s.DB.QueryRowContext(ctx, "SELECT body FROM agent_todos WHERE id=? AND user_id=?", item.ID, user).Scan(&raw))
	if strings.Contains(raw, "related_applications") {
		t.Fatal("read persisted a stale relationship", raw)
	}
}
