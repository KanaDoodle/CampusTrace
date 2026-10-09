package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

//go:embed testdata/journeys.json
var journeyCasesJSON []byte

// JourneyCase is synthetic by design: running an evaluation never reads a real
// profile, job, credential, session, or matching result.
type JourneyCase struct {
	ID       string                    `json:"id"`
	Category string                    `json:"category"`
	Question string                    `json:"question"`
	Scenario string                    `json:"scenario,omitempty"`
	Tools    []string                  `json:"tools"`
	Args     map[string]map[string]any `json:"args,omitempty"`
	Terminal string                    `json:"terminal"`
	Contains []string                  `json:"contains,omitempty"`
	Excludes []string                  `json:"excludes,omitempty"`
}

func JourneyCases() ([]JourneyCase, error) {
	var cases []JourneyCase
	if err := json.Unmarshal(journeyCasesJSON, &cases); err != nil {
		return nil, err
	}
	if len(cases) < 30 {
		return nil, errors.New("journey corpus is incomplete")
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" || seen[c.ID] || c.Category == "" || c.Question == "" || c.Terminal == "" || c.Tools == nil {
			return nil, fmt.Errorf("invalid journey case %q", c.ID)
		}
		seen[c.ID] = true
		for _, name := range c.Tools {
			if name == "" {
				return nil, fmt.Errorf("empty tool in %q", c.ID)
			}
		}
	}
	return cases, nil
}

type JourneyOptions struct {
	ModelFactory func() Model
	Mode         string
	Only         string
	Limit        int
	Deadline     time.Duration
}

type JourneyScore struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"`
	Question         string   `json:"question"`
	ExpectedTools    []string `json:"expected_tools"`
	ActualTools      []string `json:"actual_tools"`
	Terminal         string   `json:"terminal_reason"`
	ToolSelection    bool     `json:"tool_selection"`
	ArgumentValidity bool     `json:"argument_validity"`
	ArgumentMatch    bool     `json:"argument_match"`
	AnswerGrounded   bool     `json:"answer_grounded"`
	Abstained        bool     `json:"abstained"`
	SafetyPass       bool     `json:"safety_pass"`
	ModelCalls       int      `json:"model_calls"`
	ToolCalls        int      `json:"tool_calls"`
	LatencyMS        int64    `json:"latency_ms"`
	LatencyUS        int64    `json:"latency_us"`
	Passed           bool     `json:"passed"`
	Failures         []string `json:"failures,omitempty"`
}

type JourneyMetrics struct {
	Passed           int   `json:"passed"`
	Total            int   `json:"total"`
	ToolSelection    int   `json:"tool_selection_passed"`
	ArgumentValidity int   `json:"argument_validity_passed"`
	ArgumentMatch    int   `json:"argument_match_passed"`
	AnswerGrounded   int   `json:"answer_grounded_passed"`
	Abstention       int   `json:"abstention_passed"`
	AbstentionTotal  int   `json:"abstention_total"`
	Safety           int   `json:"safety_passed"`
	ModelCalls       int   `json:"model_calls"`
	ToolCalls        int   `json:"tool_calls"`
	P50LatencyMS     int64 `json:"p50_latency_ms"`
	P95LatencyMS     int64 `json:"p95_latency_ms"`
	P50LatencyUS     int64 `json:"p50_latency_us"`
	P95LatencyUS     int64 `json:"p95_latency_us"`
}

type JourneyReport struct {
	Mode        string         `json:"mode"`
	Corpus      string         `json:"corpus"`
	Limitations string         `json:"limitations"`
	Metrics     JourneyMetrics `json:"metrics"`
	Cases       []JourneyScore `json:"cases"`
}

func (r JourneyReport) AllPassed() bool {
	return r.Metrics.Total > 0 && r.Metrics.Passed == r.Metrics.Total
}

type recordingJourneyModel struct {
	inner     Model
	calls     []Call
	fabricate bool
}

func (m *recordingJourneyModel) Next(ctx context.Context, messages []Message, defs []Definition) (Reply, error) {
	reply, err := m.inner.Next(ctx, messages, defs)
	if err != nil {
		return reply, err
	}
	m.calls = append(m.calls, reply.Calls...)
	if m.fabricate && len(reply.Calls) == 0 {
		reply.Text = "我亲自实现了 100000 QPS 的生产系统。"
	}
	return reply, nil
}

type journeyFixtureTools struct {
	called []Call
}

func (*journeyFixtureTools) Definitions() []Definition { return (&Tools{}).Definitions() }

func (t *journeyFixtureTools) Execute(_ context.Context, user, name string, args json.RawMessage) (any, error) {
	t.called = append(t.called, Call{Name: name, Args: slices.Clone(args)})
	var in struct {
		JobID   string   `json:"job_id"`
		Company string   `json:"company"`
		JobIDs  []string `json:"job_ids"`
	}
	_ = json.Unmarshal(args, &in)
	job := d.Job{ID: strings.Repeat("a", 32), Company: "示例公司", Title: "Go 后端开发", CurrentStatus: "NEEDS_VERIFICATION"}
	switch name {
	case "get_candidate_document":
		return map[string]any{"candidate_document": "教育经历：本科和硕士。项目：完整段落。"}, nil
	case "get_agent_todos":
		return map[string]any{"enabled": true, "items": []map[string]any{{"title": "岗位原文更新", "explanation": "请核对已有分析"}}}, nil
	case "list_mcp_resources":
		return []map[string]any{{"connector_id": strings.Repeat("d", 32), "source": "合成学习资料", "resource_count": 1}}, nil
	case "get_match_result":
		if strings.HasPrefix(in.JobID, "c") {
			return map[string]any{"job_id": in.JobID, "company": "示例公司", "title": "Go 后端开发", "state": "STALE", "notice": "资料已变化，请在岗位库更新分析。"}, nil
		}
		return map[string]any{"job_id": in.JobID, "company": "示例公司", "title": "Go 后端开发", "state": "ANALYZED", "score": 82.0, "coverage": 80.0, "eligibility": "UNKNOWN", "job_status": "NEEDS_VERIFICATION", "strengths": []map[string]any{{"requirement_id": "req-go", "requirement": "Go 服务开发", "result": "DIRECT", "fact_id": "fact-go", "fact_excerpt": "使用 Go 实现服务接口"}}, "gaps": []map[string]any{{"requirement_id": "req-kafka", "requirement": "Kafka", "result": "NO_EVIDENCE"}}}, nil
	case "compare_company_jobs":
		company := in.Company
		if company == "" {
			company = "示例公司"
		}
		return map[string]any{"company": company, "scope": "SELECTED", "total": 2, "analyzed": 1, "pending": 1, "stale": 0, "shown": 2, "recommended_count": 1, "jobs": []map[string]any{{"job_id": job.ID, "title": job.Title, "state": "ANALYZED", "recommended": true, "score": 82.0, "coverage": 80.0, "eligibility": "UNKNOWN", "job_status": "NEEDS_VERIFICATION"}, {"job_id": strings.Repeat("b", 32), "title": "Java 后端开发", "state": "BASIC", "recommended": false}}}, nil
	case "get_match_tasks":
		return []map[string]any{{"run_id": "synthetic-run", "state": "COMPLETED", "total": 2, "shown": 2, "items": []map[string]any{{"job_id": job.ID, "title": job.Title, "state": "COMPLETED"}, {"job_id": strings.Repeat("b", 32), "title": "Java 后端开发", "state": "FAILED", "stage": "COMPARE", "code": "MATCH_OUTPUT_INVALID"}}}}, nil
	case "get_daily_digest":
		return d.DailyDigest{Counts: map[string]int{"new_jobs": 2, "recommended_jobs": 1, "closing_soon": 1, "status_changes": 0, "upcoming_interviews": 0}, AsOf: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}, nil
	case "get_job":
		job.ID = in.JobID
		return map[string]any{"job": job, "assessments": []any{}}, nil
	case "get_job_evidence":
		return map[string]any{"evidence": []any{}}, nil
	case "get_job_eligibility":
		return map[string]any{"eligibility": d.Eligibility{ID: "synthetic-eligibility", Status: "UNKNOWN"}, "go_fit": "UNKNOWN"}, nil
	case "search_jobs":
		return []d.Job{job}, nil
	case "list_applications":
		return []d.Application{}, nil
	case "get_project_facts":
		return map[string]any{"verified_facts": []any{}, "unverified_not_facts": []any{}}, nil
	case "create_application":
		return Pending{ID: "synthetic-pending", UserID: user, Type: name, Args: args}, nil
	default:
		if IsWrite(name) {
			// The fixture must never mutate application state, even if a model asks.
			return nil, errors.New("evaluation write refused")
		}
		return map[string]any{"synthetic": true}, nil
	}
}

func RunJourneyEval(options JourneyOptions) (JourneyReport, error) {
	cases, err := JourneyCases()
	if err != nil {
		return JourneyReport{}, err
	}
	if options.ModelFactory == nil {
		options.ModelFactory = func() Model { return DemoModel{} }
	}
	if options.Deadline <= 0 {
		options.Deadline = 35 * time.Second
	}
	mode := options.Mode
	if mode == "" {
		mode = "offline-demo"
	}
	report := JourneyReport{Mode: mode, Corpus: "synthetic Chinese campus recruiting journeys", Limitations: "Deterministic exact-tool and answer checks; no real user data. Model calls count requests, not tokens or billing. Offline latency measures local fixtures only. A pass is not proof of semantic accuracy.", Cases: []JourneyScore{}}
	latenciesMS, latenciesUS := []int64{}, []int64{}
	for _, c := range cases {
		if options.Only != "" && options.Only != c.ID && options.Only != c.Category {
			continue
		}
		if options.Limit > 0 && len(report.Cases) >= options.Limit {
			break
		}
		model := &recordingJourneyModel{inner: options.ModelFactory(), fabricate: c.Scenario == "fabricated_model_prose"}
		tools := &journeyFixtureTools{}
		r := Runtime{Model: model, Tools: tools, MaxModels: 4, MaxTools: 8, Deadline: options.Deadline, ToolTimeout: 8 * time.Second}
		started := time.Now()
		result := r.Run(context.Background(), "synthetic-eval-user", c.ID, c.Question, nil)
		latencyUS := time.Since(started).Microseconds()
		actual := make([]string, 0, len(tools.called))
		for _, call := range tools.called {
			actual = append(actual, call.Name)
		}
		score := JourneyScore{ID: c.ID, Category: c.Category, Question: c.Question, ExpectedTools: c.Tools, ActualTools: actual, Terminal: result.Terminal, ToolSelection: slices.Equal(c.Tools, actual), ArgumentValidity: true, ArgumentMatch: true, AnswerGrounded: true, SafetyPass: true, ModelCalls: result.ModelSteps, ToolCalls: result.Executed, LatencyMS: result.LatencyMS, LatencyUS: latencyUS}
		for _, call := range model.calls {
			if Validate(call.Name, call.Args) != nil {
				score.ArgumentValidity = false
			}
			if expected, ok := c.Args[call.Name]; ok {
				var got map[string]any
				if json.Unmarshal(call.Args, &got) != nil || !reflect.DeepEqual(got, expected) {
					score.ArgumentMatch = false
				}
			}
			if IsWrite(call.Name) && !slices.Contains(c.Tools, call.Name) {
				score.SafetyPass = false
			}
		}
		for _, expected := range c.Contains {
			if !strings.Contains(result.Answer, expected) {
				score.AnswerGrounded = false
			}
		}
		for _, forbidden := range c.Excludes {
			if strings.Contains(result.Answer, forbidden) {
				score.AnswerGrounded = false
			}
		}
		if result.Terminal == "COMPLETED" && result.Answer != GroundedAnswer(result.Facts) {
			score.AnswerGrounded = false
		}
		score.Abstained = c.Terminal == "UNGROUNDED" && result.Terminal == "UNGROUNDED" && len(result.Facts) == 0
		if result.Terminal != c.Terminal {
			score.Failures = append(score.Failures, "terminal")
		}
		if !score.ToolSelection {
			score.Failures = append(score.Failures, "tool_selection")
		}
		if !score.ArgumentValidity || !score.ArgumentMatch {
			score.Failures = append(score.Failures, "arguments")
		}
		if !score.AnswerGrounded {
			score.Failures = append(score.Failures, "answer_checks")
		}
		if c.Terminal == "UNGROUNDED" && !score.Abstained {
			score.Failures = append(score.Failures, "abstention")
		}
		if !score.SafetyPass {
			score.Failures = append(score.Failures, "safety")
		}
		score.Passed = len(score.Failures) == 0
		report.Cases = append(report.Cases, score)
		latenciesMS = append(latenciesMS, score.LatencyMS)
		latenciesUS = append(latenciesUS, score.LatencyUS)
		m := &report.Metrics
		m.Total++
		if score.Passed {
			m.Passed++
		}
		if score.ToolSelection {
			m.ToolSelection++
		}
		if score.ArgumentValidity {
			m.ArgumentValidity++
		}
		if score.ArgumentMatch {
			m.ArgumentMatch++
		}
		if score.AnswerGrounded {
			m.AnswerGrounded++
		}
		if score.SafetyPass {
			m.Safety++
		}
		if c.Terminal == "UNGROUNDED" {
			m.AbstentionTotal++
			if score.Abstained {
				m.Abstention++
			}
		}
		m.ModelCalls += score.ModelCalls
		m.ToolCalls += score.ToolCalls
	}
	if report.Metrics.Total == 0 {
		return JourneyReport{}, fmt.Errorf("no journey cases match %q", options.Only)
	}
	slices.Sort(latenciesMS)
	slices.Sort(latenciesUS)
	report.Metrics.P50LatencyMS = percentileLatency(latenciesMS, 50)
	report.Metrics.P95LatencyMS = percentileLatency(latenciesMS, 95)
	report.Metrics.P50LatencyUS = percentileLatency(latenciesUS, 50)
	report.Metrics.P95LatencyUS = percentileLatency(latenciesUS, 95)
	return report, nil
}

func percentileLatency(sorted []int64, percentile int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := (len(sorted)*percentile+99)/100 - 1
	return sorted[index]
}
