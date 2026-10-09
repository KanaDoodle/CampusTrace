package matching

import (
	"errors"
	"strings"
	"testing"
)

func TestCompanyChatRequiresCompleteSnapshotAndAllowsTiedRanks(t *testing.T) {
	c, j, _ := wholeFixture()
	k := j
	k.ID = "k"
	jobs := []HolisticJob{j, k}
	choice := CompanyChoice{ID: j.ID, Rank: 1, Reason: "可迁移的后端能力", Advantage: "相关服务实践", Tradeoff: "规模待确认", JobExcerpt: "开发后端服务", Evidence: []Citation{{ID: "go", Excerpt: "Go"}}}
	other := choice
	other.ID = k.ID
	doc := CompanyChatDocument{Version: CompanyChatVersion, PromptRevision: HolisticPromptRevision, CandidateHash: c.Hash(), Company: j.Company, InputKey: CompanyInputKey(c, jobs, ChatIdentity), JobIDs: []string{k.ID, j.ID}, Summary: "两岗可并列考虑", Choices: []CompanyChoice{other, choice}, Questions: []string{}}
	if _, err := PrepareCompanyChat(doc, c, jobs); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"missing", "duplicate", "foreign", "hash", "key", "revision", "quote", "rank"} {
		copy := doc
		copy.JobIDs = append([]string{}, doc.JobIDs...)
		copy.Choices = append([]CompanyChoice{}, doc.Choices...)
		switch variant {
		case "missing":
			copy.Choices = copy.Choices[:1]
		case "duplicate":
			copy.JobIDs[1] = copy.JobIDs[0]
		case "foreign":
			copy.Choices[0].ID = "foreign"
		case "hash":
			copy.CandidateHash = "different"
		case "key":
			copy.InputKey = "different"
		case "revision":
			copy.PromptRevision = ReviewedDocumentVersion
		case "quote":
			copy.Choices[0].JobExcerpt = "编造任职要求"
		case "rank":
			copy.Choices[0].Rank = 3
		}
		if _, err := PrepareCompanyChat(copy, c, jobs); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid company chat accepted", variant, err)
		}
	}
}

func TestDecisionPromptsKeepOpenLanguageListsAndTrainingSeparateFromHardGates(t *testing.T) {
	for _, prompt := range []string{HolisticPrompt, CompanyHolisticPrompt, CompanyChatPrompt} {
		for _, phrase := range []string{"不是封闭清单", "区分任职要求、工作职责和培养安排", "当前资料支撑不足", "不能因为简历未写而扣技术匹配", "Agent/RAG 应用不自动证明", "同类岗位的实际工作差异"} {
			if !strings.Contains(prompt, phrase) {
				t.Fatal("decision instruction absent", phrase)
			}
		}
	}
}
