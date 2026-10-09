package matcheval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

func examples(t *testing.T) Dataset {
	t.Helper()
	raw, e := os.ReadFile("testdata/examples.json")
	if e != nil {
		t.Fatal(e)
	}
	var d Dataset
	if e = json.Unmarshal(raw, &d); e != nil {
		t.Fatal(e)
	}
	if e = d.Validate(); e != nil {
		t.Fatal(e)
	}
	return d
}
func TestGraderSeparatesContractFromHumanPreference(t *testing.T) {
	d := examples(t)
	c, trial := d.Cases[0], d.Recorded[0]
	s := Grade(c, trial)
	if !s.ContractPass || s.TopAgreement == nil || !*s.TopAgreement || s.PairCorrect != 1 {
		t.Fatal(s)
	}
	wrong := *trial.Report
	wrong.Choices = append([]matching.CompanyChoice{}, wrong.Choices...)
	wrong.Choices[0].Rank, wrong.Choices[1].Rank = 2, 1
	trial.Report = &wrong
	s = Grade(c, trial)
	if !s.ContractPass || s.TopAgreement == nil || *s.TopAgreement || s.PairCorrect != 0 {
		t.Fatal("valid quotes incorrectly imply good ranking", s)
	}
	c.Reference.Reviewed = false
	s = Grade(c, trial)
	if s.QualityScored || s.TopAgreement != nil {
		t.Fatal("unreviewed reference was scored", s)
	}
}
func TestInvalidAndFailedTrialsRemainInLabeledDenominator(t *testing.T) {
	d := examples(t)
	c, trial := d.Cases[0], d.Recorded[0]
	broken := *trial.Report
	broken.Choices = append([]matching.CompanyChoice{}, broken.Choices...)
	broken.Choices[0].JobExcerpt = "invented"
	trial.Report = &broken
	s := Grade(c, trial)
	if s.ContractPass || !s.QualityScored || s.TopAgreement == nil || *s.TopAgreement {
		t.Fatal(s)
	}
	trial.Report = nil
	trial.ErrorCode = "TIMEOUT_OR_CANCELLED"
	s = Grade(c, trial)
	if !s.QualityScored || *s.TopAgreement {
		t.Fatal(s)
	}
	summary := Summarize("recorded", []Score{s})
	if summary.QualityScored != 1 || summary.TopAgreement != 0 || summary.MissingUsage != 1 {
		t.Fatal(summary)
	}
}
func TestJointFirstChoiceCannotIncludeUnacceptableJob(t *testing.T) {
	d := examples(t)
	c, trial := d.Cases[0], d.Recorded[0]
	r := *trial.Report
	r.Choices = append([]matching.CompanyChoice{}, r.Choices...)
	r.Choices[1].Rank = 1
	trial.Report = &r
	s := Grade(c, trial)
	if !s.ContractPass || *s.TopAgreement {
		t.Fatal(s)
	}
	c.Reference.AcceptableTopIDs = []string{c.Jobs[0].ID, c.Jobs[1].ID}
	c.Reference.Pairs = []Pair{{c.Jobs[0].ID, c.Jobs[1].ID, "TIED"}}
	s = Grade(c, trial)
	if !*s.TopAgreement || s.PairCorrect != 1 {
		t.Fatal(s)
	}
}
func TestDatasetRejectsCrossScopeAndDuplicateReferences(t *testing.T) {
	d := examples(t)
	d.Cases[0].Reference.AcceptableTopIDs = []string{"other-account-job"}
	if d.Validate() == nil {
		t.Fatal("cross-scope reference accepted")
	}
	d = examples(t)
	d.Cases = append(d.Cases, d.Cases[0])
	if d.Validate() == nil {
		t.Fatal("duplicate case accepted")
	}
	d = examples(t)
	d.Cases[0].Reference.Pairs = append(d.Cases[0].Reference.Pairs, Pair{d.Cases[0].Jobs[1].ID, d.Cases[0].Jobs[0].ID, "ABOVE"})
	if d.Validate() == nil {
		t.Fatal("contradictory pair accepted")
	}
}
func TestLiveUsesApplicationPromptAndActualUsageWithoutSavingInputs(t *testing.T) {
	d := examples(t)
	c := d.Cases[0]
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var wire struct {
			Messages []map[string]string `json:"messages"`
		}
		if e := json.NewDecoder(r.Body).Decode(&wire); e != nil {
			t.Fatal(e)
		}
		if len(wire.Messages) != 2 || wire.Messages[0]["content"] != matching.CompanyHolisticPrompt || !strings.Contains(wire.Messages[1]["content"], "本人已核对") {
			t.Error("different live analysis path")
		}
		reply := d.Recorded[0].Report
		content := map[string]any{"summary": reply.Summary, "choices": reply.Choices, "questions": reply.Questions}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(mustJSON(content))}}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20}})
	}))
	defer srv.Close()
	client := analysis.NewChat(srv.URL, "synthetic-key", "test-model", 1)
	trial := RunLive(context.Background(), c, 1, client)
	if calls != 1 || trial.Report == nil || !trial.Usage.Known || trial.Usage.PromptTokens != 100 || trial.LatencyMS == nil {
		t.Fatal(trial, calls)
	}
	if !Grade(c, trial).ContractPass {
		t.Fatal("live result could not be replayed")
	}
}
func mustJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }

func TestDatasetRejectsNegativeMeasurements(t *testing.T) {
	d := examples(t)
	negative := int64(-1)
	d.Recorded[0].LatencyMS = &negative
	if d.Validate() == nil {
		t.Fatal("negative latency accepted")
	}
	d = examples(t)
	d.Recorded[0].Usage.PromptTokens = -1
	if d.Validate() == nil {
		t.Fatal("negative token count accepted")
	}
}
