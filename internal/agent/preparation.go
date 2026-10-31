package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
)

type PreparationSignal struct {
	Source string `json:"source"`
	ID     string `json:"source_id,omitempty"`
	Text   string `json:"text"`
}

type PreparationQuery struct {
	Version string              `json:"version"`
	Scope   string              `json:"scope"`
	Query   string              `json:"query"`
	Signals []PreparationSignal `json:"signals"`
}

var preparationClauses = regexp.MustCompile(`[\n。；;!?！？]+`)
var preparationTechnical = regexp.MustCompile(`(?i)\b(?:go|golang|java|python|rust|redis|kafka|mysql|sql|rpc|http|grpc|linux|docker|kubernetes|opensearch|elasticsearch|llm|agent|rag|embedding|rerank|tcp|udp|pel|xautoclaim)\b|数据库|缓存|并发|事务|分布式|检索|模型|推理|部署|调度|网络|队列|性能|存储|接口|架构|监控|安全|故障|测试`)

// A bounded, inspectable query plan, not another model/extraction stage. Each
// signal is literal source text. Scores only select JD clauses for retrieval;
// they never decide eligibility, skill proficiency or hiring suitability.
func preparationQuery(title, jd string, reviews []d.Review, weak []d.WeakTopic, scope string) PreparationQuery {
	out := PreparationQuery{Version: "preparation-query-v1", Scope: scope, Signals: []PreparationSignal{}}
	seen := map[string]bool{}
	add := func(source, id, text string, limit int) {
		text = strings.TrimSpace(prefixUTF8(strings.TrimSpace(text), limit))
		if text == "" || seen[text] {
			return
		}
		remaining := 1000 - len(out.Query)
		if out.Query != "" {
			remaining--
		}
		if remaining < len(text) {
			return
		}
		seen[text] = true
		out.Signals = append(out.Signals, PreparationSignal{Source: source, ID: id, Text: text})
		if out.Query != "" {
			out.Query += " "
		}
		out.Query += text
	}
	add("job_title", "", title, 100)
	// Recent questions/missed points are linked to this scope, not arbitrary
	// recent practice runs or another application's interview history.
	for _, review := range reviews[:min(len(reviews), 2)] {
		for _, text := range review.Missed[:min(len(review.Missed), 1)] {
			add("interview_missed", review.ID, text, 130)
		}
		for _, text := range review.Questions[:min(len(review.Questions), 1)] {
			add("interview_question", review.ID, text, 130)
		}
	}
	type clause struct {
		text  string
		score int
	}
	clauses := []clause{}
	for _, text := range preparationClauses.Split(jd, -1) {
		if n := len(preparationTechnical.FindAllString(text, -1)); n > 0 {
			clauses = append(clauses, clause{text, n})
		}
	}
	sort.SliceStable(clauses, func(i, j int) bool { return clauses[i].score > clauses[j].score })
	for _, c := range clauses[:min(len(clauses), 3)] {
		// Keep a late technical sentence visible even if a portal prepends a
		// long company introduction. Do not rewrite it into a personal claim.
		start := preparationTechnical.FindStringIndex(c.text)[0]
		add("job_description", "", c.text[start:], 160)
	}
	contextText := strings.ToLower(jd)
	for _, review := range reviews {
		contextText += " " + strings.ToLower(strings.Join(review.Questions, " ")+" "+strings.Join(review.Missed, " "))
	}
	for _, topic := range PrioritizeTopics(weak, len(weak)) {
		matches := 0
		for _, token := range rag.Tokens(topic.Topic) {
			if len([]rune(token)) > 1 && strings.Contains(contextText, strings.ToLower(token)) {
				matches++
			}
		}
		if matches > 0 {
			add("related_review_topic", "", topic.Topic, 100)
		}
	}
	return out
}

func (t *Tools) prepareKnowledge(ctx context.Context, user, title, jd string, reviews []d.Review, weak []d.WeakTopic, scope string) (PreparationQuery, rag.SearchResult, error) {
	clean := make([]d.Review, len(reviews))
	for i, v := range reviews {
		clean[i] = v
		clean[i].Questions, clean[i].Missed = []string{}, []string{}
		for _, q := range v.Questions {
			clean[i].Questions = append(clean[i].Questions, t.memoryText(q))
		}
		for _, q := range v.Missed {
			clean[i].Missed = append(clean[i].Missed, t.memoryText(q))
		}
	}
	cleanWeak := append([]d.WeakTopic{}, weak...)
	for i := range cleanWeak {
		cleanWeak[i].Topic = t.memoryText(cleanWeak[i].Topic)
	}
	plan := preparationQuery(t.memoryText(title), t.memoryText(jd), clean, cleanWeak, scope)
	if t.RAG == nil || plan.Query == "" {
		return plan, rag.SearchResult{Hits: []rag.Hit{}, Retrieval: rag.RetrievalInfo{Mode: "keyword", Warnings: []string{"PREPARATION_KNOWLEDGE_UNAVAILABLE"}}}, nil
	}
	result, err := t.RAG.SearchDetailed(ctx, user, plan.Query, 5)
	return plan, result, err
}

func (t *Tools) PrepareApplication(ctx context.Context, user, id string) (any, error) {
	dossier, err := t.Store.ApplicationDossier(ctx, user, id)
	if err != nil {
		return nil, err
	}
	out := applicationDossierObservation(dossier, t)
	out["preparation_scope"] = "frozen_application"
	if dossier.Context == nil {
		out["knowledge"] = []rag.Hit{}
		out["notice"] = "历史申请缺少材料快照，不能用当前资料补作当时依据；未发起资料检索。"
		return out, nil
	}
	reviews, err := t.Store.PreparationReviews(ctx, user, id, "")
	if err != nil {
		return nil, err
	}
	plan, result, err := t.prepareKnowledge(ctx, user, dossier.Context.Job.Title, dossier.Context.JD, reviews, nil, "frozen_application")
	if err != nil {
		return nil, err
	}
	out["retrieval_context"], out["knowledge"], out["retrieval"] = plan, result.Hits, result.Retrieval
	out["review_count"] = len(reviews)
	out["policy"] = "Only this application's frozen materials and confirmed records describe the application. Retrieved notes are learning references, never evidence of personal skills or guaranteed interview questions."
	return out, nil
}

func applicationPreparationAnswer(raw json.RawMessage) []string {
	lines := dossierAnswer(raw)
	var v struct {
		Legacy      bool             `json:"legacy"`
		ReviewCount int              `json:"review_count"`
		Plan        PreparationQuery `json:"retrieval_context"`
		Knowledge   []rag.Hit        `json:"knowledge"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Legacy || v.Plan.Version == "" {
		return lines
	}
	lines = append(lines, fmt.Sprintf("本次检索结合留档 JD 与本申请的 %d 份复盘；最近问题和遗漏点优先作为复习线索。", v.ReviewCount))
	for _, signal := range v.Plan.Signals {
		if signal.Source == "interview_question" || signal.Source == "interview_missed" {
			lines = append(lines, "本次复盘线索："+signal.Text)
		}
	}
	if len(v.Knowledge) == 0 {
		return append(lines, "尚未检索到相关面试资料，可以先记录复盘或补充笔记；这不代表缺少相关能力。")
	}
	lines = append(lines, fmt.Sprintf("检索到 %d 段学习参考，可从阅读入口核对完整材料；笔记不是个人能力证明，也不保证面试会考。", len(v.Knowledge)))
	for _, hit := range v.Knowledge[:min(len(v.Knowledge), 3)] {
		lines = append(lines, "学习参考 · "+hit.Title+"："+prefixUTF8(hit.Text, 240))
	}
	return lines
}
