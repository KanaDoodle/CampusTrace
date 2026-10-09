package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/mcpclient"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type checkpointTools struct {
	calls   map[string]int
	failure string
}

func (*checkpointTools) Definitions() []agent.Definition { return (&agent.Tools{}).Definitions() }
func (t *checkpointTools) Execute(ctx context.Context, user, name string, raw json.RawMessage) (any, error) {
	if t.calls == nil {
		t.calls = map[string]int{}
	}
	t.calls[name]++
	if name == t.failure {
		return nil, p.ErrValidation
	}
	return map[string]string{"notice": "synthetic local read", "private": "手机13800138000"}, nil
}
func TestAgentCheckpointResumeReplayAndStaleProfile(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	tools := &checkpointTools{failure: "get_closing_jobs"}
	runner := agent.SkillRunner{Store: s, Tools: tools}
	input := agent.SkillInput{SkillID: "daily-review", RequestKey: d.ID()}
	v, e := runner.Start(ctx, u, input)
	must(t, e)
	if v.State != "PAUSED" || v.Next != 1 || len(v.Observations) != 1 || !v.CanResume {
		t.Fatal(v)
	}
	tools.failure = ""
	v, e = runner.Resume(ctx, u, v.ID)
	must(t, e)
	if v.State != "COMPLETED" || v.Next != 4 || tools.calls["get_agent_todos"] != 1 || tools.calls["get_closing_jobs"] != 2 || v.CanResume {
		t.Fatal(v, tools.calls)
	}
	calls := d.JSON(tools.calls)
	same, e := runner.Start(ctx, u, input)
	must(t, e)
	if same.ID != v.ID || same.State != "COMPLETED" || calls != d.JSON(tools.calls) {
		t.Fatal("replay executed", same)
	}
	if strings.Contains(d.JSON(same), "13800138000") {
		t.Fatal("sensitive checkpoint persisted")
	}
	changed := input
	changed.SkillID = "review-plan"
	changed.Topic = "Go"
	if _, e = runner.Start(ctx, u, changed); !errors.Is(e, p.ErrConflict) {
		t.Fatal("nonce changed input", e)
	}
	tools.failure = "get_closing_jobs"
	input.RequestKey = d.ID()
	paused, e := runner.Start(ctx, u, input)
	must(t, e)
	before := tools.calls["get_agent_todos"]
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER", Skills: []string{"Go"}}))
	tools.failure = ""
	stale, e := runner.Resume(ctx, u, paused.ID)
	must(t, e)
	if stale.State != "STALE" || len(stale.Observations) != 0 || tools.calls["get_agent_todos"] != before {
		t.Fatal(stale)
	}
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, e)
	if _, e = s.AgentExecution(ctx, other, v.ID); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("owner access", e)
	}
	if _, _, e = s.ClaimAgentExecution(ctx, other, v.ID); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("owner continuation", e)
	}
}

func TestAgentAttemptLimitAndLeaseContinuation(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	tools := &checkpointTools{failure: "get_closing_jobs"}
	runner := agent.SkillRunner{Store: s, Tools: tools}
	v, e := runner.Start(ctx, u, agent.SkillInput{SkillID: "daily-review", RequestKey: d.ID()})
	must(t, e)
	stored, e := s.AgentExecution(ctx, u, v.ID)
	must(t, e)
	if !stored.CanResume || stored.LeaseUntil != nil {
		t.Fatal(stored)
	}
	for n := 1; n < p.MaxAgentExecutionAttempts; n++ {
		v, e = runner.Resume(ctx, u, v.ID)
		must(t, e)
	}
	if v.State != "FAILED" || v.Resumes != p.MaxAgentExecutionAttempts {
		t.Fatal(v)
	}
	stored, e = s.AgentExecution(ctx, u, v.ID)
	must(t, e)
	if stored.CanResume || stored.LeaseUntil != nil || stored.State != "FAILED" {
		t.Fatal(stored)
	}
	before := d.JSON(tools.calls)
	v, e = runner.Resume(ctx, u, v.ID)
	must(t, e)
	if v.State != "FAILED" || d.JSON(tools.calls) != before {
		t.Fatal("exhausted task reran", v)
	}
	// The actual lease is authoritative even if the JSON timestamp is old.
	v, e = runner.Start(ctx, u, agent.SkillInput{SkillID: "daily-review", RequestKey: d.ID()})
	must(t, e)
	_, lease, e := s.ClaimAgentExecution(ctx, u, v.ID)
	must(t, e)
	if lease == "" {
		t.Fatal("missing lease")
	}
	_, e = s.DB.ExecContext(ctx, "UPDATE agent_executions SET body=JSON_SET(body,'$.updated_at','2000-01-01T00:00:00Z') WHERE id=?", v.ID)
	must(t, e)
	stored, e = s.AgentExecution(ctx, u, v.ID)
	must(t, e)
	if stored.CanResume || stored.LeaseUntil == nil {
		t.Fatal("timestamp bypassed lease", stored)
	}
	_, e = s.DB.ExecContext(ctx, "UPDATE agent_executions SET lease_until=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=?", v.ID)
	must(t, e)
	rows, e := s.AgentExecutions(ctx, u)
	must(t, e)
	found := false
	for _, row := range rows {
		if row.ID == v.ID {
			found = true
			if !row.CanResume {
				t.Fatal(row)
			}
		}
	}
	if !found {
		t.Fatal("missing execution")
	}
}
func TestAgentLeaseFenceCancelAndForget(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	identity, revision, e := s.AgentExecutionIdentity(ctx, u, "", "")
	must(t, e)
	input := agent.SkillInput{SkillID: "daily-review", RequestKey: d.ID()}
	base := p.AgentExecution{SkillID: input.SkillID, SkillVersion: "1", HarnessVersion: agent.HarnessVersion, RequestKey: input.RequestKey, Fingerprint: d.Hash(d.JSON(input)), Input: json.RawMessage(d.JSON(input)), Identity: identity, MemoryRevision: revision}
	v, e := s.BeginAgentExecution(ctx, u, base)
	must(t, e)
	var wg sync.WaitGroup
	results := make(chan string, 6)
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token, e := s.ClaimAgentExecution(ctx, u, v.ID)
			if e != nil {
				errs <- e
			} else {
				results <- token
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	tokens := []string{}
	for token := range results {
		tokens = append(tokens, token)
	}
	if len(tokens) != 1 {
		t.Fatal(tokens)
	}
	for e := range errs {
		if !errors.Is(e, p.ErrConflict) {
			t.Fatal(e)
		}
	}
	claimed, e := s.AgentExecution(ctx, u, v.ID)
	must(t, e)
	must(t, s.CancelAgentExecution(ctx, u, v.ID))
	claimed.Next = 2
	if e = s.CheckpointAgentExecution(ctx, u, tokens[0], claimed); !errors.Is(e, p.ErrConflict) {
		t.Fatal("late worker after cancel", e)
	}
	note, e := s.SaveMemory(ctx, u, p.MemoryInput{Kind: "DECISION", Content: "先准备并发"})
	must(t, e)
	identity, revision, e = s.AgentExecutionIdentity(ctx, u, "", "")
	must(t, e)
	base.RequestKey = d.ID()
	base.MemoryRevision = revision
	base.Identity = identity
	v, e = s.BeginAgentExecution(ctx, u, base)
	must(t, e)
	claimed, lease, e := s.ClaimAgentExecution(ctx, u, v.ID)
	must(t, e)
	claimed.Observations = []p.ExecutionObservation{{Tool: "search_memories", Data: json.RawMessage(`{"content":"先准备并发"}`)}}
	must(t, s.CheckpointAgentExecution(ctx, u, lease, claimed))
	must(t, s.ForgetMemory(ctx, u, note.ID, note.Version))
	if e = s.CheckpointAgentExecution(ctx, u, lease, claimed); !errors.Is(e, p.ErrConflict) {
		t.Fatal("forgotten facts resurrected", e)
	}
	v, e = s.AgentExecution(ctx, u, v.ID)
	must(t, e)
	if v.State != "STALE" || strings.Contains(d.JSON(v), "先准备并发") || len(v.Observations) > 0 {
		t.Fatal(v)
	}
}
func TestAgentEventFeedIsolationAndCoalescing(t *testing.T) {
	ctx, s, _, u, source := setup(t)
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, e)
	feed, e := s.AgentTodos(ctx, u)
	must(t, e)
	if feed.Enabled {
		t.Fatal(feed)
	}
	must(t, s.SetAgentFeed(ctx, u, true))
	i := ingest(t, ctx, s, source)
	o, e := s.Ingest(ctx, i)
	must(t, e)
	private, e := s.IngestForUser(ctx, other, p.Ingest{Company: "PrivateCompany", Title: "私人岗位", JobType: "FULL_TIME", Locations: []string{"上海"}, ExternalID: d.ID(), Text: jd, FetchStatus: "SUCCESS"})
	must(t, e)
	_ = private
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "BACHELOR"}))
	project, e := s.SaveProject(ctx, u, d.Project{Name: "Synthetic Project", Description: "实现幂等消费"})
	must(t, e)
	project.Description = "实现并发消费"
	_, e = s.UpdateProject(ctx, u, project.ID, project)
	must(t, e)
	for n := 0; n < 2; n++ {
		must(t, s.RefreshAgentFeed(ctx, u))
	}
	feed, e = s.AgentTodos(ctx, u)
	must(t, e)
	profileCount, jobCount := 0, 0
	for _, v := range feed.Items {
		if v.JobID == private.JobID {
			t.Fatal("private job leaked")
		}
		if v.Kind == "PROFILE_CHANGED" {
			profileCount++
		}
		if v.JobID == o.JobID && v.Kind == "NEW_JOB" {
			jobCount++
		}
	}
	if profileCount != 1 || jobCount != 1 || len(feed.Items) != 2 {
		t.Fatal(feed)
	}
	must(t, s.DismissAgentTodo(ctx, u, feed.Items[0].ID))
	if e = s.DismissAgentTodo(ctx, other, feed.Items[1].ID); !errors.Is(e, p.ErrNotFound) {
		t.Fatal(e)
	}
	must(t, s.SetAgentFeed(ctx, u, false))
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER"}))
	must(t, s.RefreshAgentFeed(ctx, u))
	feed, e = s.AgentTodos(ctx, u)
	must(t, e)
	if feed.Enabled || len(feed.Items) != 1 {
		t.Fatal(feed)
	}
}
func TestAgentHarnessHTTPAndOpaqueMCPResources(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Skills: []string{"Go"}}))
	tools := &agent.Tools{Store: s}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-agent-harness-secret-more-than-32-bytes")}
	token, e := authn.Token(u)
	must(t, e)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Tools: tools}).Handler()
	response := matchingRequest(handler, token, "/api/agent/skills", "GET", nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = matchingRequest(handler, token, "/api/agent/executions", "POST", agent.SkillInput{SkillID: "daily-review", RequestKey: d.ID()})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var task p.AgentExecution
	must(t, json.Unmarshal(response.Body.Bytes(), &task))
	if task.State != "COMPLETED" {
		t.Fatal(task)
	}
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, e)
	otherToken, e := authn.Token(other)
	must(t, e)
	response = matchingRequest(handler, otherToken, "/api/agent/executions/"+task.ID, "GET", nil)
	if response.Code != http.StatusNotFound {
		t.Fatal(response.Code)
	}
	uri := "learn://private/test@example.com/go"
	cfg, e := s.SaveAgentConnector(ctx, u, mcpclient.Connector{Name: "Go复习", URL: "https://example.com/mcp", Allowed: []string{uri}, Names: map[string]string{uri: "Go并发"}})
	must(t, e)
	args := json.RawMessage(`{"connector_id":"` + cfg.ID + `"}`)
	v, e := tools.Execute(ctx, u, "list_mcp_resources", args)
	must(t, e)
	text := d.JSON(v)
	if strings.Contains(text, uri) || strings.Contains(text, "test@example.com") || !strings.Contains(text, d.Hash(uri)) || !strings.Contains(text, "Go并发") {
		t.Fatal(text)
	}
	if _, e = tools.Execute(ctx, other, "list_mcp_resources", args); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("connector crossed owner", e)
	}
	if _, e = tools.Execute(ctx, u, "read_mcp_resource", json.RawMessage(`{"connector_id":"`+cfg.ID+`","resource_id":"`+strings.Repeat("a", 64)+`"}`)); !errors.Is(e, p.ErrValidation) {
		t.Fatal("unauthorized resource", e)
	}
	cfg.Name = "changed"
	cfg.Version = 0
	if _, e = s.SaveAgentConnector(ctx, u, cfg); !errors.Is(e, p.ErrConflict) {
		t.Fatal("connector version", e)
	}
}

func TestAgentClosingReadSurvivesLargeUnrelatedCatalog(t *testing.T) {
	ctx, s, _, u, source := setup(t)
	i := ingest(t, ctx, s, source)
	i.Text = jd + "\ndeadline: " + time.Now().UTC().Add(5*24*time.Hour).Format("2006-01-02")
	o, e := s.Ingest(ctx, i)
	must(t, e)
	claims, e := analysis.Extract(ctx, o.Text)
	must(t, e)
	bound, e := s.BindAnalysis(ctx, p.NewTask("ANALYZE", o.ID), d.ProcessingVersion(d.AnalysisVersion, d.ParserVersion, o.ParserVersion))
	must(t, e)
	must(t, s.PersistAnalysis(ctx, o.ID, bound.ProcessingVersion, claims))
	job, e := s.Job(ctx, o.JobID)
	must(t, e)
	must(t, s.Tx(ctx, func(tx *sql.Tx) error {
		for n := 0; n < 501; n++ {
			j := d.Job{ID: d.ID(), CompanyID: job.CompanyID, Company: job.Company, Title: "Unrelated test catalog", JobType: "FULL_TIME", Visibility: "PRIVATE", OwnerID: u, Fingerprint: d.Hash(d.ID())}
			if _, e := tx.ExecContext(ctx, "INSERT INTO jobs(id,company_id,fingerprint,visibility,owner_id,body) VALUES(?,?,?,?,?,?)", j.ID, j.CompanyID, j.Fingerprint, j.Visibility, u, d.JSON(j)); e != nil {
				return e
			}
		}
		return nil
	}))
	rows, e := s.ClosingJobs(ctx, u, 7)
	must(t, e)
	if len(rows) != 1 || rows[0].Job.ID != o.JobID {
		t.Fatal(rows)
	}
}
