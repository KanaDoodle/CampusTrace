package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRuntimeEval(t *testing.T) {
	for _, s := range RunEval() {
		t.Run(s.Name, func(t *testing.T) {
			if !s.Success || !s.SafetyPass {
				t.Fatalf("%+v", s)
			}
		})
	}
}
func TestSizeAndPermission(t *testing.T) {
	if Validate("search_jobs", []byte(`{"query":"`+strings.Repeat("a", 1001)+`"}`)) == nil {
		t.Fatal("oversize query")
	}
	if Validate("get_job", []byte(`{"job_id":"bad","user_id":"other"}`)) == nil {
		t.Fatal("owner override")
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Runtime{Model: &Scripted{}, Tools: &EvalTools{}, MaxModels: 4, MaxTools: 8, Deadline: time.Second, ToolTimeout: time.Second}
	v := r.Run(ctx, "u", "s", "question", nil)
	if v.Terminal != "CANCELLED" {
		t.Fatal(v)
	}
}

func TestModelCannotInventFactsAfterRead(t *testing.T) {
	m := &Scripted{Replies: []Reply{{Calls: []Call{{ID: "c", Name: "get_project_facts", Args: []byte(`{}`)}}}, {Text: "I implemented 100000 QPS and have 1 million production users"}}}
	r := Runtime{Model: m, Tools: &EvalTools{}, MaxModels: 4, MaxTools: 8, Deadline: time.Second, ToolTimeout: time.Second}
	v := r.Run(context.Background(), "u", "s", "项目做了什么", nil)
	if strings.Contains(v.Answer, "100000") || strings.Contains(v.Answer, "million") {
		t.Fatal("ungrounded model prose leaked")
	}
}
