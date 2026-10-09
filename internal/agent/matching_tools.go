package agent

import (
	"context"
	"sort"
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
	if row.Result.Holistic != nil {
		h := row.Result.Holistic
		points := func(items []matching.HolisticFinding) []map[string]string {
			v := []map[string]string{}
			for _, f := range items[:min(len(items), 3)] {
				v = append(v, map[string]string{"point": t.safeText(f.Point), "explanation": t.safeText(f.Explanation), "job_excerpt": t.safeText(f.JobExcerpt)})
			}
			return v
		}
		out["mode"], out["fit"], out["summary"], out["core_work"] = matching.HolisticVersion, h.Fit, t.safeText(h.Summary), t.safeText(h.CoreWork)
		out["strengths"], out["gaps"], out["blockers"], out["eligibility"] = points(h.Strengths), points(h.Gaps), points(h.Blockers), plan.Eligibility.Status
		out["notice"] = "仅读取完整材料的整体分析；引用可追溯不代表推断必然正确，没有重新调用分析模型。"
		return out, nil
	}
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
	if err := t.Store.AttachCompanyReport(ctx, user, s, company, t.MatchModel, &report, t.MaskName); err != nil {
		return nil, err
	}
	if report.Holistic != nil {
		h := report.Holistic
		choices := []map[string]any{}
		ordered := append([]matching.CompanyChoice{}, h.Choices...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Rank < ordered[j].Rank })
		titles := map[string]string{}
		for _, job := range s.Jobs {
			titles[job.Job.ID] = t.safeText(job.Job.Title)
		}
		for _, v := range ordered[:min(len(ordered), 8)] {
			choices = append(choices, map[string]any{"job_id": v.ID, "title": titles[v.ID], "rank": v.Rank, "reason": t.safeText(v.Reason), "advantage": t.safeText(v.Advantage), "tradeoff": t.safeText(v.Tradeoff)})
		}
		return map[string]any{"company": t.safeText(company), "mode": matching.HolisticVersion, "scope": scope, "total": len(h.Choices), "shown": len(choices), "truncated": len(choices) < len(h.Choices), "summary": t.safeText(h.Summary), "choices": choices, "notice": "复用本次完整材料比较，不重新调用分析；推荐仅适用于报告中的岗位范围，不代表官网全部岗位。"}, nil
	}
	return map[string]any{"company": t.safeText(company), "scope": scope, "mode": matching.HolisticVersion, "total": report.Total, "analyzed": report.Analyzed, "pending": report.Pending, "stale": report.Stale, "recommended_count": 0, "shown": 0, "choices": []matching.CompanyChoice{}, "summary": "本次范围还没有可复用的整体比较。请在岗位雷达核对完整材料后生成同公司比较。", "notice": "单岗结论或旧版条目分数不作为本次公司排序；读取没有调用分析模型。"}, nil
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
