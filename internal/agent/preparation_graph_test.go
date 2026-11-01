package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestPreparationGraphStepReceiptAndCapabilities(t *testing.T) {
	g := NewPreparationGraphClient("http://127.0.0.1:1", "synthetic-service-token")
	state := prepTestState()
	state.Engine = "langgraph"
	v := p.AgentExecution{ID: d.ID(), State: "RUNNING", Preparation: state}
	modelCalls := 0
	checkpoints := 0
	session := &preparationGraphSession{ctx: context.Background(), user: "scoped-owner", v: &v,
		model: prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
			modelCalls++
			return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(prepTestPlan(state)))}}}, nil
		}),
		checkpoint: func() error { checkpoints++; return nil }}
	g.sessions["synthetic-capability"] = session
	request := func(token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/internal/preparation/step", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		g.InternalStep(w, req)
		return w
	}
	if got := request("foreign", `{"action":"sync","sequence":0}`); got.Code != 401 {
		t.Fatal(got.Code)
	}
	if got := request("synthetic-capability", `{"action":"model","sequence":0,"user":"other"}`); got.Code != 400 {
		t.Fatal(got.Code)
	}
	if got := request("synthetic-capability", `{"action":"model","sequence":0}`); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	if state.GraphSequence != 1 || modelCalls != 1 || len(v.Calls) != 1 {
		t.Fatal(state.GraphSequence, modelCalls, v.Calls)
	}
	if got := request("synthetic-capability", `{"action":"model","sequence":0}`); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if modelCalls != 1 {
		t.Fatal("replayed provider call")
	}
	if got := request("synthetic-capability", `{"action":"tool","sequence":1}`); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	if v.State != "COMPLETED" || state.GraphSequence != 2 || checkpoints < 3 {
		t.Fatal(v.State, state.GraphSequence, checkpoints)
	}
	if got := request("synthetic-capability", `{"action":"tool","sequence":1}`); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if state.ToolCalls != 1 {
		t.Fatal("replayed completed tool")
	}
	session.closed = true
	if got := request("synthetic-capability", `{"action":"sync","sequence":2}`); got.Code != 409 {
		t.Fatal("closed lease accepted", got.Code)
	}
}

func TestPreparationGraphUnknownRefusesDispatch(t *testing.T) {
	g := NewPreparationGraphClient("http://127.0.0.1:1", "synthetic")
	state := prepTestState()
	state.Engine = "langgraph"
	state.ProviderState = "UNKNOWN"
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	g.sessions["cap"] = &preparationGraphSession{ctx: context.Background(), v: &v, checkpoint: func() error { t.Fatal("unknown dispatch checkpointed"); return nil }}
	req := httptest.NewRequest("POST", "/internal/preparation/step", strings.NewReader(`{"action":"model","sequence":0}`))
	req.Header.Set("Authorization", "Bearer cap")
	w := httptest.NewRecorder()
	g.InternalStep(w, req)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}

func TestPreparationGraphCheckpointFenceAndMinimalState(t *testing.T) {
	g := NewPreparationGraphClient("http://127.0.0.1:1", "synthetic")
	state := prepTestState()
	state.Engine = "langgraph"
	private := "private-material-and-secret-not-in-graph"
	state.Sources[0].Text = private
	state.Messages = json.RawMessage(`[{"role":"user","content":"` + private + `"}]`)
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	raw, _ := json.Marshal(preparationGraphSnapshot(&v))
	if strings.Contains(string(raw), private) || strings.Contains(string(raw), `"sources"`) || strings.Contains(string(raw), `"messages"`) {
		t.Fatal("private context leaked into graph state")
	}
	count := 0
	g.sessions["cap"] = &preparationGraphSession{ctx: context.Background(), v: &v, model: prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		t.Fatal("model sent after checkpoint failure")
		return Reply{}, nil
	}), checkpoint: func() error {
		count++
		if count == 1 {
			return nil
		}
		return p.ErrConflict
	}}
	req := httptest.NewRequest("POST", "/internal/preparation/step", strings.NewReader(`{"action":"model","sequence":0}`))
	req.Header.Set("Authorization", "Bearer cap")
	w := httptest.NewRecorder()
	g.InternalStep(w, req)
	if w.Code != 409 {
		t.Fatal("undurable step acknowledged", w.Code)
	}
}

func TestPreparationGraphSuccessRequiresBusinessReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "complete", "graph_version": PreparationGraphVersion})
	}))
	defer server.Close()
	g := NewPreparationGraphClient(server.URL, "test-token")
	v := p.AgentExecution{ID: d.ID(), State: "RUNNING", Preparation: prepTestState()}
	if err := g.Run(context.Background(), "owner", &v, nil, nil, func() error { return nil }); err == nil || v.State != "FAILED" {
		t.Fatal("graph success substituted for business state", err, v.State)
	}
}

func TestPreparationGraphInvalidURLDoesNotPanic(t *testing.T) {
	g := NewPreparationGraphClient("://invalid", "synthetic")
	if err := g.Health(context.Background()); err == nil {
		t.Fatal("invalid URL accepted")
	}
}
