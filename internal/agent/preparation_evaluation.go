package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

type PreparationEvalCase struct {
	ID           string                `json:"id"`
	Split        string                `json:"split"`
	MaterialKind string                `json:"material_kind"`
	Input        d.PreparationInput    `json:"input"`
	Missing      []string              `json:"missing"`
	Sources      []d.PreparationSource `json:"sources"`
	Corpus       []d.PreparationSource `json:"corpus"`
	PreviousPlan *d.PreparationPlan    `json:"previous_plan,omitempty"`
	Expected     string                `json:"expected"`
	Control      string                `json:"control,omitempty"`
	ReviewNote   string                `json:"review_note"`
}
type PreparationEvalResult struct {
	RetrievalNewSources   int                   `json:"retrieval_new_sources"`
	RetrievalCitedSources int                   `json:"retrieval_cited_sources"`
	CaseID                string                `json:"case_id"`
	Arm                   string                `json:"arm"`
	State                 string                `json:"state"`
	ProgramExpectationMet bool                  `json:"program_expectation_met"`
	PlanValid             bool                  `json:"plan_valid"`
	SourcesUsed           []string              `json:"sources_used"`
	Calls                 int                   `json:"model_calls"`
	ToolCalls             int                   `json:"tool_calls"`
	InvalidCalls          int                   `json:"invalid_calls"`
	FailedToolCalls       int                   `json:"failed_tool_calls"`
	RepeatedQueries       int                   `json:"repeated_queries"`
	LatencyMS             float64               `json:"latency_ms"`
	Usage                 TokenBudget           `json:"usage"`
	EstimatedCostUSD      *float64              `json:"estimated_cost_usd"`
	Plan                  *d.PreparationPlan    `json:"plan,omitempty"`
	Question              string                `json:"question,omitempty"`
	Trace                 []d.PreparationTrace  `json:"trace"`
	BusinessState         string                `json:"business_state"`
	Error                 string                `json:"error,omitempty"`
	Answer                string                `json:"answer,omitempty"`
	CandidatePlan         *d.PreparationPlan    `json:"candidate_plan,omitempty"`
	ValidationError       string                `json:"validation_error,omitempty"`
	ModelTurns            []PreparationEvalTurn `json:"model_turns,omitempty"`
}

// Evaluation records returned candidates, including rejected ones. These files
// contain the authorized task material and must be reviewed before sharing.
type PreparationEvalTurn struct {
	Text            string                `json:"text,omitempty"`
	Calls           []PreparationEvalCall `json:"calls,omitempty"`
	Usage           analysis.Usage        `json:"usage"`
	Error           string                `json:"error,omitempty"`
	ProgramFeedback []json.RawMessage     `json:"program_feedback,omitempty"`
}
type PreparationEvalCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type preparationEvalRecorder struct {
	inner Model
	turns []PreparationEvalTurn
}

func (m *preparationEvalRecorder) Next(ctx context.Context, messages []Message, definitions []Definition) (Reply, error) {
	reply, err := m.inner.Next(ctx, messages, definitions)
	turn := PreparationEvalTurn{Text: prefixUTF8(reply.Text, 16384), Usage: reply.Usage}
	for i := len(messages) - 1; i >= 0 && messages[i].Role != "assistant"; i-- {
		message := messages[i]
		var feedback struct {
			Error string `json:"error"`
		}
		if message.Role == "tool" && len(message.Content) <= 8192 && json.Unmarshal([]byte(message.Content), &feedback) == nil && feedback.Error != "" {
			turn.ProgramFeedback = append(turn.ProgramFeedback, json.RawMessage(message.Content))
		}
	}
	for _, call := range reply.Calls {
		turn.Calls = append(turn.Calls, PreparationEvalCall{ID: prefixUTF8(call.ID, 128), Name: prefixUTF8(call.Name, 128), Arguments: prefixUTF8(string(call.Args), 32768)})
	}
	if err != nil {
		turn.Error = "MODEL_REQUEST_FAILED"
		var httpErr *analysis.HTTPError
		if errors.As(err, &httpErr) {
			turn.Error = fmt.Sprintf("MODEL_HTTP_%d", httpErr.Status)
		}
		if ctx.Err() != nil {
			turn.Error = "CONTEXT_ENDED"
		}
	}
	m.turns = append(m.turns, turn)
	return reply, err
}

func EvalPreparationCase(ctx context.Context, c PreparationEvalCase, arm string, model Model) PreparationEvalResult {
	return evalPreparationCase(ctx, c, arm, model, nil)
}

// EvalPreparationGraphCase exercises the deployed graph over HTTP. Its tools
// use the labelled case corpus; business writes remain a separate SQL/API test.
func EvalPreparationGraphCase(ctx context.Context, c PreparationEvalCase, arm string, model Model, graph *PreparationGraphClient) PreparationEvalResult {
	return evalPreparationCase(ctx, c, arm, model, graph)
}
func evalPreparationCase(ctx context.Context, c PreparationEvalCase, arm string, model Model, graph *PreparationGraphClient) PreparationEvalResult {
	started := time.Now()
	in := NormalizePreparationInput(c.Input)
	v := p.AgentExecution{ID: d.ID(), State: "RUNNING", Preparation: &d.PreparationState{Input: in, Sources: append([]d.PreparationSource{}, c.Sources...), Missing: c.Missing, PreviousPlan: c.PreviousPlan, ProviderState: "READY", Usage: json.RawMessage(`{"limit":128000}`)}}
	result := PreparationEvalResult{CaseID: c.ID, Arm: arm, SourcesUsed: []string{}, BusinessState: "NOT_SAVED; SQL confirmation/isolation verified separately"}
	if c.Control == "stale" || c.Control == "unauthorized" {
		result.State = map[string]string{"stale": "STALE", "unauthorized": "DENIED"}[c.Control]
		result.ProgramExpectationMet = result.State == c.Expected
		return result
	}
	tools := &preparationEvalTools{c: c, queries: map[string]bool{}}
	recorder := &preparationEvalRecorder{inner: capModel(model, 2048)}
	model = recorder
	sources := v.Preparation.Sources
	if arm == "single-prompt" {
		sources = append(sources, c.Corpus...)
	}
	if arm == "fixed-plus-synthesis" {
		// Same literal query builder and BM25 ranking as the fixed preparation path.
		jd := ""
		title := ""
		reviews := []d.Review{}
		for _, s := range c.Sources {
			if s.Kind == "JD" {
				jd = s.Text
				title = s.Title
			}
		}
		for _, s := range c.Sources {
			if s.Kind == "HISTORY" {
				reviews = append(reviews, d.Review{ID: s.RecordID, Questions: []string{s.Text}})
			}
		}
		query := preparationQuery(title, jd, reviews, nil, "frozen_application")
		selected, _ := tools.Execute(ctx, "synthetic-owner", "search_preparation_knowledge", json.RawMessage(d.JSON(map[string]string{"query": query.Query})))
		if extra, ok := selected.([]d.PreparationSource); ok {
			sources = append(sources, extra...)
		}
	}
	v.Preparation.Sources = sources
	messages := preparationMessages(in, sources, c.Missing, c.PreviousPlan)
	v.Preparation.Messages = json.RawMessage(d.JSON(messages))
	var err error
	if arm == "dynamic" {
		if graph != nil {
			v.Preparation.Engine = "langgraph"
			v.Preparation.GraphVersion = PreparationGraphVersion
			err = graph.Run(ctx, "synthetic-owner", &v, model, tools, func() error { return nil })
		} else {
			err = RunPreparationLoop(ctx, "synthetic-owner", &v, model, tools, func() error { return nil })
		}
	} else {
		// Baselines get the same goal, constraints and allowed material scope. They
		// synthesize once; the fixed arm receives its one preselected search result.
		defs := preparationDefinitionsForState(v.Preparation)[2:]
		budget := TokenBudget{Limit: 128000}
		estimate := requestEstimate(messages, defs)
		if estimate > 96000 || !budget.admit(estimate, 2048) {
			v.State = "PAUSED"
			result.Error = "INPUT_BUDGET_LIMIT"
		} else {
			v.Preparation.ModelCalls = 1
			reply, e := capModel(model, 2048).Next(ctx, messages, defs)
			err = e
			budget.charge(reply.Usage, estimate, 2048)
			if err != nil {
				v.State = "PAUSED"
				v.Preparation.ProviderState = "UNKNOWN"
			} else {
				args := json.RawMessage(reply.Text)
				name := "finish_preparation_plan"
				if len(reply.Calls) == 1 {
					args = reply.Calls[0].Args
					name = reply.Calls[0].Name
				}
				if len(reply.Calls) > 1 || (name != "ask_preparation_question" && name != "finish_preparation_plan") {
					v.State = "FAILED"
					result.Error = "INVALID_TERMINAL_CALL"
					result.InvalidCalls++
				} else if name == "ask_preparation_question" {
					var q struct {
						Question string `json:"question"`
					}
					if d.Strict(args, &q) == nil && strings.TrimSpace(q.Question) != "" && len(q.Question) <= 1000 && !resume.HasSensitive(q.Question) {
						v.Preparation.Question = q.Question
						v.State = "PAUSED"
					} else {
						v.State = "FAILED"
						result.Error = "INVALID_QUESTION"
						result.InvalidCalls++
					}
				} else {
					var plan d.PreparationPlan
					validation := d.StrictLimit(args, &plan, 32768)
					if validation == nil {
						result.CandidatePlan = &plan
						validation = ValidatePreparationPlan(v.Preparation, plan)
					}
					if validation == nil {
						v.Preparation.Plan = &plan
						v.State = "COMPLETED"
					} else {
						v.State = "FAILED"
						result.Error = "INVALID_PLAN"
						result.ValidationError = validation.Error()
						result.InvalidCalls++
					}
				}
			}
		}
		v.Preparation.Usage = json.RawMessage(d.JSON(budget))
	}
	result.State = v.State
	result.Answer = v.Answer
	result.ModelTurns = recorder.turns
	result.Plan = v.Preparation.Plan
	result.Question = v.Preparation.Question
	result.Calls = v.Preparation.ModelCalls
	result.ToolCalls = tools.calls
	result.RepeatedQueries = tools.repeated
	result.Trace = v.Preparation.Trace
	for _, trace := range result.Trace {
		if trace.Status == "INVALID_TOOL_CALL" {
			result.InvalidCalls++
		}
		if trace.Status == "REJECTED" {
			if trace.Detail == "TOOL_UNAVAILABLE" || trace.Detail == "TEMPORARY_UNAVAILABLE" || trace.Detail == "TOOL_TIMEOUT" {
				result.FailedToolCalls++
			} else {
				result.InvalidCalls++
			}
		}
	}
	result.FailedToolCalls = max(result.FailedToolCalls, tools.failed)
	json.Unmarshal(v.Preparation.Usage, &result.Usage)
	if result.Plan != nil {
		result.PlanValid = ValidatePreparationPlan(v.Preparation, *result.Plan) == nil
		used := map[string]bool{}
		for _, item := range result.Plan.Items {
			for _, ref := range item.References {
				if !used[ref.SourceID] {
					result.SourcesUsed = append(result.SourcesUsed, ref.SourceID)
					used[ref.SourceID] = true
				}
			}
		}
	}
	initial := map[string]bool{}
	for _, source := range c.Sources {
		initial[source.ID] = true
	}
	if arm != "single-prompt" {
		retrieved := map[string]bool{}
		for _, source := range v.Preparation.Sources {
			if !initial[source.ID] {
				retrieved[source.ID] = true
			}
		}
		result.RetrievalNewSources = len(retrieved)
		for _, id := range result.SourcesUsed {
			if retrieved[id] {
				result.RetrievalCitedSources++
			}
		}
	}
	result.ProgramExpectationMet = result.State == c.Expected && (c.Expected != "COMPLETED" || result.PlanValid)
	if result.ProgramExpectationMet && c.Expected == "PAUSED" {
		if c.Control == "unknown-model" {
			// This explicitly labelled controlled case tests unknown-result
			// protection; it is not a successful clarification.
			result.ProgramExpectationMet = v.Preparation.ProviderState == "UNKNOWN"
		} else {
			// Budget exhaustion or a network error also pauses execution.
			// Missing-information cases pass only with an actual question.
			result.ProgramExpectationMet = strings.TrimSpace(result.Question) != "" && err == nil
		}
	}
	if err != nil {
		result.Error = "MODEL_OR_TOOL_INTERRUPTED"
	}
	result.LatencyMS = float64(time.Since(started).Microseconds()) / 1000
	return result
}

type preparationEvalTools struct {
	c               PreparationEvalCase
	calls, repeated int
	failed          int
	queries         map[string]bool
}

func (t *preparationEvalTools) Definitions() []Definition { return preparationDefinitions() }
func (t *preparationEvalTools) Execute(ctx context.Context, user, name string, args json.RawMessage) (any, error) {
	t.calls++
	var input struct {
		Query string `json:"query"`
	}
	if d.Strict(args, &input) != nil || strings.TrimSpace(input.Query) == "" || len(input.Query) > 1000 {
		return nil, p.ErrValidation
	}
	key := name + input.Query
	if t.queries[key] {
		t.repeated++
	}
	t.queries[key] = true
	if t.c.Control == "retrieval-error" {
		t.failed++
		return nil, errors.New("controlled retrieval unavailable")
	}
	kind := "KNOWLEDGE"
	if name == "search_related_reviews" {
		if !t.c.Input.OtherReviews {
			return nil, p.ErrValidation
		}
		kind = "HISTORY"
	}
	if t.c.Control == "empty-retrieval" {
		return []d.PreparationSource{}, nil
	}
	chunks := []rag.Chunk{}
	byID := map[string]d.PreparationSource{}
	for _, s := range t.c.Corpus {
		if s.Kind == kind {
			chunks = append(chunks, rag.Chunk{ID: s.ID, Text: s.Text, Title: s.Title})
			byID[s.ID] = s
		}
	}
	out := []d.PreparationSource{}
	for _, hit := range rag.KeywordRank(input.Query, chunks) {
		if len(out) == 5 {
			break
		}
		out = append(out, byID[hit.ID])
	}
	return out, nil
}

// ControlledPreparationModel exercises program paths without network/model
// inference. It has no knowledge of expected labels or task IDs.
type ControlledPreparationModel struct {
	Control string
	Calls   int
}

func (m *ControlledPreparationModel) Next(ctx context.Context, messages []Message, defs []Definition) (Reply, error) {
	m.Calls++
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	if m.Control == "unknown-model" {
		return Reply{}, errors.New("controlled unknown response")
	}
	var data struct {
		Goal     d.PreparationInput    `json:"goal"`
		Sources  []d.PreparationSource `json:"sources"`
		Previous *d.PreparationPlan    `json:"previous_plan"`
	}
	if len(messages) < 2 || json.Unmarshal([]byte(messages[1].Content), &data) != nil {
		return Reply{}, p.ErrValidation
	}
	emit := func(name string, args any) (Reply, error) {
		return Reply{Calls: []Call{{ID: fmt.Sprintf("controlled-%d", m.Calls), Name: name, Args: json.RawMessage(d.JSON(args))}}}, nil
	}
	has := func(name string) bool {
		for _, def := range defs {
			if def.Name == name {
				return true
			}
		}
		return false
	}
	if data.Goal.Minutes == 0 || data.Goal.Days == 0 {
		return emit("ask_preparation_question", map[string]string{"question": "请补充总准备时间和完成天数。"})
	}
	var jd, history *d.PreparationSource
	for i := range data.Sources {
		if data.Sources[i].Kind == "JD" {
			jd = &data.Sources[i]
		}
		if data.Sources[i].Kind == "HISTORY" {
			history = &data.Sources[i]
		}
	}
	if jd == nil {
		return emit("ask_preparation_question", map[string]string{"question": "缺少本次申请的历史 JD，请核对原始留档材料；不会使用当前 JD 替代。"})
	}
	if has("search_preparation_knowledge") && len(messages) == 2 && data.Previous == nil {
		query := "Go 并发 MySQL 索引"
		if m.Control == "rewrite" {
			query = "unmatched-initial-query"
		}
		return emit("search_preparation_knowledge", map[string]string{"query": query})
	}
	if m.Control == "rewrite" && has("search_preparation_knowledge") && m.Calls == 2 {
		return emit("search_preparation_knowledge", map[string]string{"query": "MySQL 索引"})
	}
	plan := d.PreparationPlan{Summary: "在时间约束内准备实际实现和口头演练", Items: []d.PreparationItem{}}
	if data.Previous != nil {
		excluded := map[string]bool{}
		for _, id := range append(append([]string{}, data.Goal.Mastered...), data.Goal.Completed...) {
			excluded[id] = true
		}
		for _, item := range data.Previous.Items {
			if !excluded[item.ID] {
				plan.Items = append(plan.Items, item)
			}
		}
	} else {
		quote := jd.Text
		if len(quote) > 200 {
			quote = prefixUTF8(quote, 200)
		}
		plan.Items = append(plan.Items, d.PreparationItem{ID: "go", Title: "Go 并发与取消", Basis: "JD", Day: 1, Action: "解释竞争条件并实现取消传播", Deliverable: "可运行例子与取舍说明", References: []d.PreparationReference{{SourceID: jd.ID, Quote: quote}}}, d.PreparationItem{ID: "mysql", Title: "MySQL 索引与事务", Basis: "JD", Day: 1, Action: "用执行计划核查一条查询", Deliverable: "SQL 与执行计划解释", References: []d.PreparationReference{{SourceID: jd.ID, Quote: quote}}})
		if history != nil {
			plan.Items = append(plan.Items, d.PreparationItem{ID: "review", Title: "复盘失分项", Basis: "HISTORY", Day: 1, Action: "针对原问题练习并核对答案", Deliverable: "复盘改进清单", References: []d.PreparationReference{{SourceID: history.ID, Quote: prefixUTF8(history.Text, 200)}}})
		}
	}
	for i := range plan.Items {
		plan.Items[i].Minutes = data.Goal.Minutes / max(1, len(plan.Items))
	}
	return emit("finish_preparation_plan", plan)
}
