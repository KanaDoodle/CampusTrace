package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

type preparationPause struct {
	tools *agent.Tools
	pause bool
	calls int
}

func (t *preparationPause) Definitions() []agent.Definition { return t.tools.Definitions() }
func (t *preparationPause) Execute(ctx context.Context, user, name string, raw json.RawMessage) (any, error) {
	t.calls++
	if t.pause {
		return nil, errors.New("synthetic preparation pause")
	}
	return t.tools.Execute(ctx, user, name, raw)
}

func TestPreparationCohesionFrozenApplicationReviewsAndKnowledgeFencing(t *testing.T) {
	ctx, s, q, user, _ := setup(t)
	must(t, s.SaveProfile(ctx, user, d.Profile{Languages: []string{"Go"}, Degree: "MASTER", GraduationYear: 2027}))
	input := p.Ingest{Company: "合成准备公司", Title: "后端工程师", ExternalID: d.ID(), JobType: "FULL_TIME", Text: "使用 Go 与 Redis Streams，关注 PEL 和 XAUTOCLAIM 消费恢复。", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
	observation, err := s.IngestForUser(ctx, user, input)
	must(t, err)
	raw, err := s.ApplyAction(ctx, user, d.ID(), "record_submitted_application", []byte(d.JSON(p.CreateArgs{JobID: observation.JobID})))
	must(t, err)
	var application d.Application
	must(t, json.Unmarshal(raw, &application))
	interview, err := s.SaveInterview(ctx, user, d.Interview{ApplicationID: application.ID, Round: 1, ScheduledAt: time.Now(), Result: "PENDING"})
	must(t, err)
	_, err = s.ApplyAction(ctx, user, d.ID(), "record_interview_review", []byte(d.JSON(d.Review{InterviewID: interview.ID, Questions: []string{"XAUTOCLAIM 如何恢复 Redis PEL？"}, Evaluation: "需要复习消息重领边界", Missed: []string{"Redis PEL 重领与持久化确认"}, Topics: []d.WeakCandidate{{Topic: "Redis Streams", Weight: 3, Evidence: "需要复习消息重领边界"}}})))
	must(t, err)
	service := &rag.Service{Store: s, Sem: make(chan struct{}, 2)}
	redisDoc, err := service.Ingest(ctx, user, rag.Document{Title: "Redis PEL", Text: "Redis Streams PEL 消息通过 XAUTOCLAIM 恢复，持久化后确认。"})
	must(t, err)
	_, err = service.Ingest(ctx, user, rag.Document{Title: "未来 Kafka", Text: "KAFKA_FUTURE_ONLY consumer partition offsets"})
	must(t, err)
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	_, err = service.Ingest(ctx, other, rag.Document{Title: "Redis 秘密笔记", Text: "Redis PEL FOREIGN_PRIVATE_NOTE 不得返回"})
	must(t, err)
	tools := &agent.Tools{Store: s, Queue: q, RAG: service}
	paused := &preparationPause{tools: tools, pause: true}
	runner := agent.SkillRunner{Store: s, Tools: paused}
	task, err := runner.Start(ctx, user, agent.SkillInput{SkillID: "application-prep", ApplicationID: application.ID, RequestKey: d.ID()})
	must(t, err)
	if task.State != "PAUSED" {
		t.Fatal(task)
	}
	before, _, err := s.AgentExecutionIdentity(ctx, user, "", application.ID, "application-prep")
	must(t, err)
	// Changing today's profile/JD must not silently change this application's
	// preparation materials or invalidate its otherwise unchanged checkpoint.
	must(t, s.SaveProfile(ctx, user, d.Profile{Languages: []string{"Python"}, Degree: "MASTER", GraduationYear: 2027}))
	input.Text, input.ObservedAt = "KAFKA_FUTURE_ONLY：使用 Python 与 Kafka 开发。", input.ObservedAt.Add(time.Minute)
	later, err := s.IngestForUser(ctx, user, input)
	must(t, err)
	if later.JobID != observation.JobID {
		t.Fatal("fixture did not update the same job")
	}
	after, _, err := s.AgentExecutionIdentity(ctx, user, "", application.ID, "application-prep")
	must(t, err)
	if before != after {
		t.Fatal("current profile/JD changed frozen application identity")
	}
	paused.pause = false
	task, err = runner.Resume(ctx, user, task.ID)
	must(t, err)
	if task.State != "COMPLETED" || len(task.Observations) != 1 {
		t.Fatal(task)
	}
	var output struct {
		Scope     string                 `json:"preparation_scope"`
		Context   string                 `json:"context_key"`
		Candidate string                 `json:"candidate_document"`
		Plan      agent.PreparationQuery `json:"retrieval_context"`
		Knowledge []rag.Hit              `json:"knowledge"`
	}
	must(t, json.Unmarshal(task.Observations[0].Data, &output))
	if output.Scope != "frozen_application" || output.Context == "" || !strings.Contains(output.Plan.Query, "XAUTOCLAIM") || strings.Contains(output.Plan.Query, "KAFKA_FUTURE_ONLY") || strings.Contains(output.Candidate, "Python") || len(output.Knowledge) == 0 || output.Knowledge[0].DocumentID != redisDoc.ID || strings.Contains(d.JSON(output), "FOREIGN_PRIVATE_NOTE") {
		t.Fatal(output)
	}
	if _, err = tools.PrepareApplication(ctx, other, application.ID); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("foreign application was readable", err)
	}
	paused.pause = true
	stale, err := runner.Start(ctx, user, agent.SkillInput{SkillID: "application-prep", ApplicationID: application.ID, RequestKey: d.ID()})
	must(t, err)
	must(t, service.DeleteDocument(ctx, user, redisDoc.ID))
	paused.pause = false
	calls := paused.calls
	stale, err = runner.Resume(ctx, user, stale.ID)
	must(t, err)
	if stale.State != "STALE" || len(stale.Observations) != 0 || paused.calls != calls {
		t.Fatal("deleted knowledge reused a checkpoint", stale)
	}
	// Missing historical materials remain unknown, even though today's job and
	// candidate profile are available. This read does not fabricate a snapshot.
	_, err = s.DB.ExecContext(ctx, "DELETE FROM application_contexts WHERE application_id=? AND user_id=?", application.ID, user)
	must(t, err)
	legacy, err := tools.PrepareApplication(ctx, user, application.ID)
	must(t, err)
	if text := d.JSON(legacy); !strings.Contains(text, `"legacy":true`) || strings.Contains(text, "KAFKA_FUTURE_ONLY") || strings.Contains(text, `"retrieval_context"`) {
		t.Fatal(legacy)
	}
}

func TestRetiredPracticeKeepsOwnedHistoryWithoutExecutionOrMutation(t *testing.T) {
	ctx, s, q, user, _ := setup(t)
	run := p.PracticeRun{ID: d.ID(), RequestKey: d.ID(), Code: "historic private code", Tests: "historic private tests", State: "RUNNING", Output: "historical record", CreatedAt: time.Now().Add(-time.Hour)}
	body := d.JSON(run)
	_, err := s.DB.ExecContext(ctx, "INSERT INTO practice_runs(id,user_id,request_key,fingerprint,state,created_at,body) VALUES(?,?,?,?,?,?,?)", run.ID, user, run.RequestKey, d.Hash(body), run.State, run.CreatedAt, body)
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-retired-practice-secret-32-bytes")}
	token, err := authn.Token(user)
	must(t, err)
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	foreign, err := authn.Token(other)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn}).Handler()
	owned := matchingRequest(handler, token, "/api/practice/runs", "GET", nil)
	if owned.Code != http.StatusOK || !strings.Contains(owned.Body.String(), run.Code) {
		t.Fatal(owned.Code, owned.Body.String())
	}
	denied := matchingRequest(handler, foreign, "/api/practice/runs", "GET", nil)
	if denied.Code != http.StatusOK || strings.Contains(denied.Body.String(), run.Code) {
		t.Fatal(denied.Code, denied.Body.String())
	}
	retired := matchingRequest(handler, token, "/api/practice/runs", "POST", map[string]any{"code": "must not run", "request_key": d.ID()})
	if retired.Code != http.StatusGone || !strings.Contains(retired.Body.String(), "PRACTICE_RETIRED") {
		t.Fatal(retired.Code, retired.Body.String())
	}
	var count int
	var stored string
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM practice_runs WHERE user_id=?", user).Scan(&count))
	must(t, s.DB.QueryRowContext(ctx, "SELECT JSON_UNQUOTE(JSON_EXTRACT(body,'$.state')) FROM practice_runs WHERE id=?", run.ID).Scan(&stored))
	if count != 1 || stored != "RUNNING" {
		t.Fatal("history read or retired POST mutated a record", count, stored)
	}
}
