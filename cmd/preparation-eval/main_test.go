package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	"github.com/KanaDoodle/CampusTrace/internal/projectdefense"
)

type evalRoundTrip func(*http.Request) (*http.Response, error)

func TestDefenseEvaluationRequiresExplicitCodeAuthorizationAndProductGraph(t *testing.T) {
	tasks := []agent.PreparationEvalCase{{Input: d.PreparationInput{Defense: &d.DefenseInput{Topic: "go-langgraph", Answer: "回答"}}}}
	if err := validateDefenseEvaluation(tasks, []string{"dynamic"}, false, true, ""); err == nil {
		t.Fatal("code sent without exact evidence authorization")
	}
	if err := validateDefenseEvaluation(tasks, []string{"dynamic"}, false, true, projectdefense.Version()); err != nil {
		t.Fatal(err)
	}
	for _, arms := range [][]string{{"single-prompt"}, {"dynamic", "fixed-plus-synthesis"}} {
		if err := validateDefenseEvaluation(tasks, arms, false, false, ""); err == nil {
			t.Fatal("wrong execution path accepted")
		}
	}
	if err := validateDefenseEvaluation(tasks, []string{"dynamic"}, true, false, ""); err == nil {
		t.Fatal("legacy defense executor accepted")
	}
}

func (f evalRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLiveRequestCarriesOutputCapThroughCallLimiter(t *testing.T) {
	remaining := 1
	model := newLiveModel(modelconfig.Config{URL: "https://api.deepseek.com/chat/completions", Model: "deepseek-flash", APIKey: "synthetic-key"}, &remaining)
	calls := 0
	client := model.inner.(agent.LiveModel).Client
	client.HTTP = &http.Client{Transport: evalRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["max_tokens"] != float64(2048) {
			t.Fatal("output cap missing from actual request", body["max_tokens"])
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)), Header: http.Header{}}, nil
	})}
	if _, err := model.Next(context.Background(), []agent.Message{{Role: "user", Content: "synthetic"}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := model.Next(context.Background(), nil, nil); err == nil {
		t.Fatal("call cap ignored")
	}
	if calls != 1 || remaining != 0 {
		t.Fatal(calls, remaining)
	}
}

func TestNearestRankDoesNotReportMedianAsSmallSampleP95(t *testing.T) {
	if got := nearestRank([]float64{10, 20, 90}, .95); got != 90 {
		t.Fatal(got)
	}
	if got := nearestRank([]float64{10, 20, 90}, .5); got != 20 {
		t.Fatal(got)
	}
	if got := nearestRank(nil, .95); got != 0 {
		t.Fatal(got)
	}
}

func TestEvaluationArmBudgetMatchesActualSelectedPaths(t *testing.T) {
	for _, c := range []struct {
		value  string
		weight int
	}{{"dynamic", 6}, {"single-prompt,fixed-plus-synthesis", 2}, {"single-prompt,fixed-plus-synthesis,dynamic", 8}} {
		_, weight, err := evaluationArms(c.value)
		if err != nil || weight != c.weight {
			t.Fatal(c, weight, err)
		}
	}
	for _, bad := range []string{"dynamic,dynamic", "", "other"} {
		if _, _, err := evaluationArms(bad); err == nil {
			t.Fatal("invalid arm accepted", bad)
		}
	}
}
