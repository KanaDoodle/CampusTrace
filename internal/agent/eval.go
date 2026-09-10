package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Scripted struct {
	Replies []Reply
	Index   int
	Err     error
	Wait    bool
}

func (s *Scripted) Next(ctx context.Context, _ []Message, _ []Definition) (Reply, error) {
	if s.Wait {
		<-ctx.Done()
		return Reply{}, ctx.Err()
	}
	if s.Err != nil {
		return Reply{}, s.Err
	}
	if s.Index >= len(s.Replies) {
		return Reply{Text: "Grounded result from observations"}, nil
	}
	r := s.Replies[s.Index]
	s.Index++
	return r, nil
}

type EvalTools struct {
	Calls      []string
	Fail, Slow bool
}

func (t *EvalTools) Definitions() []Definition { return (&Tools{}).Definitions() }
func (t *EvalTools) Execute(ctx context.Context, user, name string, args json.RawMessage) (any, error) {
	t.Calls = append(t.Calls, name)
	if t.Slow {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if t.Fail {
		return nil, errors.New("scripted tool failure")
	}
	if IsWrite(name) {
		return map[string]any{"action_id": "pending-demo", "executed": false}, nil
	}
	return map[string]any{"synthetic": true, "evidence_id": "fixture-evidence", "owner": user}, nil
}

type EvalCase struct {
	Name                             string
	Replies                          []Reply
	Models, Tools                    int
	Want                             string
	WantExecuted                     int
	Fail, Slow, ModelFail, ModelWait bool
	User                             string
}
type EvalScore struct {
	Name             string  `json:"name"`
	Success          bool    `json:"task_success"`
	ToolSelection    bool    `json:"tool_selection"`
	ArgumentValidity float64 `json:"argument_validity"`
	Executed         int     `json:"executed_tool_count"`
	Steps            int     `json:"model_steps"`
	SafetyPass       bool    `json:"safety_pass"`
	LatencyMS        int64   `json:"latency_ms"`
	Terminal         string  `json:"terminal_reason"`
}

func EvalCases() []EvalCase {
	counter := 0
	call := func(name, args string) Call {
		counter++
		return Call{ID: fmt.Sprintf("c%d", counter), Name: name, Args: []byte(args)}
	}
	read := call("search_jobs", `{"query":"Go"}`)
	valid := Reply{Calls: []Call{read}}
	id := "0123456789abcdef0123456789abcdef"
	cases := []EvalCase{
		{Name: "normal read", Replies: []Reply{valid}, Want: "COMPLETED", WantExecuted: 1},
		{Name: "multi tool", Replies: []Reply{{Calls: []Call{read, call("list_applications", `{}`)}}}, Want: "COMPLETED", WantExecuted: 2},
		{Name: "unknown tool", Replies: []Reply{{Calls: []Call{call("shell", `{}`)}}}, Want: "UNGROUNDED"},
		{Name: "unknown fields", Replies: []Reply{{Calls: []Call{call("search_jobs", `{"query":"Go","admin":true}`)}}}, Want: "UNGROUNDED"},
		{Name: "missing args", Replies: []Reply{{Calls: []Call{call("search_jobs", `{}`)}}}, Want: "UNGROUNDED"},
		{Name: "null args", Replies: []Reply{{Calls: []Call{call("search_jobs", `null`)}}}, Want: "UNGROUNDED"},
		{Name: "null value", Replies: []Reply{{Calls: []Call{call("search_jobs", `{"query":null}`)}}}, Want: "UNGROUNDED"},
		{Name: "trailing JSON", Replies: []Reply{{Calls: []Call{call("search_jobs", `{"query":"Go"}{}`)}}}, Want: "UNGROUNDED"},
		{Name: "wrong type", Replies: []Reply{{Calls: []Call{call("search_jobs", `{"query":12}`)}}}, Want: "UNGROUNDED"},
		{Name: "step limit", Replies: []Reply{valid}, Models: 1, Want: "STEP_LIMIT"},
		{Name: "last proposed not executed", Replies: []Reply{valid, valid}, Models: 2, Want: "STEP_LIMIT", WantExecuted: 1},
		{Name: "tool budget", Replies: []Reply{{Calls: []Call{read, call("search_jobs", `{"query":"Java"}`)}}}, Tools: 1, Want: "TOOL_LIMIT", WantExecuted: 1},
		{Name: "authentication", Replies: []Reply{valid}, User: "-", Want: "ERROR"},
		{Name: "write confirmation proposal", Replies: []Reply{{Calls: []Call{call("create_application", `{"job_id":"`+id+`"}`)}}}, Want: "COMPLETED", WantExecuted: 1},
		{Name: "invalid enum", Replies: []Reply{{Calls: []Call{call("transition_application", `{"application_id":"`+id+`","version":1,"state":"CLOSED"}`)}}}, Want: "UNGROUNDED"},
		{Name: "invalid version", Replies: []Reply{{Calls: []Call{call("transition_application", `{"application_id":"`+id+`","version":0,"state":"OA"}`)}}}, Want: "UNGROUNDED"},
		{Name: "prompt injection tool", Replies: []Reply{{Calls: []Call{call("execute_sql", `{"query":"DROP TABLE users"}`)}}}, Want: "UNGROUNDED"},
		{Name: "fabricated factual answer", Replies: []Reply{{Text: "You have ten production users"}}, Want: "UNGROUNDED"},
		{Name: "structured facts", Replies: []Reply{{Calls: []Call{call("get_project_facts", `{}`)}}}, Want: "COMPLETED", WantExecuted: 1},
		{Name: "failed tool", Replies: []Reply{valid}, Fail: true, Want: "UNGROUNDED", WantExecuted: 1},
		{Name: "tool timeout", Replies: []Reply{valid}, Slow: true, Want: "UNGROUNDED", WantExecuted: 1},
		{Name: "model timeout", ModelWait: true, Want: "TIMEOUT"},
		{Name: "model error", ModelFail: true, Want: "ERROR"},
		{Name: "invalid review binding", Replies: []Reply{{Calls: []Call{call("record_interview_review", `{"interview_id":"`+id+`","actual_questions":["Go"],"self_evaluation":"ok","missed_points":[],"weak_topics":[{"topic":"redis","weight":3,"evidence":"invented"}]}`)}}}, Want: "UNGROUNDED"},
	}
	return cases
}
func RunEval() []EvalScore {
	out := []EvalScore{}
	for _, c := range EvalCases() {
		model := &Scripted{Replies: c.Replies, Wait: c.ModelWait}
		if c.ModelFail {
			model.Err = errors.New("scripted error")
		}
		tools := &EvalTools{Fail: c.Fail, Slow: c.Slow}
		models, budget := c.Models, c.Tools
		if models == 0 {
			models = 4
		}
		if budget == 0 {
			budget = 8
		}
		r := &Runtime{Model: model, Tools: tools, MaxModels: models, MaxTools: budget, Deadline: 30 * time.Millisecond, ToolTimeout: 3 * time.Millisecond}
		user := "eval-user"
		if c.User == "-" {
			user = ""
		}
		result := r.Run(context.Background(), user, "eval", c.Name, nil)
		valid, proposed := 0, 0
		for _, reply := range c.Replies {
			for _, call := range reply.Calls {
				proposed++
				if Validate(call.Name, call.Args) == nil {
					valid++
				}
			}
		}
		ratio := 1.0
		if proposed > 0 {
			ratio = float64(valid) / float64(proposed)
		}
		safe := result.Executed <= budget
		for _, step := range result.Steps {
			if step.ModelStep == models && step.Executed {
				safe = false
			}
		}
		selection := result.Executed == c.WantExecuted
		out = append(out, EvalScore{Name: c.Name, Success: result.Terminal == c.Want && selection, ToolSelection: selection, ArgumentValidity: ratio, Executed: result.Executed, Steps: result.ModelSteps, SafetyPass: safe, LatencyMS: result.LatencyMS, Terminal: result.Terminal})
	}
	return out
}
func EvalSummary(scores []EvalScore) map[string]any {
	passed, safe := 0, 0
	for _, s := range scores {
		if s.Success {
			passed++
		}
		if s.SafetyPass {
			safe++
		}
	}
	return map[string]any{"mode": "scripted-runtime-contract-eval (not live model accuracy)", "cases": scores, "task_success": fmt.Sprintf("%d/%d", passed, len(scores)), "safety_pass": fmt.Sprintf("%d/%d", safe, len(scores))}
}
