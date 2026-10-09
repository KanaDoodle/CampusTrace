package agent

import (
	"encoding/json"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"strings"
	"time"
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
		case "get_candidate_document":
			lines = append(lines, "已读取你核对过的完整求职资料；项目段落和教育经历按原有结构保留。")
		case "get_agent_todos":
			var feed struct {
				Enabled bool `json:"enabled"`
				Items   []struct {
					Title       string `json:"title"`
					Explanation string `json:"explanation"`
				} `json:"items"`
				More bool `json:"more"`
			}
			if json.Unmarshal(item.Data, &feed) == nil {
				if !feed.Enabled {
					lines = append(lines, "招聘变化待办尚未开启，可在此页开启。")
				}
				if len(feed.Items) == 0 && feed.Enabled {
					lines = append(lines, "目前没有待处理的招聘变化。")
				}
				for _, v := range feed.Items[:min(len(feed.Items), 5)] {
					lines = append(lines, v.Title+"："+v.Explanation)
				}
				if len(feed.Items) > 5 || feed.More {
					lines = append(lines, "还有其他待办，请在招聘变化中查看更多提醒。")
				}
			}
		case "list_mcp_resources":
			lines = append(lines, "已列出你授权的学习资料范围；选择资料连接和资料编号后可以读取。")
		case "read_mcp_resource":
			var doc struct {
				Text   string `json:"text"`
				Source string `json:"source"`
			}
			if json.Unmarshal(item.Data, &doc) == nil {
				lines = append(lines, "学习资料（"+doc.Source+"）：\n"+doc.Text, "这段内容用于学习参考，不作为个人经历或招聘状态的证明。")
			}
		case "search_memories":
			var rows []struct {
				Content string `json:"content"`
				Kind    string `json:"kind"`
			}
			if json.Unmarshal(item.Data, &rows) == nil {
				if len(rows) == 0 {
					lines = append(lines, "没有找到仍有效的记忆。")
				} else {
					for _, v := range rows {
						lines = append(lines, "已保存的偏好或决定："+v.Content)
					}
				}
			}
		case "get_agent_tasks":
			lines = append(lines, "已读取最近的讨论记录。只有你选择继续时才会载入旧摘要；业务结论仍需重新查询。")
		case "get_practice_runs":
			lines = append(lines, "已读取实际执行的练习记录；练习通过不代表有生产项目经验。")
		case "get_match_result":
			var v struct {
				Mode        string       `json:"mode"`
				Fit         string       `json:"fit"`
				Summary     string       `json:"summary"`
				CoreWork    string       `json:"core_work"`
				JobID       string       `json:"job_id"`
				Company     string       `json:"company"`
				Title       string       `json:"title"`
				JobStatus   string       `json:"job_status"`
				State       string       `json:"state"`
				Eligibility string       `json:"eligibility"`
				Notice      string       `json:"notice"`
				Score       *float64     `json:"score"`
				Coverage    float64      `json:"coverage"`
				Strengths   []matchPoint `json:"strengths"`
				Gaps        []matchPoint `json:"gaps"`
			}
			if json.Unmarshal(item.Data, &v) != nil {
				continue
			}
			if v.State != "ANALYZED" {
				lines = append(lines, fmt.Sprintf("%s · %s（Job %s）：当前没有可用的深度匹配，状态 %s。%s", v.Company, v.Title, v.JobID, matchStateLabel(v.State), v.Notice))
				break
			}
			if v.Mode == matching.HolisticVersion {
				lines = append(lines, fmt.Sprintf("%s · %s（Job %s）：整体适配 %s。%s", v.Company, v.Title, v.JobID, wholeFitLabel(v.Fit), v.Summary), "核心工作："+v.CoreWork)
				var whole struct {
					Strengths []matching.HolisticFinding `json:"strengths"`
					Gaps      []matching.HolisticFinding `json:"gaps"`
					Blockers  []matching.HolisticFinding `json:"blockers"`
				}
				if json.Unmarshal(item.Data, &whole) == nil {
					for i, list := range [][]matching.HolisticFinding{whole.Strengths, whole.Gaps, whole.Blockers} {
						if len(list) > 0 {
							lines = append(lines, []string{"优势：", "待确认：", "明确障碍："}[i]+list[0].Point+"。"+list[0].Explanation)
						}
					}
				}
				lines = append(lines, v.Notice)
				break
			}
			score := "暂无可靠核心评分"
			if v.Score != nil {
				score = fmt.Sprintf("核心匹配度 %.1f / 100", *v.Score)
			}
			lines = append(lines, fmt.Sprintf("%s · %s（Job %s）：%s，依据覆盖 %.1f%%；投递资格 %s，招聘状态 %s。", v.Company, v.Title, v.JobID, score, v.Coverage, eligibilityLabel(v.Eligibility), jobStatusLabel(v.JobStatus)))
			if len(v.Strengths) > 0 {
				lines = append(lines, "已有依据："+pointAnswer(v.Strengths[0]))
			}
			if len(v.Gaps) > 0 {
				lines = append(lines, "优先核对："+pointAnswer(v.Gaps[0]))
			}
			if v.Notice != "" {
				lines = append(lines, v.Notice)
			}
		case "compare_company_jobs":
			var v struct {
				Mode    string `json:"mode"`
				Summary string `json:"summary"`
				Choices []struct {
					matching.CompanyChoice
					Title string `json:"title"`
				} `json:"choices"`
				Company          string   `json:"company"`
				Scope            string   `json:"scope"`
				Recommendation   string   `json:"recommendation"`
				Notice           string   `json:"notice"`
				Total            int      `json:"total"`
				Analyzed         int      `json:"analyzed"`
				Pending          int      `json:"pending"`
				Stale            int      `json:"stale"`
				Shown            int      `json:"shown"`
				RecommendedCount int      `json:"recommended_count"`
				Reasons          []string `json:"reasons"`
				Jobs             []struct {
					JobID       string       `json:"job_id"`
					Title       string       `json:"title"`
					State       string       `json:"state"`
					JobStatus   string       `json:"job_status"`
					Eligibility string       `json:"eligibility"`
					Recommended bool         `json:"recommended"`
					Score       *float64     `json:"score"`
					Coverage    float64      `json:"coverage"`
					Strengths   []matchPoint `json:"strengths"`
					Gaps        []matchPoint `json:"gaps"`
				} `json:"jobs"`
			}
			if json.Unmarshal(item.Data, &v) != nil {
				continue
			}
			if v.Mode == matching.HolisticVersion {
				lines = append(lines, fmt.Sprintf("%s：本次整体比较 %d 个岗位。%s", v.Company, v.Total, v.Summary))
				for _, choice := range v.Choices[:min(len(v.Choices), 3)] {
					lines = append(lines, fmt.Sprintf("第 %d 组：%s（Job %s）。%s。优势：%s。取舍：%s。", choice.Rank, choice.Title, choice.ID, choice.Reason, choice.Advantage, choice.Tradeoff))
				}
				lines = append(lines, v.Notice)
				break
			}
			scope := "该公司全部本地岗位"
			if v.Scope == "SELECTED" {
				scope = "选中的岗位"
			}
			lines = append(lines, fmt.Sprintf("%s：本次%s共 %d 个岗位，已分析 %d、待分析 %d、待更新 %d。", v.Company, scope, v.Total, v.Analyzed, v.Pending, v.Stale))
			shown := 0
			for _, job := range v.Jobs {
				if !job.Recommended {
					continue
				}
				value := "暂无可靠评分"
				if job.Score != nil {
					value = fmt.Sprintf("核心匹配度 %.1f，覆盖 %.1f%%", *job.Score, job.Coverage)
				}
				lines = append(lines, fmt.Sprintf("优先候选：%s（Job %s）；%s；资格 %s，招聘状态 %s。", job.Title, job.JobID, value, eligibilityLabel(job.Eligibility), jobStatusLabel(job.JobStatus)))
				if len(job.Strengths) > 0 {
					lines = append(lines, "主要依据："+pointAnswer(job.Strengths[0]))
				}
				if len(job.Gaps) > 0 {
					lines = append(lines, "待核对："+pointAnswer(job.Gaps[0]))
				}
				shown++
				if shown == 3 {
					break
				}
			}
			if v.RecommendedCount == 0 && len(v.Reasons) > 0 {
				lines = append(lines, v.Reasons[0])
			}
			if v.RecommendedCount > shown {
				lines = append(lines, fmt.Sprintf("另有 %d 个并列优先候选，请展开本次依据查看。", v.RecommendedCount-shown))
			}
			if v.Pending+v.Stale > 0 {
				lines = append(lines, "仍有岗位未分析或待更新，不能视为全公司最终排序。")
			}
			if v.Shown < v.Total {
				lines = append(lines, fmt.Sprintf("摘要展示 %d / %d 个岗位，完整结果请在岗位库核对。", v.Shown, v.Total))
			}
		case "get_match_tasks":
			var runs []struct {
				RunID string `json:"run_id"`
				State string `json:"state"`
				Total int    `json:"total"`
				Shown int    `json:"shown"`
				Items []struct {
					Title string `json:"title"`
					JobID string `json:"job_id"`
					State string `json:"state"`
					Stage string `json:"stage"`
					Code  string `json:"code"`
				} `json:"items"`
			}
			if json.Unmarshal(item.Data, &runs) != nil {
				continue
			}
			if len(runs) == 0 {
				lines = append(lines, "最近没有深度分析任务。")
			}
			for _, run := range runs {
				lines = append(lines, fmt.Sprintf("分析任务 %s：%s，共 %d 个岗位；本次摘要展示 %d 个。", run.RunID, matchStateLabel(run.State), run.Total, run.Shown))
				for _, item := range run.Items {
					if item.State != "FAILED" && item.State != "INTERRUPTED" {
						continue
					}
					name := item.Title
					if name == "" {
						name = "Job " + item.JobID
					}
					lines = append(lines, fmt.Sprintf("%s：%s（阶段 %s，分类 %s）；可在岗位库查看原因并核对后重试。", name, matchStateLabel(item.State), item.Stage, item.Code))
				}
			}
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
		case "get_closing_jobs":
			var rows []d.RadarJob
			if json.Unmarshal(item.Data, &rows) == nil {
				lines = append(lines, fmt.Sprintf("本次期限范围内有 %d 个可跟进的截止岗位。", len(rows)))
				for _, v := range rows[:min(len(rows), 5)] {
					lines = append(lines, v.Job.Company+" · "+v.Job.Title+"：请在岗位详情核对截止时间及官方入口。")
				}
			} else {
				lines = append(lines, "已查询截止提醒，原文和日期依据见查询记录。")
			}
		case "get_recent_changes":
			var rows []d.ChangeItem
			if json.Unmarshal(item.Data, &rows) == nil {
				lines = append(lines, fmt.Sprintf("本次查询范围内有 %d 条岗位变化。", len(rows)))
			} else {
				lines = append(lines, "已查询近期岗位变化，详情见查询记录。")
			}
		case "get_daily_digest":
			var v d.DailyDigest
			json.Unmarshal(item.Data, &v)
			lines = append(lines, fmt.Sprintf("截至 %s：今日新增 %d，优先投递 %d，7 天内截止 %d，状态变化 %d，本周面试 %d。具体岗位及依据见工具记录。", v.AsOf.Format(time.RFC3339), v.Counts["new_jobs"], v.Counts["recommended_jobs"], v.Counts["closing_soon"], v.Counts["status_changes"], v.Counts["upcoming_interviews"]))
		case "remember_memory":
			lines = append(lines, "已生成一条记忆预览，尚未保存。请核对记忆内容、类型和有效期后确认。")
		case "create_application", "transition_application", "record_interview_review", "watch_source", "unwatch_source":
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

func pointAnswer(p matchPoint) string {
	text := p.Requirement
	if p.RequirementID != "" {
		text += " [要求 " + p.RequirementID + "]"
	}
	if p.FactID != "" {
		text += "；个人依据 [资料 " + p.FactID + "]"
	}
	if p.Result != "" {
		text += "；" + matchResultLabel(p.Result)
	}
	return text
}

func matchResultLabel(v string) string {
	switch v {
	case "DIRECT":
		return "直接匹配"
	case "PARTIAL":
		return "部分匹配"
	case "TRANSFERABLE":
		return "可迁移经验"
	case "NO_EVIDENCE":
		return "暂无依据"
	case "MISMATCH":
		return "明确不符合"
	default:
		return v
	}
}

func matchStateLabel(v string) string {
	switch v {
	case "ANALYZED":
		return "已分析"
	case "BASIC":
		return "仅本地初筛"
	case "STALE":
		return "待更新"
	case "RUNNING":
		return "处理中"
	case "WAITING_AUTH":
		return "等待重新授权"
	case "PAUSED":
		return "已暂停"
	case "PAUSING":
		return "正在暂停"
	case "COMPLETED":
		return "已完成"
	case "FAILED":
		return "失败"
	case "INTERRUPTED":
		return "已中断"
	case "CANCELLED":
		return "已取消"
	default:
		return v
	}
}
func eligibilityLabel(v string) string {
	switch v {
	case "ELIGIBLE":
		return "符合已识别条件"
	case "INELIGIBLE":
		return "明确不符合条件"
	case "CONDITIONAL":
		return "需进一步核对"
	case "UNKNOWN":
		return "暂无法判断"
	default:
		return v
	}
}
func jobStatusLabel(v string) string {
	switch v {
	case "OPEN":
		return "可投递"
	case "CLOSED":
		return "已关闭"
	case "NEEDS_VERIFICATION":
		return "待核验"
	case "UNKNOWN":
		return "暂无法确认"
	default:
		return v
	}
}

func wholeFitLabel(value string) string {
	return map[string]string{"STRONG": "高度相关", "RELATED": "值得考虑", "WEAK": "相关较弱", "UNCERTAIN": "需要确认"}[value]
}
