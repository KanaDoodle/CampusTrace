package agent

import (
	"slices"
	"testing"
)

func TestSyntheticJourneyEvaluation(t *testing.T) {
	report, err := RunJourneyEval(JourneyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.Total < 40 || !report.AllPassed() {
		for _, c := range report.Cases {
			if !c.Passed {
				t.Errorf("%s: %v; got %v (%s)", c.ID, c.Failures, c.ActualTools, c.Terminal)
			}
		}
		t.Fatalf("journey baseline failed: %+v", report.Metrics)
	}
	if report.Metrics.AbstentionTotal < 5 || report.Metrics.Abstention != report.Metrics.AbstentionTotal {
		t.Fatal("unsupported requests were not withheld", report.Metrics)
	}
	if report.Metrics.ModelCalls < report.Metrics.Total || report.Metrics.ToolCalls == 0 {
		t.Fatal("call accounting was not collected", report.Metrics)
	}
}

func TestJourneyEvaluationDetectsWrongToolAndNoEvidence(t *testing.T) {
	report, err := RunJourneyEval(JourneyOptions{Only: "match-go", ModelFactory: func() Model { return &Scripted{} }})
	if err != nil {
		t.Fatal(err)
	}
	if report.AllPassed() || report.Metrics.Total != 1 || report.Cases[0].ToolSelection || !slices.Contains(report.Cases[0].Failures, "tool_selection") {
		t.Fatal("evaluation did not detect a tool-selection regression", report)
	}
	if _, err := RunJourneyEval(JourneyOptions{Only: "missing-case"}); err == nil {
		t.Fatal("unknown case filter silently accepted")
	}
}
