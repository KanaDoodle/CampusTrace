package matching

import (
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"sort"
	"strings"
	"time"
)

const DecisionVersion = "decision-v1"
const MaxDecisionJobs = 200

var ErrDecisionCapacity = errors.New("decision view capacity exceeded")

// Use the same alternative units and representative match in scoring,
// comparison and preparation: one successful optional direction is enough.
func requirementUnits(reqs []Requirement, category string) [][]Requirement {
	units := [][]Requirement{}
	groups := map[string]int{}
	for _, r := range reqs {
		if r.Category == "QUALIFICATION" {
			continue
		}
		section := r.Category
		if r.Aspect == "SOFT" || softOnly(r) {
			section = "SOFT"
		}
		if section != category {
			continue
		}
		if r.GroupID != "" {
			if i, ok := groups[r.GroupID]; ok {
				units[i] = append(units[i], r)
				continue
			}
			groups[r.GroupID] = len(units)
		}
		units = append(units, []Requirement{r})
	}
	return units
}
func unitComparison(unit []Requirement, byID map[string]Match) (Requirement, Match, string, float64) {
	selected := unit[0]
	match := byID[selected.ID]
	best, value, allMismatch := "NO_EVIDENCE", -1.0, true
	for _, r := range unit {
		m := byID[r.ID]
		if r.Confidence < .8 || m.Result != "MISMATCH" {
			allMismatch = false
		}
		v, positive := map[string]float64{"DIRECT": 1, "PARTIAL": .5, "TRANSFERABLE": .25}[m.Result]
		if r.Confidence >= .8 && positive && v > value {
			selected, match, best, value = r, m, m.Result, v
		}
	}
	if best == "NO_EVIDENCE" && allMismatch {
		best, value = "MISMATCH", 0
	}
	return selected, match, best, value
}

type DecisionInput struct {
	Job                   d.Job
	State, ExcludedReason string
	Local                 *LocalScreen
	Result                *Result
}
type PreparationEvidence struct {
	RequirementID string `json:"requirement_id"`
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	ProjectName   string `json:"project_name,omitempty"`
	Excerpt       string `json:"excerpt"`
}
type PreparationTask struct {
	ID                    string                `json:"id"`
	Kind                  string                `json:"kind"`
	Category              string                `json:"category"`
	Priority              int                   `json:"priority"`
	Title                 string                `json:"title"`
	Action                string                `json:"action"`
	Result                string                `json:"result"`
	Explanation           string                `json:"explanation"`
	Requirements          []Requirement         `json:"requirements"`
	SelectedRequirementID string                `json:"selected_requirement_id,omitempty"`
	Evidence              []PreparationEvidence `json:"evidence"`
}
type PreparationPlan struct {
	Version     string            `json:"version"`
	Job         d.Job             `json:"job"`
	State       string            `json:"state"`
	InputKey    string            `json:"input_key"`
	AnalyzedAt  time.Time         `json:"analyzed_at"`
	Score       *float64          `json:"score"`
	Coverage    float64           `json:"coverage"`
	Eligibility d.Eligibility     `json:"eligibility"`
	Tasks       []PreparationTask `json:"tasks"`
	Notice      string            `json:"notice"`
}

func BuildPreparation(in DecisionInput, p d.Profile, now time.Time) PreparationPlan {
	plan := PreparationPlan{Version: DecisionVersion, Job: in.Job, State: in.State, Tasks: []PreparationTask{}}
	if in.State != "ANALYZED" || in.Result == nil {
		plan.Notice = "先完成深度分析，再生成有岗位依据的准备清单。"
		if in.State == "STALE" {
			plan.Notice = "岗位、资料或模型已变化；请更新分析后重新生成清单。旧结果不用于当前准备建议。"
		}
		return plan
	}
	r := in.Result
	plan.InputKey = r.InputKey
	plan.AnalyzedAt = r.AnalyzedAt
	plan.Score, plan.Coverage = Score(r.Requirements, r.Matches)
	plan.Eligibility = Qualification(in.Job, p, r.Requirements, now)
	if in.ExcludedReason != "" {
		plan.Notice = in.ExcludedReason + "。清单仅供核对和复习，不代表可以投递。"
	}
	byID := map[string]Match{}
	for _, m := range r.Matches {
		byID[m.RequirementID] = m
	}
	facts := map[string]Fact{}
	for _, f := range r.CandidateFacts {
		facts[f.ID] = f
	}
	if plan.Eligibility.Status != "ELIGIBLE" {
		reqs := []Requirement{}
		for _, req := range r.Requirements {
			if req.Category == "QUALIFICATION" {
				reqs = append(reqs, req)
			}
		}
		plan.Tasks = append(plan.Tasks, PreparationTask{ID: "verify-qualification", Kind: "VERIFY", Category: "QUALIFICATION", Priority: 1, Title: "核对投递资格与招聘状态", Action: "对照官网和资格核对表，确认毕业届别、学历等未明确条件；明确不符合的条件不会通过技术复习消除。", Result: plan.Eligibility.Status, Requirements: reqs, Evidence: []PreparationEvidence{}})
	}
	for _, category := range []string{"REQUIRED", "RESPONSIBILITY", "BONUS", "SOFT"} {
		for _, unit := range requirementUnits(r.Requirements, category) {
			selected, m, status, _ := unitComparison(unit, byID)
			task := PreparationTask{ID: d.Hash(category + "\n" + selected.ID)[:24], Category: category, Title: selected.Text, Result: status, Explanation: m.Explanation, Requirements: unit, SelectedRequirementID: selected.ID, Evidence: []PreparationEvidence{}, Priority: 2}
			switch status {
			case "DIRECT":
				task.Kind = "REHEARSE"
				task.Action = "准备原理与实现说明。只用下面已核对的事实介绍实际经历，没有做过的取舍、测试或测量明确说明。"
			case "PARTIAL", "TRANSFERABLE":
				task.Kind = "DEEPEN"
				task.Action = "先说明已有经验适用的部分，再核对岗位额外要求和适用边界；相近机制不能直接说成掌握了指定组件。"
			case "MISMATCH":
				task.Kind = "LEARN"
				task.Action = "结合明确不符合的依据评估差距，安排相关学习或练习；练习完成前不要写成已掌握或已实现。"
			default:
				task.Kind = "EVIDENCE"
				if len(unit) > 1 {
					task.Explanation = "本组选项仍有待核对的方向，暂无足够依据形成匹配或整体不符合的结论。"
				}
				task.Action = "先核对自己是否实际做过：有真实经历就补充并确认项目事实；没有则安排学习。暂无依据不等于不会。"
				if selected.Confidence < .8 {
					task.Action = "这项要求的解释置信度较低，先核对岗位原文与含义，再决定补充资料或安排学习。"
				}
			}
			if category == "REQUIRED" && status != "DIRECT" {
				task.Priority = 1
			}
			if category == "BONUS" || category == "SOFT" {
				task.Priority = 3
			}
			if len(unit) > 1 {
				task.Action += " 本组是任选方向，只需核对所选方向；无需把所有方向同时补齐。"
			}
			sources := []Match{m}
			if status == "MISMATCH" && len(unit) > 1 {
				sources = []Match{}
				for _, req := range unit {
					sources = append(sources, byID[req.ID])
				}
			}
			seen := map[string]bool{}
			for _, source := range sources {
				for _, citation := range source.Evidence {
					f, ok := facts[citation.ID]
					key := source.RequirementID + "\n" + citation.ID + "\n" + citation.Excerpt
					if !ok || citation.Excerpt == "" || !strings.Contains(f.Text, citation.Excerpt) || seen[key] {
						continue
					}
					seen[key] = true
					task.Evidence = append(task.Evidence, PreparationEvidence{source.RequirementID, f.ID, f.Kind, f.ProjectName, citation.Excerpt})
				}
			}
			plan.Tasks = append(plan.Tasks, task)
		}
	}
	sort.SliceStable(plan.Tasks, func(i, j int) bool { return plan.Tasks[i].Priority < plan.Tasks[j].Priority })
	if len(plan.Tasks) == 0 {
		plan.Notice = "没有提取到可核对的要求，暂不能生成针对性的准备项。请先查看岗位原文。"
	}
	return plan
}

type CompanyJob struct {
	Job                                           d.Job             `json:"job"`
	State                                         string            `json:"state"`
	Score                                         *float64          `json:"score"`
	Coverage                                      float64           `json:"coverage"`
	Eligibility                                   string            `json:"eligibility"`
	Direction                                     string            `json:"direction"`
	City                                          string            `json:"city_preference"`
	JobType                                       string            `json:"type_preference"`
	BlockedReason                                 string            `json:"blocked_reason"`
	Comparable                                    bool              `json:"comparable"`
	Recommended                                   bool              `json:"recommended"`
	Strengths                                     []PreparationTask `json:"strengths"`
	Gaps                                          []PreparationTask `json:"gaps"`
	Sections                                      []SectionScore    `json:"sections"`
	AnalyzedAt                                    time.Time         `json:"analyzed_at"`
	roleRank, eligibilityRank, cityRank, typeRank int
	bonusSupport                                  float64
}
type CompanyComparison struct {
	Version        string       `json:"version"`
	Company        string       `json:"company"`
	Scope          string       `json:"scope"`
	GeneratedAt    time.Time    `json:"generated_at"`
	Total          int          `json:"total"`
	Analyzed       int          `json:"analyzed"`
	Pending        int          `json:"pending"`
	Stale          int          `json:"stale"`
	Recommendation string       `json:"recommendation"`
	RecommendedIDs []string     `json:"recommended_job_ids"`
	Reasons        []string     `json:"reasons"`
	Jobs           []CompanyJob `json:"jobs"`
}

func directionPreference(p d.Profile, in DecisionInput) (int, string) {
	if len(p.TargetRoles) == 0 {
		return 0, "尚未填写意向职能"
	}
	roles := detectLocalRoles(in.Job.Title)
	if in.Local != nil && in.Local.Role != "" {
		roles = detectLocalRoles(in.Local.Role)
	}
	targets := []string{}
	for _, t := range p.TargetRoles {
		targets = append(targets, detectLocalRoles(t)...)
		if len(Normalize(t)) >= 2 && strings.Contains(Normalize(in.Job.Title), Normalize(t)) {
			return 2, "与已保存意向一致"
		}
	}
	for _, role := range roles {
		if hasString(targets, role) {
			return 2, "与已保存意向一致"
		}
	}
	if hasString(targets, "后端开发") && hasString(roles, "基础架构与平台") || hasString(targets, "基础架构与平台") && hasString(roles, "后端开发") {
		return 1, "与意向相关，需核对职责"
	}
	if len(targets) > 0 && len(roles) > 0 {
		return -1, "与当前意向方向不同"
	}
	return 0, "方向尚需核对"
}
func cityPreference(p d.Profile, j d.Job) (int, string) {
	for _, city := range j.Locations {
		if d.CityAlternatives(city, p.PreferredCities) {
			return 2, "包含首选城市"
		}
	}
	for _, city := range j.Locations {
		if d.CityAlternatives(city, p.AcceptableCities) {
			return 1, "包含可接受城市"
		}
	}
	if len(p.PreferredCities)+len(p.AcceptableCities) == 0 {
		return 0, "尚未填写城市偏好"
	}
	if len(j.Locations) == 0 {
		return 0, "地点尚未明确"
	}
	return 0, "需确认是否接受地点"
}
func typePreference(p d.Profile, j d.Job) (int, string) {
	if len(p.PreferredTypes) == 0 {
		return 0, "尚未填写类型偏好"
	}
	if j.JobType == "" || j.JobType == "UNKNOWN" {
		return 0, "类型尚未明确"
	}
	canonical := func(s string) string {
		return strings.ToUpper(strings.NewReplacer("_", "", "-", "", " ", "").Replace(s))
	}
	for _, v := range p.PreferredTypes {
		if canonical(v) == canonical(j.JobType) {
			return 1, "符合类型偏好"
		}
	}
	return 0, "需确认岗位类型偏好"
}
func compareOrder(a, b CompanyJob) int {
	for _, pair := range [][2]int{{a.roleRank, b.roleRank}, {a.eligibilityRank, b.eligibilityRank}} {
		if pair[0] > pair[1] {
			return -1
		}
		if pair[0] < pair[1] {
			return 1
		}
	}
	for _, pair := range [][2]float64{{*a.Score, *b.Score}, {a.Coverage, b.Coverage}, {float64(a.cityRank), float64(b.cityRank)}, {float64(a.typeRank), float64(b.typeRank)}, {a.bonusSupport, b.bonusSupport}} {
		if pair[0] > pair[1] {
			return -1
		}
		if pair[0] < pair[1] {
			return 1
		}
	}
	return 0
}
func BuildCompanyComparison(company, scope string, inputs []DecisionInput, p d.Profile, now time.Time) CompanyComparison {
	out := CompanyComparison{Version: DecisionVersion, Company: company, Scope: scope, GeneratedAt: now, Recommendation: "NONE", RecommendedIDs: []string{}, Reasons: []string{}, Jobs: []CompanyJob{}}
	for _, in := range inputs {
		out.Total++
		row := CompanyJob{Job: in.Job, State: in.State, Strengths: []PreparationTask{}, Gaps: []PreparationTask{}, Sections: []SectionScore{}, Eligibility: "UNKNOWN", BlockedReason: in.ExcludedReason}
		row.roleRank, row.Direction = directionPreference(p, in)
		row.cityRank, row.City = cityPreference(p, in.Job)
		row.typeRank, row.JobType = typePreference(p, in.Job)
		if in.Job.CurrentStatus == "CLOSED" {
			row.BlockedReason = "岗位已关闭"
		}
		if in.State == "STALE" {
			out.Stale++
		} else if in.State != "ANALYZED" || in.Result == nil {
			out.Pending++
		} else {
			out.Analyzed++
			plan := BuildPreparation(in, p, now)
			row.Score, row.Coverage, row.Eligibility, row.AnalyzedAt = plan.Score, plan.Coverage, plan.Eligibility.Status, plan.AnalyzedAt
			row.Sections = ScoreBreakdown(in.Result.Requirements, in.Result.Matches)
			for _, task := range plan.Tasks {
				if task.Category == "QUALIFICATION" {
					continue
				}
				if task.Category == "REQUIRED" || task.Category == "RESPONSIBILITY" {
					if task.Result == "DIRECT" {
						if len(row.Strengths) < 3 {
							row.Strengths = append(row.Strengths, task)
						}
					} else if len(row.Gaps) < 3 {
						row.Gaps = append(row.Gaps, task)
					}
				}
			}
			for _, section := range row.Sections {
				if section.Category == "BONUS" && section.Total > 0 {
					row.bonusSupport = (float64(section.Direct) + .5*float64(section.Partial) + .25*float64(section.Transferable)) / float64(section.Total)
				}
			}
			row.eligibilityRank = map[string]int{"ELIGIBLE": 3, "CONDITIONAL": 2, "UNKNOWN": 1}[row.Eligibility]
			if row.BlockedReason == "" && row.Eligibility == "INELIGIBLE" {
				row.BlockedReason = "明确不符合投递条件"
			}
			row.Comparable = row.Score != nil && row.Coverage >= 60 && row.BlockedReason == ""
		}
		out.Jobs = append(out.Jobs, row)
	}
	sort.SliceStable(out.Jobs, func(i, j int) bool {
		a, b := out.Jobs[i], out.Jobs[j]
		if a.Comparable != b.Comparable {
			return a.Comparable
		}
		if a.Comparable {
			if order := compareOrder(a, b); order != 0 {
				return order < 0
			}
		}
		return a.Job.ID < b.Job.ID
	})
	candidates := []int{}
	for i, j := range out.Jobs {
		if j.Comparable && j.roleRank >= 0 {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		out.Reasons = append(out.Reasons, "当前范围没有同时具备有效评分、足够依据且未被排除的意向岗位。请补充资料、更新分析或核对岗位条件。")
		return out
	}
	top := out.Jobs[candidates[0]]
	for _, i := range candidates {
		if compareOrder(top, out.Jobs[i]) != 0 {
			break
		}
		out.Jobs[i].Recommended = true
		out.RecommendedIDs = append(out.RecommendedIDs, out.Jobs[i].Job.ID)
	}
	out.Recommendation = "READY"
	if out.Pending+out.Stale > 0 {
		out.Recommendation = "PARTIAL"
	}
	if len(out.RecommendedIDs) > 1 {
		out.Recommendation = "TIED"
	}
	out.Reasons = append(out.Reasons, "仅使用本次范围内的有效分析；先看意向方向与资格状态，再按核心匹配度、覆盖度、城市与类型偏好、加分项有据匹配比例排序。相同条件保留并列。")
	out.Reasons = append(out.Reasons, fmt.Sprintf("优先候选的核心匹配度 %.1f，依据覆盖 %.1f%%；%s，%s。", *top.Score, top.Coverage, top.Direction, top.City))
	if top.Eligibility != "ELIGIBLE" {
		out.Reasons = append(out.Reasons, "优先候选的投递资格仍需核对，不能直接视为可投递。")
	}
	if top.Job.CurrentStatus != "OPEN" {
		out.Reasons = append(out.Reasons, "优先候选的招聘状态尚未确认开放，请核验官网。")
	}
	if out.Pending+out.Stale > 0 {
		out.Reasons = append(out.Reasons, fmt.Sprintf("另有 %d 个未分析、%d 个待更新岗位，暂不参与推荐；结论不代表整家公司所有岗位中的最优选择。", out.Pending, out.Stale))
	}
	return out
}
