package matching

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func wrappedWholeFixture() (Candidate, HolisticJob, HolisticAssessment) {
	c, j, h := wholeFixture()
	c.Projects[0].Description = "使用 Go 开发任务服\n务，处理消费中\n断，增加单元\n测试。"
	j.Text = "职责：使用 Go 开发后端服\n务，关注可靠性。"
	h.Strengths[0].JobExcerpt = "使用 Go 开发后端服务，关注可靠性"
	h.Strengths[0].Evidence = []Citation{{ID: "project-p-description", Excerpt: "使用Go开发任务服务，处理消费中断，增加单元测试。"}}
	return c, j, h
}

func TestWholeWhitespaceRepairRestoresLiteralQuotesWithoutMutatingInputs(t *testing.T) {
	c, j, h := wrappedWholeFixture()
	before, candidateBefore := d.JSON(h), c.Hash()
	if err := ValidateHolistic(h, j.Text, c); !errors.Is(err, ErrInvalid) {
		t.Fatal("persistence validator accepted nonliteral quote", err)
	}
	prepared, err := PrepareHolisticAssessment(h, j.Text, c)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Strengths[0].JobExcerpt != "使用 Go 开发后端服\n务，关注可靠性" || prepared.Strengths[0].Evidence[0].Excerpt != c.Projects[0].Description {
		t.Fatal("literal source not restored", prepared)
	}
	if err := ValidateHolistic(prepared, j.Text, c); err != nil || d.JSON(h) != before || c.Hash() != candidateBefore {
		t.Fatal("repair changed inputs or failed strict validation", err)
	}
	m := &fakeModel{output: map[string]any{"jobs": []HolisticJobReply{{ID: j.ID, Assessment: h}}}}
	results, err := AnalyzeHolistically(context.Background(), m, c, []HolisticJob{j})
	if err != nil || m.calls != 1 || d.JSON(results[j.ID]) != d.JSON(prepared) {
		t.Fatal("model boundary missed restoration or made an extra call", err, m.calls)
	}
	result, err := ImportChatJob(ChatJob{ID: j.ID, Assessment: &h}, j.Text, d.Job{ID: j.ID}, d.Profile{}, c, time.Time{})
	if err != nil || result.Holistic == nil || d.JSON(*result.Holistic) != d.JSON(prepared) || d.JSON(h) != before {
		t.Fatal("chat boundary missed restoration or mutated submitted document", err)
	}
}

func TestWholeRepairRejectsRewritesAndLocatesWrongSource(t *testing.T) {
	for _, tc := range []struct{ name, quote, id, reason string }{
		{"paraphrase", "使用Go设计服务并处理故障。", "project-p-description", "HOLISTIC_EVIDENCE_EXCERPT_NOT_EXACT"},
		{"ellipsis", "使用Go…增加单元测试。", "project-p-description", "HOLISTIC_EVIDENCE_EXCERPT_NOT_EXACT"},
		{"punctuation", "使用Go开发任务服务,处理消费中断", "project-p-description", "HOLISTIC_EVIDENCE_EXCERPT_NOT_EXACT"},
		{"case", "使用go开发任务服务", "project-p-description", "HOLISTIC_EVIDENCE_EXCERPT_NOT_EXACT"},
		{"wrong-id", "使用Go开发任务服务", "bullet", "HOLISTIC_EVIDENCE_EXCERPT_NOT_EXACT"},
		{"unknown-id", "Go", "unknown", "FACT_UNKNOWN"},
		{"empty", "\n\t", "project-p-description", "HOLISTIC_EVIDENCE_EXCERPT_EMPTY"},
		{"oversized", strings.Repeat("x", 1201), "project-p-description", "HOLISTIC_EVIDENCE_EXCERPT_LENGTH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, j, h := wrappedWholeFixture()
			h.Strengths[0].Evidence = []Citation{{ID: "go", Excerpt: "Go"}, {ID: tc.id, Excerpt: tc.quote}}
			_, err := PrepareHolisticAssessment(h, j.Text, c)
			var v *ValidationError
			if !errors.As(err, &v) || v.Reason != tc.reason || v.Scope != "HOLISTIC_STRENGTH" || v.ItemIndex != 1 || v.CitationIndex != 2 {
				t.Fatal("incorrect safe diagnostic", err, v)
			}
		})
	}
	for _, kind := range []string{"CITY_PREFERRED", "PLANNED", "LIMITATION"} {
		c, j, h := wrappedWholeFixture()
		c.Facts = append(c.Facts, Fact{ID: "invalid", Kind: kind, Text: "内容\n排版"})
		h.Strengths[0].Evidence = []Citation{{ID: "invalid", Excerpt: "内容排版"}}
		_, err := PrepareHolisticAssessment(h, j.Text, c)
		var v *ValidationError
		if !errors.As(err, &v) || v.Reason != "INVALID_ABILITY_EVIDENCE" || v.CitationIndex != 1 {
			t.Fatal("formatting restoration bypassed evidence kind", kind, err)
		}
	}
}

func TestWholeRepairRejectsAmbiguousAndOversizedSourceSpans(t *testing.T) {
	for _, tc := range []struct{ source, quote, reason string }{
		{"任务\n服务。任务\t服务。", "任务服务", "HOLISTIC_EVIDENCE_EXCERPT_AMBIGUOUS"},
		{"开" + strings.Repeat(" ", 1200) + "发", "开发", "HOLISTIC_EVIDENCE_EXCERPT_LENGTH"},
		{"处理 10 次失败", "处理100次失败", "HOLISTIC_EVIDENCE_EXCERPT_NOT_EXACT"},
		{"使用 PostgreSQL。", "使用MySQL。", "HOLISTIC_EVIDENCE_EXCERPT_NOT_EXACT"},
	} {
		c, j, h := wrappedWholeFixture()
		c.Projects[0].Description = tc.source
		h.Strengths[0].Evidence[0].Excerpt = tc.quote
		_, err := PrepareHolisticAssessment(h, j.Text, c)
		var v *ValidationError
		if !errors.As(err, &v) || v.Reason != tc.reason {
			t.Fatal("unsafe restoration", err)
		}
	}
}

func TestWholeDiagnosticsUseInputJobOrderAndFindingGroup(t *testing.T) {
	c, j, h := wrappedWholeFixture()
	k := j
	k.ID = "k"
	k.Text = "另外一个岗位的原文"
	good, err := PrepareHolisticAssessment(h, j.Text, c)
	if err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.Strengths = []HolisticFinding{}
	bad.Gaps = []HolisticFinding{{Point: "需核对", Explanation: "需要进一步确认", JobExcerpt: "其他来源的改写文字", Evidence: []Citation{}}}
	m := &fakeModel{output: map[string]any{"jobs": []HolisticJobReply{{ID: k.ID, Assessment: bad}, {ID: j.ID, Assessment: good}}}}
	_, err = AnalyzeHolistically(context.Background(), m, c, []HolisticJob{j, k})
	var v *ValidationError
	if !errors.As(err, &v) || v.JobIndex != 2 || v.Scope != "HOLISTIC_GAP" || v.ItemIndex != 1 || v.Reason != "HOLISTIC_JOB_EXCERPT_NOT_EXACT" || m.calls != 1 {
		t.Fatal("provider reply order replaced input positions", err, v)
	}
}

func TestCompanyModelAndChatPreparationRestoreQuotesAndKeepStrictValidation(t *testing.T) {
	c, j, h := wrappedWholeFixture()
	k := j
	k.ID = "k"
	choice := CompanyChoice{ID: j.ID, Rank: 1, Reason: "服务经验相关", Advantage: "故障处理经验", Tradeoff: "规模需要核对", JobExcerpt: h.Strengths[0].JobExcerpt, Evidence: h.Strengths[0].Evidence}
	other := choice
	other.ID, other.Rank = k.ID, 2
	r := HolisticCompanyReport{Version: HolisticVersion, Company: j.Company, Summary: "优先服务开发", Choices: []CompanyChoice{other, choice}}
	before := d.JSON(r)
	prepared, err := PrepareCompanyReport(r, c, []HolisticJob{j, k})
	if err != nil || ValidateCompanyReport(prepared, c, []HolisticJob{j, k}) != nil || before != d.JSON(r) {
		t.Fatal("company preparation changed input or failed literal validation", err)
	}
	m := &fakeModel{output: map[string]any{"summary": r.Summary, "choices": r.Choices, "questions": []string{}}}
	fromModel, err := CompareHolistically(context.Background(), m, c, []HolisticJob{j, k}, "fixture")
	if err != nil || m.calls != 1 || d.JSON(fromModel.Choices) != d.JSON(prepared.Choices) {
		t.Fatal("company model boundary failed", err, m.calls)
	}
	r.Choices[0].Evidence = []Citation{{ID: "project-p-description", Excerpt: "改写的材料"}}
	_, err = PrepareCompanyReport(r, c, []HolisticJob{j, k})
	var v *ValidationError
	if !errors.As(err, &v) || v.JobIndex != 2 || v.ItemIndex != 1 || v.Scope != "COMPANY_CHOICE" || v.CitationIndex != 1 {
		t.Fatal("company positions missing", err, v)
	}
}

func TestWholeGatesRestoreFormattingButRejectPreferredQualifications(t *testing.T) {
	c, j, h := wholeFixture()
	j.Text += "\n要求本科及以上\n学历。"
	h.Gates = []HolisticGate{{Type: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Excerpt: "本科及以上学历"}}
	prepared, err := PrepareHolisticAssessment(h, j.Text, c)
	if err != nil || prepared.Gates[0].Excerpt != "本科及以上\n学历" || h.Gates[0].Excerpt != "本科及以上学历" {
		t.Fatal("qualification formatting not restored", err)
	}
	j.Text += "\n硕士\n优先。"
	h.Gates[0] = HolisticGate{Type: "EDUCATION_REQUIREMENT", Value: "MASTER", Excerpt: "硕士优先"}
	_, err = PrepareHolisticAssessment(h, j.Text, c)
	var v *ValidationError
	if !errors.As(err, &v) || v.Reason != "HOLISTIC_GATES" || v.Scope != "HOLISTIC_GATE" || v.ItemIndex != 1 {
		t.Fatal("preferred education became a hard gate", err)
	}
}
