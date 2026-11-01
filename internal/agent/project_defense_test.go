package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func defenseFixture(t *testing.T, topic string) (*d.PreparationState, d.DefenseReview) {
	t.Helper()
	s := prepTestState()
	s.Input.Minutes = 15
	s.Input.Days = 1
	s.Input.RetrievalMode = "off"
	s.Input.Defense = &d.DefenseInput{Topic: topic, Answer: "我还不熟悉这个项目。"}
	var err error
	s.Defense, err = newDefenseState(s.Input, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.QualityVersion = PreparationQualityVersion
	s.Engine = "langgraph"
	s.Messages = json.RawMessage(d.JSON(defenseMessages(s.Input, s.Sources, s.Missing, s.Defense)))
	lookup := map[string]d.PreparationSource{}
	for _, src := range s.Defense.Evidence {
		lookup[src.ID] = src
	}
	r := d.DefenseReview{Summary: "先读一个片段，再复述一个动作", NextAction: "READ", NextQuestion: "请说明刚才片段做了什么。"}
	for _, point := range s.Defense.Points {
		src := lookup[point.Sources[0]]
		r.Findings = append(r.Findings, d.DefenseFinding{Point: point.ID, Verdict: "NOT_COVERED", Explanation: "这次回答没有展开此点，先阅读指定片段。", References: []d.PreparationReference{{SourceID: src.ID, Quote: strings.TrimSpace(strings.Split(src.Text, "\n")[0]), Supports: "该片段用于核对对应规则"}}})
	}
	r.Plan = d.PreparationPlan{Summary: "先阅读，再复述", Items: []d.PreparationItem{{ID: "read", Title: "读一个片段", Minutes: 10, Day: 1, Basis: "GENERAL", Action: "展开依据，阅读这一段函数", Deliverable: "一句话记下函数的动作", Workload: &d.PreparationWorkload{Mode: "READ", Units: 1, Scope: "1个片段：当前执行函数"}, References: r.Findings[0].References}, {ID: "retell", Title: "复述一个动作", Minutes: 5, Day: 1, Basis: "GENERAL", Action: "用自己的话说明其中一步", Deliverable: "一句自己的说明", Workload: &d.PreparationWorkload{Mode: "ORAL", Units: 1, Scope: "1个问题：其中一步由谁完成"}}}}
	r.Plan.Items[1].References = []d.PreparationReference{}
	return s, r
}

func readDefenseFixture(t *testing.T, s *d.PreparationState) {
	t.Helper()
	ids := []string{}
	seen := map[string]bool{}
	for _, point := range s.Defense.Points {
		id := point.Sources[0]
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	_, _, err := defenseTool(s, p.ExecutionCall{Name: "read_project_defense_evidence", Args: json.RawMessage(d.JSON(map[string]any{"ids": ids}))})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectDefenseEvidenceAndAnswerBinding(t *testing.T) {
	for _, topic := range []string{"go-langgraph", "unknown-recovery"} {
		t.Run(topic, func(t *testing.T) {
			s, r := defenseFixture(t, topic)
			if validateDefenseReview(s, r) == nil {
				t.Fatal("unread evidence accepted")
			}
			readDefenseFixture(t, s)
			if err := validateDefenseReview(s, r); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"invented_answer", "invented_quote", "wrong_point_source", "duplicate_point", "over_budget", "fake_history", "stale_evidence"} {
				t.Run(name, func(t *testing.T) {
					var copyState d.PreparationState
					var copyReview d.DefenseReview
					_ = json.Unmarshal([]byte(d.JSON(s)), &copyState)
					_ = json.Unmarshal([]byte(d.JSON(r)), &copyReview)
					switch name {
					case "invented_answer":
						copyReview.Findings[0].Verdict = "SUPPORTED"
						copyReview.Findings[0].AnswerQuote = "我已经完全掌握了"
					case "invented_quote":
						copyReview.Findings[0].References[0].Quote = "not-in-code"
					case "wrong_point_source":
						copyReview.Findings[0].References = copyReview.Findings[1].References
					case "duplicate_point":
						copyReview.Findings[1].Point = copyReview.Findings[0].Point
					case "over_budget":
						copyReview.Plan.Items[0].Minutes = 15
					case "fake_history":
						copyReview.Plan.Items[0].Basis = "HISTORY"
					case "stale_evidence":
						copyState.Defense.Version = "old-version"
					}
					if validateDefenseReview(&copyState, copyReview) == nil {
						t.Fatal("invalid review accepted")
					}
				})
			}
		})
	}
}

func TestProjectDefenseAllowlistBudgetAndLineage(t *testing.T) {
	s, r := defenseFixture(t, "go-langgraph")
	for _, args := range []string{`{"ids":["../../.env.model.local"]}`, `{"ids":["x"],"path":"/etc/passwd"}`} {
		_, _, err := defenseTool(s, p.ExecutionCall{Name: "read_project_defense_evidence", Args: json.RawMessage(args)})
		if err == nil || len(s.Sources) != 1 || s.Defense.EvidenceRead != 0 {
			t.Fatal("scope widened", err)
		}
	}
	readDefenseFixture(t, s)
	readDefenseFixture(t, s)
	readDefenseFixture(t, s)
	before := len(s.Sources)
	_, _, err := defenseTool(s, p.ExecutionCall{Name: "read_project_defense_evidence", Args: json.RawMessage(d.JSON(map[string]any{"ids": []string{s.Defense.Evidence[0].ID}}))})
	if err == nil || len(s.Sources) != before {
		t.Fatal("read budget bypass")
	}
	s.Defense.Review = &r
	child, err := newDefenseState(s.Input, s.Defense)
	if err != nil || child.Question != r.NextQuestion || child.Round != 2 || child.Previous == nil {
		t.Fatal(child, err)
	}
	s.Defense.Round = 4
	if _, err = newDefenseState(s.Input, s.Defense); err == nil {
		t.Fatal("unbounded continuation")
	}
	s.Defense.Round = 1
	other := s.Input
	other.Defense = &d.DefenseInput{Topic: "unknown-recovery", Answer: "回答"}
	if _, err = newDefenseState(other, s.Defense); err == nil {
		t.Fatal("topic changed through old execution")
	}
}

func TestProjectDefenseEvidenceSizeRejectedBeforeMutation(t *testing.T) {
	s, _ := defenseFixture(t, "go-langgraph")
	s.Sources[0].Text = strings.Repeat("a", 131072)
	_, _, err := defenseTool(s, p.ExecutionCall{Name: "read_project_defense_evidence", Args: json.RawMessage(d.JSON(map[string]any{"ids": []string{s.Defense.Evidence[0].ID}}))})
	if err == nil || len(s.Sources) != 1 || s.Defense.EvidenceRead != 0 {
		t.Fatal("oversized read mutated durable context", err)
	}
}

func TestProjectDefenseControlledStepRepairAndKnownReadReuse(t *testing.T) {
	s, r := defenseFixture(t, "go-langgraph")
	v := p.AgentExecution{Preparation: s, State: "RUNNING"}
	models := 0
	model := prepModelFunc(func(_ context.Context, messages []Message, defs []Definition) (Reply, error) {
		models++
		var call Call
		switch models {
		case 1:
			ids := []string{}
			for _, point := range s.Defense.Points {
				ids = append(ids, point.Sources[0])
			}
			call = Call{ID: "read", Name: "read_project_defense_evidence", Args: json.RawMessage(d.JSON(map[string]any{"ids": ids}))}
		case 2:
			bad := r
			bad.Findings = append([]d.DefenseFinding{}, r.Findings...)
			bad.Findings[0].References = []d.PreparationReference{{SourceID: r.Findings[0].References[0].SourceID, Quote: "fabricated", Supports: "bad"}}
			call = Call{ID: "bad", Name: "finish_project_defense_review", Args: json.RawMessage(d.JSON(bad))}
		case 3:
			if !strings.Contains(messages[len(messages)-1].Content, "INVALID_DEFENSE") {
				t.Fatal("no failure feedback")
			}
			call = Call{ID: "fixed", Name: "finish_project_defense_review", Args: json.RawMessage(d.JSON(r))}
		default:
			t.Fatal("unbounded model loop", messages[len(messages)-1].Content)
		}
		return Reply{Calls: []Call{call}}, nil
	})
	for v.State == "RUNNING" {
		action := "model"
		if v.Next < len(v.Calls) {
			action = "tool"
		}
		if err := runPreparationStep(context.Background(), "owner", &v, model, nil, func() error { return nil }, action); err != nil {
			t.Fatal(err)
		}
	}
	if v.State != "COMPLETED" || models != 3 || s.Defense.EvidenceRead != 1 || len(s.Input.Mastered) > 0 || s.Defense.Review == nil || s.GraphSequence != 6 {
		t.Fatal(v.State, models, s.Defense, s.GraphSequence)
	}
}
