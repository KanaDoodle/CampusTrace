package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
)

func TestPreparationRetrievalUsesActualQuestionsAndLateJDRequirements(t *testing.T) {
	jd := strings.Repeat("公司介绍与办公环境。", 200) + "岗位职责：使用 Redis Streams 与 Go 实现可靠消费。任职要求：熟悉 MySQL 事务与并发控制。"
	reviews := []d.Review{{ID: "review-1", Questions: []string{"XAUTOCLAIM 如何恢复 pending 消息？"}, Missed: []string{"Redis PEL 消息重领的边界"}}}
	weak := []d.WeakTopic{{Topic: "支付核算", Weight: 5, Count: 100}, {Topic: "Redis Streams", Weight: 1, Count: 1}}
	plan := preparationQuery("后端开发", jd, reviews, weak, "current_job")
	if !strings.Contains(plan.Query, "Redis") || !strings.Contains(plan.Query, "XAUTOCLAIM") || !strings.Contains(plan.Query, "MySQL") || strings.Contains(plan.Query, "支付核算") || strings.Contains(plan.Query, "公司介绍") {
		t.Fatal(plan)
	}
	found := false
	for _, signal := range plan.Signals {
		if signal.Source == "interview_question" && signal.ID == "review-1" {
			found = true
		}
	}
	if !found || len(plan.Query) > 1000 || !utf8.ValidString(plan.Query) {
		t.Fatal("query lost scope, provenance or its bound", plan)
	}
}

func TestApplicationPreparationAnswerSeparatesActualReviewFromLearningReference(t *testing.T) {
	data := map[string]any{"application_id": "a", "job": d.Job{Title: "后端"}, "review_count": 1, "retrieval_context": PreparationQuery{Version: "v1", Signals: []PreparationSignal{{Source: "interview_question", Text: "PEL 如何重领？"}}}, "knowledge": []rag.Hit{{Chunk: rag.Chunk{Title: "Redis 笔记", Text: "XAUTOCLAIM 重领消息。"}}}}
	answer := strings.Join(applicationPreparationAnswer(json.RawMessage(d.JSON(data))), "\n")
	for _, want := range []string{"本申请的 1 份复盘", "PEL 如何重领", "Redis 笔记", "XAUTOCLAIM", "不是个人能力证明"} {
		if !strings.Contains(answer, want) {
			t.Fatal(answer)
		}
	}
	data["legacy"] = true
	answer = strings.Join(applicationPreparationAnswer(json.RawMessage(d.JSON(data))), "\n")
	if strings.Contains(answer, "XAUTOCLAIM") || !strings.Contains(answer, "没有历史材料快照") {
		t.Fatal(answer)
	}
}

func TestPreparationQueryIsBoundedAndDoesNotRewriteSourceIntoClaims(t *testing.T) {
	text := "熟悉分布式事务与数据库并发控制，保留否定和待核对边界。"
	plan := preparationQuery(strings.Repeat("后端", 50), strings.Repeat(text, 500), []d.Review{{ID: "r", Questions: []string{strings.Repeat("如何处理失败？", 1000)}, Missed: []string{strings.Repeat("不清楚租约过期。", 1000)}}}, nil, "frozen_application")
	if len(plan.Query) > 1000 || !utf8.ValidString(plan.Query) || plan.Scope != "frozen_application" {
		t.Fatal(plan)
	}
	for _, signal := range plan.Signals {
		if signal.Source == "job_description" && !strings.Contains(strings.Repeat(text, 500), signal.Text) {
			t.Fatal("retrieval invented a requirement", signal)
		}
	}
}

func TestPreparationRedactsAllQuerySourcesWithoutChangingSavedInputs(t *testing.T) {
	reviews := []d.Review{{ID: "r", Questions: []string{"张同学 person@example.com 如何排查 Go 并发？"}, Missed: []string{"手机号13800138000，Redis PEL"}}}
	weak := []d.WeakTopic{{Topic: "张同学 Go", Weight: 1, Count: 1}}
	tools := &Tools{MaskName: "张同学"}
	plan, _, err := tools.prepareKnowledge(context.Background(), "user", "张同学的Go岗位", "使用Go与Redis", reviews, weak, "current_job")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"张同学", "person@example.com", "13800138000"} {
		if strings.Contains(plan.Query, secret) {
			t.Fatal("query leaked a saved identifier", plan.Query)
		}
	}
	if !strings.Contains(reviews[0].Questions[0], "person@example.com") || weak[0].Topic != "张同学 Go" {
		t.Fatal("redaction changed saved inputs")
	}
}
