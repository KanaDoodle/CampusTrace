package agent

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

// NextAction is a read-only destination, never a command, remote URL or write.
// Only successful application tools can supply targets. A click re-reads the
// destination with the current account and its current data.
type NextAction struct {
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Reason     string `json:"reason"`
	Source     string `json:"source_tool"`
	JobID      string `json:"job_id,omitempty"`
	Company    string `json:"company,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	Topic      string `json:"topic,omitempty"`
	RunID      string `json:"run_id,omitempty"`
}

var actionID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func actionText(v string, n int) string {
	v = resume.Redact(strings.TrimSpace(v))
	runes := []rune(v)
	if len(runes) > n {
		v = string(runes[:n]) + "…"
	}
	return v
}

func NextActions(facts []any) []NextAction {
	type candidate struct {
		action   NextAction
		priority int
	}
	items := []candidate{}
	seen := map[string]int{}
	add := func(a NextAction, priority int) {
		key := a.Kind + ":" + a.JobID + ":" + a.Company + ":" + a.DocumentID + ":" + a.Topic + ":" + a.RunID
		if i, ok := seen[key]; ok {
			if priority < items[i].priority {
				items[i] = candidate{a, priority}
			}
			return
		}
		seen[key] = len(items)
		items = append(items, candidate{a, priority})
	}
	for _, fact := range facts {
		var item struct {
			Tool string          `json:"tool"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal([]byte(d.JSON(fact)), &item) != nil {
			continue
		}
		job := func(id, title, kind, reason string, priority int) {
			if !actionID.MatchString(id) {
				return
			}
			label := "查看岗位："
			if kind == "preparation" {
				label = "准备面试："
			}
			add(NextAction{Kind: kind, Label: label + actionText(title, 60), Reason: reason, Source: item.Tool, JobID: id}, priority)
		}
		switch item.Tool {
		case "compare_company_jobs":
			var v struct {
				Company string `json:"company"`
				Pending int    `json:"pending"`
				Stale   int    `json:"stale"`
			}
			if json.Unmarshal(item.Data, &v) != nil || strings.TrimSpace(v.Company) == "" || len(v.Company) > 200 {
				continue
			}
			reason := "打开公司候选范围，核对已有比较与投递进展。"
			if v.Pending+v.Stale > 0 {
				reason = "还有未分析或待更新岗位，先核对候选范围，再决定是否补充分析。"
			}
			add(NextAction{Kind: "company", Label: "查看公司取舍：" + actionText(v.Company, 60), Reason: reason, Source: item.Tool, Company: v.Company}, 0)
		case "get_match_result":
			var v struct {
				JobID string `json:"job_id"`
				Title string `json:"title"`
				State string `json:"state"`
			}
			if json.Unmarshal(item.Data, &v) != nil {
				continue
			}
			if v.State == "ANALYZED" {
				job(v.JobID, v.Title, "preparation", "查看当前岗位的准备清单与资料依据。", 1)
			} else {
				job(v.JobID, v.Title, "job", "目前没有有效深度分析；先打开岗位核对原文与分析状态。", 0)
			}
		case "get_preparation_context", "get_job":
			var v struct {
				Job       d.Job     `json:"job"`
				Knowledge []rag.Hit `json:"knowledge"`
			}
			if json.Unmarshal(item.Data, &v) != nil {
				continue
			}
			job(v.Job.ID, v.Job.Title, "preparation", "打开当前岗位的完整准备清单；历史薄弱点另行标注。", 0)
			for _, h := range v.Knowledge[:min(len(v.Knowledge), 2)] {
				if actionID.MatchString(h.DocumentID) {
					add(NextAction{Kind: "knowledge_source", Label: "阅读材料：" + actionText(h.Title, 60), Reason: "打开完整材料，核对片段的上下文。", Source: item.Tool, DocumentID: h.DocumentID}, 2)
				}
			}
		case "get_closing_jobs":
			var rows []d.RadarJob
			if json.Unmarshal(item.Data, &rows) != nil {
				continue
			}
			for _, v := range rows[:min(len(rows), 2)] {
				job(v.Job.ID, v.Job.Title, "job", "核对最新截止时间与官网投递入口。", 0)
			}
		case "search_jobs":
			var rows []d.Job
			if json.Unmarshal(item.Data, &rows) != nil {
				continue
			}
			for _, v := range rows[:min(len(rows), 2)] {
				job(v.ID, v.Title, "job", "查看当前岗位说明和已有分析。", 3)
			}
		case "list_applications":
			var rows []d.Application
			if json.Unmarshal(item.Data, &rows) != nil {
				continue
			}
			if len(rows) > 0 {
				add(NextAction{Kind: "applications", Label: "查看投递进展", Reason: "核对实际投递与面试安排。", Source: item.Tool}, 3)
			}
		case "get_match_tasks":
			var rows []struct {
				RunID string `json:"run_id"`
			}
			if json.Unmarshal(item.Data, &rows) != nil || len(rows) == 0 {
				continue
			}
			if actionID.MatchString(rows[0].RunID) {
				add(NextAction{Kind: "matching_tasks", Label: "查看深度分析任务", Reason: "先检查失败原因，再决定是否重试；此入口不会自动调用模型。", Source: item.Tool, RunID: rows[0].RunID}, 1)
			}
		case "get_weak_topics":
			var rows []d.WeakTopic
			if json.Unmarshal(item.Data, &rows) != nil {
				continue
			}
			for _, v := range PrioritizeTopics(rows, 2) {
				add(NextAction{Kind: "review_topic", Label: "复习：" + actionText(v.Topic, 60), Reason: "来自真实面试复盘；按加强程度与出现次数安排复习。", Source: item.Tool, Topic: v.Topic}, 2)
			}
			if len(PrioritizeTopics(rows, 1)) > 0 {
				add(NextAction{Kind: "interviews", Label: "核对原始面试复盘", Reason: "查看实际被问到的问题与复盘，确认这些薄弱点的来源。", Source: item.Tool}, 3)
			}
		case "search_knowledge":
			var v rag.SearchResult
			if json.Unmarshal(item.Data, &v) != nil {
				continue
			}
			if len(v.Hits) == 0 {
				add(NextAction{Kind: "knowledge", Label: "补充或搜索学习材料", Reason: "本次没有找到相关笔记，可以换个表达或补充材料。", Source: item.Tool}, 3)
			}
			for _, h := range v.Hits[:min(len(v.Hits), 2)] {
				if actionID.MatchString(h.DocumentID) {
					add(NextAction{Kind: "knowledge_source", Label: "阅读材料：" + actionText(h.Title, 60), Reason: "打开完整材料，核对片段的上下文。", Source: item.Tool, DocumentID: h.DocumentID}, 2)
				}
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].priority < items[j].priority })
	out := []NextAction{}
	for _, v := range items[:min(len(items), 6)] {
		out = append(out, v.action)
	}
	return out
}

// Historical review priorities are not inferred job requirements. Ignore rows
// without actual review evidence and use a stable tie break across reloads.
func PrioritizeTopics(rows []d.WeakTopic, limit int) []d.WeakTopic {
	out := []d.WeakTopic{}
	for _, v := range rows {
		if strings.TrimSpace(v.Topic) != "" && len(v.Topic) <= 120 && v.Weight >= 1 && v.Weight <= 5 && v.Count > 0 && len(v.Evidence) > 0 {
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := int64(out[i].Weight)*int64(out[i].Count), int64(out[j].Weight)*int64(out[j].Count)
		if a != b {
			return a > b
		}
		return out[i].Topic < out[j].Topic
	})
	return out[:max(0, min(limit, len(out)))]
}
