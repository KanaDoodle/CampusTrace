package agent

import (
	"encoding/json"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
)

// Final business-fact narration is rendered from tool observations. Model prose
// is not allowed to invent an implementation or turn a proposal into a write.
func GroundedAnswer(facts []any) string {
	lines := []string{}
	for _, fact := range facts {
		var item struct {
			Tool string          `json:"tool"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal([]byte(d.JSON(fact)), &item) != nil {
			continue
		}
		switch item.Tool {
		case "get_job":
			var v struct {
				Job         d.Job          `json:"job"`
				Assessments []d.Assessment `json:"assessments"`
			}
			if json.Unmarshal(item.Data, &v) == nil {
				lines = append(lines, fmt.Sprintf("%s · %s（Job %s）", v.Job.Company, v.Job.Title, v.Job.ID))
				if len(v.Assessments) > 0 {
					a := v.Assessments[len(v.Assessments)-1]
					lines = append(lines, fmt.Sprintf("已记录状态 %s：%s [Assessment %s；%s]", a.Status, a.Reason, a.ID, a.AssessedAt.Format("2006-01-02 15:04Z")))
				} else {
					lines = append(lines, "尚无状态评估，无法确认岗位开放。")
				}
			}
		case "get_job_evidence":
			var v struct {
				Evidence []d.Evidence `json:"evidence"`
			}
			json.Unmarshal(item.Data, &v)
			lines = append(lines, fmt.Sprintf("检索到 %d 条历史证据；来源 Observation、摘录和当前评估引用见结构化记录。", len(v.Evidence)))
		case "get_job_eligibility":
			var v struct {
				Eligibility d.Eligibility `json:"eligibility"`
				Fit         string        `json:"go_fit"`
			}
			json.Unmarshal(item.Data, &v)
			lines = append(lines, fmt.Sprintf("资格：%s；GoFit：%s [Eligibility %s]", v.Eligibility.Status, v.Fit, v.Eligibility.ID))
			for _, r := range v.Eligibility.Results {
				if r.Result == "FAIL" || r.Result == "UNKNOWN" || r.Result == "CONDITIONAL" {
					lines = append(lines, fmt.Sprintf("%s: %s — %s [%s]", r.Rule, r.Result, r.Explanation, strings.Join(r.EvidenceIDs, ", ")))
				}
			}
		case "get_project_facts":
			var v struct {
				Verified []d.ProjectFact `json:"verified_facts"`
			}
			json.Unmarshal(item.Data, &v)
			if len(v.Verified) == 0 {
				lines = append(lines, "没有已核验项目事实，不能确认实现或经历。")
			}
			for _, f := range v.Verified {
				lines = append(lines, fmt.Sprintf("[%s] %s [ProjectFact %s]", f.Kind, f.Claim, f.ID))
			}
		case "list_applications":
			var rows []d.Application
			json.Unmarshal(item.Data, &rows)
			lines = append(lines, fmt.Sprintf("当前账号有 %d 条申请记录。", len(rows)))
			for _, a := range rows {
				lines = append(lines, fmt.Sprintf("Job %s: %s [Application %s]", a.JobID, a.State, a.ID))
			}
		case "search_jobs":
			var rows []d.Job
			json.Unmarshal(item.Data, &rows)
			lines = append(lines, fmt.Sprintf("找到 %d 个岗位；列表状态是最近持久化评估，详情中可查看时间和证据。", len(rows)))
		case "create_application", "transition_application", "record_interview_review":
			var pending Pending
			json.Unmarshal(item.Data, &pending)
			lines = append(lines, fmt.Sprintf("仅生成待确认操作 %s（%s），尚未写入业务状态。请检查预览后显式确认。", pending.ID, item.Tool))
		case "get_preparation_context":
			lines = append(lines, "已将目标岗位要求、资格/技术匹配、已核验项目事实、历史薄弱点和检索知识合并。准备主题与优先级见 recommended_topics；不虚构个人经历或面试答案。")
		case "search_knowledge":
			lines = append(lines, "已检索学习材料，引用位于 knowledge chunk ID；检索文本是参考资料，不用于判断岗位状态或资格。")
		default:
			lines = append(lines, "已查询 "+item.Tool+"；原始记录见 grounded_observations。")
		}
	}
	if len(lines) == 0 {
		return "没有取得足够的可信业务数据，无法确认事实。"
	}
	return strings.Join(lines, "\n")
}
