package agent

import (
	"context"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

// These projections keep full candidate profiles and raw job descriptions out
// of the conversational tool result. The underlying decision snapshot is the
// same read-only, version-aware path used by the matching workspace.
type matchPoint struct {
	RequirementID      string `json:"requirement_id"`
	Requirement        string `json:"requirement"`
	RequirementExcerpt string `json:"requirement_excerpt"`
	Result             string `json:"result"`
	FactID             string `json:"fact_id,omitempty"`
	FactExcerpt        string `json:"fact_excerpt,omitempty"`
}

func (t *Tools) safeText(s string) string {
	if t.MaskName != "" {
		s = strings.ReplaceAll(s, t.MaskName, "[已遮盖姓名]")
	}
	s = resume.Redact(s)
	r := []rune(s)
	if len(r) > 180 {
		return string(r[:180]) + "…"
	}
	return s
}

func (t *Tools) matchPoints(tasks []matching.PreparationTask) []matchPoint {
	out := make([]matchPoint, 0, 2)
	for _, task := range tasks {
		if len(out) == 2 {
			break
		}
		if task.Category == "QUALIFICATION" {
			continue
		}
		point := matchPoint{RequirementID: task.SelectedRequirementID, Requirement: t.safeText(task.Title), Result: task.Result}
		for _, req := range task.Requirements {
			if req.ID == task.SelectedRequirementID {
				point.RequirementExcerpt = t.safeText(req.Excerpt)
				break
			}
		}
		if len(task.Evidence) > 0 {
			point.FactID = task.Evidence[0].ID
			point.FactExcerpt = t.safeText(task.Evidence[0].Excerpt)
		}
		out = append(out, point)
	}
	return out
}

func (t *Tools) MatchResult(ctx context.Context, user, id string) (any, error) {
	if t.MatchModel == "" {
		return nil, p.ErrValidation
	}
	s, err := t.Store.MatchDecisionSnapshot(ctx, user, t.MatchModel, t.MaskName, []string{id}, "")
	if err != nil {
		return nil, err
	}
	row := s.Jobs[0]
	out := map[string]any{"job_id": row.Job.ID, "company": t.safeText(row.Job.Company), "title": t.safeText(row.Job.Title), "job_status": row.Job.CurrentStatus, "state": row.State, "notice": "仅读取已有分析；没有发起模型调用。"}
	if row.State != "ANALYZED" || row.Result == nil {
		out["notice"] = "当前模型、资料或岗位原文下没有可用的深度分析；过期结果不参与建议，请在岗位库核对并按需更新。"
		return out, nil
	}
	plan := matching.BuildPreparation(matching.DecisionInput{Job: row.Job, State: row.State, ExcludedReason: row.ExcludedReason, Local: row.Local, Result: row.Result}, s.Profile, time.Now().UTC())
	out["score"], out["coverage"], out["eligibility"] = plan.Score, plan.Coverage, plan.Eligibility.Status
	out["analyzed_at"], out["locally_refreshed"], out["evidence_reviews"] = plan.AnalyzedAt, row.Result.LocallyRefreshed, plan.EvidenceReviews
	out["strengths"], out["gaps"] = t.matchPoints(filterTasks(plan.Tasks, "DIRECT")), t.matchPoints(filterTasks(plan.Tasks, "GAP"))
	if plan.Notice != "" {
		out["notice"] = t.safeText(plan.Notice)
	}
	return out, nil
}

func filterTasks(tasks []matching.PreparationTask, result string) []matching.PreparationTask {
	out := make([]matching.PreparationTask, 0, len(tasks))
	for _, task := range tasks {
		if task.Category != "REQUIRED" && task.Category != "RESPONSIBILITY" {
			continue
		}
		if (result == "DIRECT" && task.Result == "DIRECT") || (result == "GAP" && task.Result != "DIRECT") {
			out = append(out, task)
		}
	}
	return out
}

func (t *Tools) CompareCompanyJobs(ctx context.Context, user, company string, ids []string) (any, error) {
	if t.MatchModel == "" {
		return nil, p.ErrValidation
	}
	company = strings.TrimSpace(company)
	s, err := t.Store.MatchDecisionSnapshot(ctx, user, t.MatchModel, t.MaskName, ids, company)
	if err != nil {
		return nil, err
	}
	if company == "" {
		if len(s.Jobs) == 0 {
			return nil, p.ErrNotFound
		}
		company = s.Jobs[0].Job.Company
	}
	for _, job := range s.Jobs {
		if job.Job.Company != company {
			return nil, p.ErrValidation
		}
	}
	inputs := make([]matching.DecisionInput, 0, len(s.Jobs))
	for _, job := range s.Jobs {
		inputs = append(inputs, matching.DecisionInput{Job: job.Job, State: job.State, ExcludedReason: job.ExcludedReason, Local: job.Local, Result: job.Result})
	}
	scope := "ALL"
	if len(ids) > 0 {
		scope = "SELECTED"
	}
	report := matching.BuildCompanyComparison(company, scope, inputs, s.Profile, time.Now().UTC())
	rows := make([]map[string]any, 0, min(len(report.Jobs), 8))
	for _, job := range report.Jobs[:min(len(report.Jobs), 8)] {
		row := map[string]any{"job_id": job.Job.ID, "title": t.safeText(job.Job.Title), "job_status": job.Job.CurrentStatus, "state": job.State, "recommended": job.Recommended, "blocked_reason": t.safeText(job.BlockedReason)}
		if job.State == "ANALYZED" {
			row["score"], row["coverage"], row["eligibility"] = job.Score, job.Coverage, job.Eligibility
			row["direction"], row["city_preference"] = job.Direction, job.City
			strengths, gaps := t.matchPoints(job.Strengths), t.matchPoints(job.Gaps)
			if len(strengths) > 1 {
				strengths = strengths[:1]
			}
			if len(gaps) > 1 {
				gaps = gaps[:1]
			}
			row["strengths"], row["gaps"] = strengths, gaps
			row["evidence_reviews"] = job.EvidenceReviews
		}
		rows = append(rows, row)
	}
	reasons := make([]string, 0, len(report.Reasons))
	for _, reason := range report.Reasons {
		reasons = append(reasons, t.safeText(reason))
	}
	return map[string]any{"company": t.safeText(company), "scope": scope, "total": report.Total, "analyzed": report.Analyzed, "pending": report.Pending, "stale": report.Stale, "recommendation": report.Recommendation, "recommended_count": len(report.RecommendedIDs), "shown": len(rows), "truncated": len(rows) < report.Total, "reasons": reasons, "jobs": rows, "notice": "仅比较本次范围；待分析、待更新和未展示的岗位不可从摘要推断。分数不是录用概率，招聘是否开放须另核验。"}, nil
}

func (t *Tools) MatchTasks(ctx context.Context, user string) (any, error) {
	runs, err := t.Store.MatchRuns(ctx, user)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, min(len(runs), 3))
	for _, run := range runs[:min(len(runs), 3)] {
		if err := t.Store.HydrateMatchRun(ctx, user, &run); err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, min(len(run.Items), 5))
		for _, item := range run.Items[:min(len(run.Items), 5)] {
			row := map[string]any{"job_id": item.JobID, "state": item.State, "stage": item.Stage, "code": item.Code}
			if item.Job != nil {
				row["title"] = t.safeText(item.Job.Title)
			}
			items = append(items, row)
		}
		out = append(out, map[string]any{"run_id": run.ID, "state": run.State, "updated_at": run.UpdatedAt, "total": len(run.Items), "shown": len(items), "items": items, "notice": "仅查询进度；继续或重试需在岗位库重新核对授权。"})
	}
	return out, nil
}
