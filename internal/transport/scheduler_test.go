package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/modelcontrol"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestModelControlFailuresPreserveActionableHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{p.ErrModelQueueFull, 503, "MODEL_GLOBAL_QUEUE_FULL"},
		{modelcontrol.ErrWaitDeadline, 503, "MATCH_QUEUE_TIMEOUT"},
		{p.ErrModelBudget, 429, "MODEL_GLOBAL_BUDGET"},
		{p.ErrModelReview, 409, "MODEL_REVIEW_REQUIRED"},
		{p.ErrModelLease, 409, "MODEL_EXECUTION_LOST"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			matchFailure(rec, tc.err)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatal(rec.Code, rec.Body.String())
			}
			if tc.status == 503 && rec.Header().Get("Retry-After") != "2" {
				t.Fatal("missing overload retry hint")
			}
		})
	}
}

func TestMatchingAdmissionReturnsRetryAfterBeforeAnyDatabaseOrModelWork(t *testing.T) {
	api := &API{}
	runner := api.StartMatchTasks(context.Background(), 2)
	defer runner.Close()
	for i := 0; i < 16; i++ {
		runner.admission <- struct{}{}
	}
	routes := map[string]http.HandlerFunc{}
	api.matchingTaskRoutes(func(pattern string, h http.HandlerFunc) { routes[pattern] = h })
	rec := httptest.NewRecorder()
	routes["POST /api/matching/tasks"](rec, httptest.NewRequest("POST", "/api/matching/tasks", strings.NewReader("{}")))
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "2" || !strings.Contains(rec.Body.String(), "MATCH_QUEUE_FULL") {
		t.Fatal(rec.Code, rec.Header(), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	routes["GET /api/matching/tasks/scheduler"](rec, httptest.NewRequest("GET", "/api/matching/tasks/scheduler", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"admitted":16`) || !strings.Contains(rec.Body.String(), `"scope":"api_instance"`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestRetiredExecutionCannotRemoveResumedCancellationHandle(t *testing.T) {
	api := &API{}
	r := api.StartMatchTasks(context.Background(), 1)
	defer r.Close()
	oldCtx, oldCancel := context.WithCancel(context.Background())
	defer oldCancel()
	freshCtx, freshCancel := context.WithCancel(context.Background())
	defer freshCancel()
	r.active["run"] = matchExecution{cancel: oldCancel, token: "old"}
	r.active["run"] = matchExecution{cancel: freshCancel, token: "fresh"}
	r.forgetExecution("run", "old")
	r.cancelRun("run")
	if freshCtx.Err() == nil || oldCtx.Err() != nil {
		t.Fatal("old cleanup replaced or removed fresh cancellation")
	}
	r.forgetExecution("run", "fresh")
	if len(r.active) != 0 {
		t.Fatal("fresh execution leaked")
	}
}
