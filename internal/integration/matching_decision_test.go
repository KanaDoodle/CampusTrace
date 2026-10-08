package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestDecisionViewsUseCurrentOwnedResultsWithoutModelCalls(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	profile := d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}}
	must(t, s.SaveProfile(ctx, u, profile))
	ids := []string{}
	for i := 0; i < 3; i++ {
		o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Decision fixture", Title: "服务端开发", JobType: "FULL_TIME", Locations: []string{"上海市"}, ExternalID: d.ID(), Text: "掌握 Go，开发服务", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		ids = append(ids, o.JobID)
	}
	authn := auth.Service{Store: s, Secret: []byte("decision-synthetic-secret-32-bytes")}
	token, err := authn.Token(u)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	snap, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", nil)
	must(t, err)
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": ids[:2], "candidate_hash": snap.CandidateHash})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	initialCalls := model.calls.Load()
	inventory, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", nil)
	must(t, err)
	ranked := 0
	for _, row := range inventory.Jobs {
		if row.State == "ANALYZED" {
			ranked++
			if row.Priority == nil || row.CompanyPlacement == nil || row.CompanyPlacement.Rank != 1 || row.CompanyPlacement.Total != 2 || row.CompanyPlacement.Pending != 1 || !row.CompanyPlacement.Tied {
				t.Fatal("inventory does not expose scoped application ordering", row.Job.ID, row.Priority, row.CompanyPlacement)
			}
		} else if row.Priority != nil || row.CompanyPlacement != nil {
			t.Fatal("pending job has a current rank")
		}
	}
	if ranked != 2 || model.calls.Load() != initialCalls {
		t.Fatal("ranking required additional model calls")
	}
	compareBody := map[string]any{"company": "Decision fixture"}
	compare := func(token string, body any) matching.CompanyComparison {
		t.Helper()
		rec := matchingRequest(handler, token, "/api/matching/company", "POST", body)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing private cache guard")
		}
		var v matching.CompanyComparison
		must(t, json.Unmarshal(rec.Body.Bytes(), &v))
		return v
	}
	report := compare(token, compareBody)
	if report.Total != 3 || report.Pending != 1 || report.Analyzed != 2 || len(report.RecommendedIDs) != 2 {
		t.Fatalf("bad comparison: %+v", report)
	}
	agentTools := &agent.Tools{Store: s, Queue: q, MatchModel: matching.ModelIdentity("server-default", "fixture")}
	read, err := agentTools.MatchResult(ctx, u, ids[0])
	must(t, err)
	current := read.(map[string]any)
	if current["state"] != "ANALYZED" || current["score"] == nil || strings.Contains(d.JSON(current), "candidate_facts") {
		t.Fatal("agent deep match is missing or exposes full profile", current)
	}
	otherModelTools := *agentTools
	otherModelTools.MatchModel = matching.ModelIdentity("different-provider", "fixture")
	otherModelRead, err := otherModelTools.MatchResult(ctx, u, ids[0])
	must(t, err)
	otherModelView := otherModelRead.(map[string]any)
	if otherModelView["state"] != "STALE" || otherModelView["score"] != nil {
		t.Fatal("agent exposed a result from a different selected model", otherModelView)
	}
	view, err := agentTools.CompareCompanyJobs(ctx, u, "Decision fixture", nil)
	must(t, err)
	comparison := view.(map[string]any)
	if comparison["total"] != 3 || comparison["pending"] != 1 || comparison["recommended_count"] != 2 {
		t.Fatal("agent company comparison differs from workspace", comparison)
	}
	if _, err := agentTools.CompareCompanyJobs(ctx, u, "", []string{ids[0], ids[1]}); err != nil {
		t.Fatal("selected comparison", err)
	}
	chatTools := &agent.Tools{Store: s, Queue: q}
	chat := &agent.Runtime{Model: agent.DemoModel{}, Tools: chatTools, R: q.R, Prefix: q.Prefix, MaxModels: 4, MaxTools: 8, Deadline: 8 * time.Second, ToolTimeout: 5 * time.Second}
	chatHandler := (&transport.API{Store: s, Queue: q, Tools: chatTools, Agent: chat, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	chatRec := matchingRequest(chatHandler, token, "/agent/decide", "POST", map[string]any{"session_id": "decision-test", "message": "查看岗位 " + ids[0] + " 的深度匹配"})
	if chatRec.Code != 200 {
		t.Fatal(chatRec.Code, chatRec.Body.String())
	}
	var chatAnswer agent.Result
	must(t, json.Unmarshal(chatRec.Body.Bytes(), &chatAnswer))
	if chatAnswer.Terminal != "COMPLETED" || !strings.Contains(chatAnswer.Answer, "核心匹配度") || !strings.Contains(chatRec.Body.String(), "get_match_result") {
		t.Fatal("agent route did not read current matching result", chatRec.Body.String())
	}
	chatRec = matchingRequest(chatHandler, token, "/agent/decide", "POST", map[string]any{"session_id": "decision-test", "message": "请对比这两个岗位 " + ids[0] + " " + ids[1]})
	if chatRec.Code != 200 || !strings.Contains(chatRec.Body.String(), "compare_company_jobs") {
		t.Fatal("agent route did not compare selected jobs", chatRec.Code, chatRec.Body.String())
	}
	chatRec = matchingRequest(chatHandler, token, "/agent/decide", "POST", map[string]any{"session_id": "decision-test", "message": "请比较 Decision fixture 的后端岗位"})
	if chatRec.Code != 200 || !strings.Contains(chatRec.Body.String(), "compare_company_jobs") || !strings.Contains(chatRec.Body.String(), "Decision fixture") {
		t.Fatal("offline agent did not compare company jobs", chatRec.Code, chatRec.Body.String())
	}
	prepPath := "/api/matching/preparation/" + ids[0]
	rec = matchingRequest(handler, token, prepPath, "POST", map[string]any{})
	var plan matching.PreparationPlan
	must(t, json.Unmarshal(rec.Body.Bytes(), &plan))
	if rec.Code != 200 || plan.State != "ANALYZED" || len(plan.Tasks) == 0 || plan.InputKey == "" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, task := range plan.Tasks {
		if task.Category == "REQUIRED" && (len(task.Evidence) == 0 || task.Evidence[0].Excerpt != "Go") {
			t.Fatal("missing exact source", task)
		}
	}
	report = compare(token, map[string]any{"company": "Decision fixture", "scope": "SELECTED", "job_ids": ids[:1]})
	if report.Scope != "SELECTED" || report.Total != 1 || report.Pending != 0 {
		t.Fatal("selection lost")
	}
	for _, body := range []any{map[string]any{"company": "Other", "job_ids": ids[:1]}, map[string]any{"company": "Decision fixture", "job_ids": []string{ids[0], ids[0]}}} {
		rec = matchingRequest(handler, token, "/api/matching/company", "POST", body)
		if rec.Code == 200 {
			t.Fatal("invalid scope accepted")
		}
	}
	rec = matchingRequest(handler, "", "/api/matching/company", "POST", compareBody)
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("anonymous report", rec.Code)
	}
	other, err := s.NewUser(ctx, d.ID()+"@decision-other.invalid", "unused")
	must(t, err)
	must(t, s.SaveProfile(ctx, other, profile))
	otherToken, err := authn.Token(other)
	must(t, err)
	if _, err := agentTools.MatchResult(ctx, other, ids[0]); err == nil {
		t.Fatal("agent read another user's private match")
	}
	if compare(otherToken, compareBody).Total != 0 {
		t.Fatal("private company counts leaked")
	}
	rec = matchingRequest(handler, otherToken, prepPath, "POST", map[string]any{})
	if rec.Code != 404 {
		t.Fatal("private evidence leaked", rec.Code)
	}
	profile.Languages = []string{"Java"}
	must(t, s.SaveProfile(ctx, u, profile))
	read, err = agentTools.MatchResult(ctx, u, ids[0])
	must(t, err)
	stale := read.(map[string]any)
	if stale["state"] != "STALE" || stale["score"] != nil || strings.Contains(d.JSON(stale), "\"strengths\"") {
		t.Fatal("agent exposed stale score or evidence", stale)
	}
	view, err = agentTools.CompareCompanyJobs(ctx, u, "Decision fixture", nil)
	must(t, err)
	comparison = view.(map[string]any)
	if comparison["stale"] != 2 || comparison["recommended_count"] != 0 {
		t.Fatal("agent recommended stale jobs", comparison)
	}
	report = compare(token, compareBody)
	if report.Stale != 2 || len(report.RecommendedIDs) != 0 {
		t.Fatal("changed profile used cached recommendation")
	}
	for _, row := range report.Jobs {
		if row.Score != nil || len(row.Strengths) != 0 || len(row.Gaps) != 0 {
			t.Fatal("stale evidence used")
		}
	}
	rec = matchingRequest(handler, token, prepPath, "POST", map[string]any{})
	must(t, json.Unmarshal(rec.Body.Bytes(), &plan))
	if plan.State != "STALE" || len(plan.Tasks) != 0 || plan.InputKey != "" || strings.Contains(rec.Body.String(), "\"excerpt\":\"Go\"") {
		t.Fatal("stale preparation", rec.Body.String())
	}
	if model.calls.Load() != initialCalls {
		t.Fatal("decision reading called the model")
	}
	quotaAfter, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", nil)
	must(t, err)
	if quotaAfter.CallsToday != int(initialCalls) {
		t.Fatal("read-only decision consumed quota", quotaAfter.CallsToday, initialCalls)
	}
}
