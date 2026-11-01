package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/defensestrategy"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestDefenseTeachingStrategyReachesActualPrompt(t *testing.T) {
	s, _ := defenseFixture(t, "go-langgraph")
	if err := json.Unmarshal([]byte(`{"teaching_strategy":{"schema":"defense-teaching-v1","explain":"observation-first","followup":"contrast","reading":"gap-only"}}`), s.Defense); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.JSON(defenseMessages(s.Input, s.Sources, s.Missing, s.Defense)), "先指出本轮原回答中的具体表述") {
		t.Fatal("candidate teaching strategy never reaches product model prompt")
	}
}

func TestStrategyProposalUsesDevelopmentOnlyAndCannotExpandScope(t *testing.T) {
	base := defensestrategy.Default()
	diagnostics := StrategyDiagnostics{Split: "development", DatasetSHA: strings.Repeat("a", 64), ReportSHA: strings.Repeat("b", 64), Base: base, Observations: []StrategyObservation{{CaseID: "dev-only", State: "COMPLETED", ProgramMet: true, InvalidCalls: 1}}}
	candidate := StrategyCandidate{ParentSHA: base.ID(), Hypothesis: "用对照追问核对当前回答中的遗漏。", Cases: []string{"dev-only"}, Profile: base}
	candidate.Profile.Followup = "contrast"
	calls := 0
	model := prepModelFunc(func(_ context.Context, ms []Message, defs []Definition) (Reply, error) {
		calls++
		if len(defs) != 1 || strings.Contains(ms[1].Content, "validation") {
			t.Fatal("proposal scope expanded")
		}
		return Reply{Calls: []Call{{Name: "propose_defense_teaching_strategy", Args: json.RawMessage(d.JSON(candidate))}}}, nil
	})
	if _, err := ProposeDefenseStrategy(context.Background(), model, diagnostics); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	diagnostics.Split = "validation"
	if _, err := ProposeDefenseStrategy(context.Background(), model, diagnostics); err == nil || calls != 1 {
		t.Fatal("validation reached proposer")
	}
	diagnostics.Split = "development"
	candidate.Cases = []string{"held-out"}
	if _, err := ProposeDefenseStrategy(context.Background(), model, diagnostics); err == nil {
		t.Fatal("unseen failure reference accepted")
	}
	candidate.Cases = []string{"dev-only"}
	candidate.Profile.Followup = "execute_write"
	if _, err := ProposeDefenseStrategy(context.Background(), model, diagnostics); err == nil {
		t.Fatal("scope mutation accepted")
	}
}

func TestStrategySnapshotAndProgramGuardsSurviveContinuation(t *testing.T) {
	s, r := defenseFixture(t, "go-langgraph")
	custom := defensestrategy.Default()
	custom.Followup = "contrast"
	s.Defense.TeachingStrategy = &custom
	s.Defense.Review = &r
	child, err := newDefenseState(s.Input, s.Defense)
	if err != nil || child.TeachingStrategy == nil || *child.TeachingStrategy != custom {
		t.Fatal(child, err)
	}
	readDefenseFixture(t, s)
	r.Findings[0].References[0].Quote = "fabricated"
	if validateDefenseReview(s, r) == nil {
		t.Fatal("teaching profile bypassed citation validation")
	}
	for _, def := range defenseDefinitions(s) {
		if def.Write || (def.Name != "read_project_defense_evidence" && def.Name != "finish_project_defense_review") {
			t.Fatal("policy expanded tools")
		}
	}
}
