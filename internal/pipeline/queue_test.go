package pipeline

import (
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestTaskEnvelopeRejectsInvalidProtocolBeforeDispatch(t *testing.T) {
	base := p.NewTask("ANALYZE", d.ID())
	for _, mutate := range []func(*p.Task){func(t *p.Task) { t.Attempt = 0 }, func(t *p.Task) { t.Attempt = -1 }, func(t *p.Task) { t.Attempt = 101 }, func(t *p.Task) { t.Version = 99 }, func(t *p.Task) { t.ID = "" }, func(t *p.Task) { t.EntityID = "" }, func(t *p.Task) { t.Type = "UNKNOWN" }, func(t *p.Task) { t.OwnerID = "foreign" }, func(t *p.Task) { t.TraceParent = "secret" }} {
		bad := base
		mutate(&bad)
		if _, e := Decode([]byte(d.JSON(bad))); e == nil {
			t.Fatalf("accepted invalid task %+v", bad)
		}
	}
	for _, raw := range []string{"{broken", strings.Repeat("x", 65537), strings.TrimSuffix(d.JSON(base), "}") + `,"secret":"not-a-field"}`} {
		if _, e := Decode([]byte(raw)); e == nil {
			t.Fatal("accepted malformed body")
		}
	}
}
func TestFailureOutcomePreservesIdentityAndBoundsBudget(t *testing.T) {
	task := p.NewTask("ANALYZE", d.ID())
	out := FailureOutcome(task, errors.New("503 connection"), 2)
	if out.State != "RETRY" || out.Delay < 2*time.Second || out.Delay > 3*time.Second {
		t.Fatal(out)
	}
	next, e := Decode(out.Body)
	if e != nil {
		t.Fatal(e)
	}
	if next.ID != task.ID || next.Attempt != 2 || next.FirstFailed.IsZero() {
		t.Fatal(next)
	}
	terminal := FailureOutcome(next, errors.New("503 connection"), 2)
	if terminal.State != "DEAD" {
		t.Fatal("unbounded retries")
	}
	var saved p.Task
	if e = json.Unmarshal(terminal.Body, &saved); e != nil {
		t.Fatal(e)
	}
	if !saved.FirstFailed.Equal(next.FirstFailed) {
		t.Fatal("failure history changed")
	}
	if FailureOutcome(task, errors.New("403 auth"), 4).State != "DEAD" {
		t.Fatal("permanent error retried")
	}
}
