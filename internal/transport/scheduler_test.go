package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
