package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	"github.com/KanaDoodle/CampusTrace/internal/projectdefense"
)

type limitedModel struct {
	inner     agent.Model
	remaining *int
}

func (m limitedModel) Next(ctx context.Context, msg []agent.Message, defs []agent.Definition) (agent.Reply, error) {
	if *m.remaining <= 0 {
		return agent.Reply{}, fmt.Errorf("explicit call limit reached")
	}
	*m.remaining--
	return m.inner.Next(ctx, msg, defs)
}

func newLiveModel(cfg modelconfig.Config, remaining *int) limitedModel {
	client := analysis.NewChat(cfg.URL, cfg.APIKey, cfg.Model, 1)
	// Set the cap before wrapping: agent.capModel cannot see through the
	// provider-call limiter, so the request itself must carry this limit.
	client.OutputTokenLimit = 2048
	client.HTTP = modelconfig.PublicClient()
	return limitedModel{agent.LiveModel{Client: client}, remaining}
}
func nearestRank(sorted []float64, percentile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := max(0, min(len(sorted)-1, int(math.Ceil(percentile*float64(len(sorted))))-1))
	return sorted[index]
}

func evaluationArms(value string) ([]string, int, error) {
	arms := strings.Split(value, ",")
	seen := map[string]bool{}
	weight := 0
	for _, arm := range arms {
		if seen[arm] {
			return nil, 0, fmt.Errorf("duplicate evaluation arm")
		}
		seen[arm] = true
		switch arm {
		case "single-prompt", "fixed-plus-synthesis":
			weight++
		case "dynamic":
			weight += 6
		default:
			return nil, 0, fmt.Errorf("unknown evaluation arm")
		}
	}
	return arms, weight, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validateDefenseEvaluation(tasks []agent.PreparationEvalCase, arms []string, legacy, live bool, evidenceSHA string) error {
	for _, task := range tasks {
		if task.Input.Defense == nil {
			continue
		}
		if legacy || len(arms) != 1 || arms[0] != "dynamic" {
			return fmt.Errorf("project defense requires the LangGraph dynamic arm")
		}
		if live && evidenceSHA != projectdefense.Version() {
			return fmt.Errorf("live project defense requires explicit authorization of the embedded project evidence SHA-256")
		}
	}
	return nil
}
func run() error {
	dataset := flag.String("dataset", "internal/agent/testdata/preparation-tasks.json", "reviewable task file")
	split := flag.String("split", "development", "development or validation")
	output := flag.String("output", "", "JSON report path")
	only := flag.String("only", "", "optional case ID")
	limit := flag.Int("limit", 0, "optional number of selected tasks")
	armsFlag := flag.String("arms", "single-prompt,fixed-plus-synthesis,dynamic", "comma-separated comparison arms; dynamic-only spot checks require at most 6 calls per task")
	graphURL := flag.String("graph-url", "http://127.0.0.1:18110", "LangGraph service URL for dynamic arm")
	callbackAddr := flag.String("graph-callback-addr", "127.0.0.1:18112", "loopback callback listener; configure Python PREPARATION_GO_URL to this address")
	legacy := flag.Bool("legacy-dynamic", false, "explicit historical Go executor; never automatic fallback")
	live := flag.Bool("live", false, "perform actual model calls only with explicit configuration/material authorization")
	url := flag.String("url", "", "public Chat Completions URL")
	name := flag.String("model", "", "model name")
	keyEnv := flag.String("key-env", "", "explicit key environment variable")
	authorize := flag.String("authorize-material-sha256", "", "exact authorized dataset SHA-256")
	authorizeEvidence := flag.String("authorize-project-evidence-sha256", "", "exact authorized embedded project evidence SHA-256 for live defense tasks")
	maxCalls := flag.Int("max-calls", 24, "hard total provider-call cap")
	inputPrice := flag.Float64("input-usd-per-million", 0, "optional user supplied input token price")
	outputPrice := flag.Float64("output-usd-per-million", 0, "optional output token price")
	flag.Parse()
	arms, perTaskCalls, err := evaluationArms(*armsFlag)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*dataset)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("dataset exceeds 1 MiB")
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	var tasks []agent.PreparationEvalCase
	if err := json.Unmarshal(raw, &tasks); err != nil {
		return err
	}
	if len(tasks) < 20 || len(tasks) > 30 {
		return fmt.Errorf("expected 20-30 reviewable tasks")
	}
	selected := []agent.PreparationEvalCase{}
	for _, task := range tasks {
		if task.Split == *split && (*only == "" || task.ID == *only) {
			selected = append(selected, task)
		}
	}
	if *limit > 0 && len(selected) > *limit {
		selected = selected[:*limit]
	}
	if len(selected) == 0 {
		return fmt.Errorf("no selected tasks")
	}
	for _, task := range selected {
		if task.MaterialKind != "synthetic-control" && task.MaterialKind != "human-approved-desensitized" {
			return fmt.Errorf("all selected materials require an authorized classification before any request")
		}
	}
	perTaskLimit := 6
	if err := validateDefenseEvaluation(selected, arms, *legacy, *live, *authorizeEvidence); err != nil {
		return err
	}
	if !*legacy && len(selected) == 1 && len(arms) == 1 && arms[0] == "dynamic" && *maxCalls > 0 && *maxCalls < 6 {
		perTaskLimit = *maxCalls
		perTaskCalls = *maxCalls
	}
	if *live && *maxCalls < len(selected)*perTaskCalls {
		return fmt.Errorf("call cap must cover all selected arms (at most %d per task); narrow with -only/-limit/-arms", perTaskCalls)
	}
	var real agent.Model
	remaining := *maxCalls
	if *live {
		if *authorize != digest || *maxCalls < 1 || *maxCalls > 240 || *keyEnv == "" {
			return fmt.Errorf("live mode requires exact material SHA-256, explicit key-env and 1..240 max-calls")
		}
		cfg := modelconfig.Config{URL: *url, Model: *name, APIKey: os.Getenv(*keyEnv)}
		if err := cfg.Validate(); err != nil {
			return err
		}
		real = newLiveModel(cfg, &remaining)
	}
	var graph *agent.PreparationGraphClient
	if !*legacy && strings.Contains(","+*armsFlag+",", ",dynamic,") {
		graph = agent.NewPreparationGraphClient(*graphURL, os.Getenv("PREPARATION_SERVICE_TOKEN"))
		if graph.Token == "" {
			graph.Token = "local-preparation-service-change-this-2027"
		}
		host, _, err := net.SplitHostPort(*callbackAddr)
		if err != nil || (host != "127.0.0.1" && host != "::1") {
			return fmt.Errorf("evaluation callback must bind loopback")
		}
		listener, err := net.Listen("tcp", *callbackAddr)
		if err != nil {
			return err
		}
		mux := http.NewServeMux()
		mux.HandleFunc("POST /internal/preparation/step", graph.InternalStep)
		server := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second}
		defer server.Close()
		go server.Serve(listener)
		if err := graph.Health(context.Background()); err != nil {
			return fmt.Errorf("LangGraph unavailable; no legacy fallback")
		}
	}
	results := []agent.PreparationEvalResult{}
	started := time.Now()
	for _, task := range selected {
		if task.MaterialKind != "synthetic-control" && task.MaterialKind != "human-approved-desensitized" {
			return fmt.Errorf("unreviewed material kind")
		}
		for _, arm := range arms {
			var model agent.Model = &agent.ControlledPreparationModel{Control: task.Control}
			if *live {
				model = real
			}
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			var result agent.PreparationEvalResult
			if *legacy {
				result = agent.EvalPreparationCase(ctx, task, arm, model)
			} else {
				result = agent.EvalPreparationGraphCaseWithLimit(ctx, task, arm, model, graph, perTaskLimit)
			}
			cancel()
			if *live && result.Usage.UnreportedCalls == 0 && *inputPrice > 0 && *outputPrice > 0 {
				cost := (float64(result.Usage.PromptTokens)**inputPrice + float64(result.Usage.CompletionTokens)**outputPrice) / 1e6
				result.EstimatedCostUSD = &cost
			}
			results = append(results, result)
		}
	}
	if len(results) == 0 {
		return fmt.Errorf("split has no cases")
	}
	summary := map[string]any{}
	for _, arm := range arms {
		n, pass, calls, tools, invalid, failures := 0, 0, 0, 0, 0, 0
		latencies := []float64{}
		for _, r := range results {
			if r.Arm != arm {
				continue
			}
			n++
			if r.ProgramExpectationMet {
				pass++
			}
			calls += r.Calls
			tools += r.ToolCalls
			invalid += r.InvalidCalls
			failures += r.FailedToolCalls
			latencies = append(latencies, r.LatencyMS)
		}
		sort.Float64s(latencies)
		summary[arm] = map[string]any{"tasks": n, "program_expectations_met": pass, "model_calls": calls, "tool_calls": tools, "invalid_calls": invalid, "failed_tool_calls": failures, "p50_ms": nearestRank(latencies, .5), "p95_ms": nearestRank(latencies, .95), "quantile_method": "nearest-rank"}
	}
	mode := "controlled-response-program-regression"
	if *live {
		mode = "real-model-on-explicitly-authorized-materials"
	}
	report := map[string]any{"mode": mode, "split": *split, "dataset_sha256": digest, "model": *name, "model_config": map[string]any{"url": *url, "requested_model": *name, "request_policy": "existing ChatClient provider-specific defaults", "max_output_tokens": 2048}, "max_calls": *maxCalls, "external_model_attempts": 0, "elapsed_ms": time.Since(started).Milliseconds(), "results": results, "summary": summary, "semantic_quality": "REQUIRES_HUMAN_REVIEW; citation substring checks do not prove factual entailment", "business_writes": "No writes in CLI arms; SQL confirmation, ownership, stale input and idempotency are separately tested", "baseline_adaptation": "Fixed path uses existing literal preparationQuery plus BM25, then one structured synthesis. All arms receive the same goal and constraints; the single prompt gets the entire allowed small corpus.", "cost": "Reported usage and optional user-supplied prices; absent usage or prices leaves cost null. Controlled responses are not actual token/cost measurements."}
	if *live {
		report["external_model_attempts"] = *maxCalls - remaining
	}
	report["agent_version"] = agent.PreparationAgentVersion
	report["dynamic_executor"] = "historical-go"
	if graph != nil {
		report["dynamic_executor"] = "langgraph"
		report["graph_version"] = agent.PreparationGraphVersion
		report["framework_versions"] = graph.Framework()
	}
	report["prompt_sha256"] = d.Hash(agent.PreparationPolicy)
	if !*legacy {
		report["prompt_sha256"] = d.Hash(agent.PreparationPolicy + agent.PreparationQualityPolicy)
		report["quality_version"] = agent.PreparationQualityVersion
	}
	report["tools_version"] = "preparation-tools-v2-bm25-quality"
	for _, task := range selected {
		if task.Input.Defense != nil {
			delete(report, "prompt_sha256")
			report["prompt_hash_scope"] = "actual system message SHA-256 recorded on each result"
			report["defense_prompt_version"] = agent.DefensePromptVersion
			report["project_evidence_sha256"] = projectdefense.Version()
			report["baseline_adaptation"] = "Project defense runs the product LangGraph dynamic path; legacy and single/fixed comparisons are explicitly rejected."
			break
		}
	}
	report["per_task_model_limit"] = perTaskLimit
	report["semantic_rubric"] = map[string]any{"status": "UNREVIEWED", "scale": "0=violated/unsupported, 1=partial, 2=meets; null=not reviewed; N/A=not applicable", "dimensions": []string{"target_requirement_coverage", "citation_entailment", "feasible_workload", "feedback_compliance", "no_fabricated_experience", "retrieval_added_value"}, "reviewer": "No independent human labels; use scripts/review-preparation-evaluation.py to record reviewer and evidence"}
	for _, task := range selected {
		if task.Input.Defense == nil {
			continue
		}
		report["baseline_adaptation"] = "Project defense uses only the product LangGraph dynamic path. Paired memory_mode=confirmed/off cases keep model, current question, sources and budgets equal; controlled replies demonstrate data flow, not quality improvement."
		report["memory_policy_version"] = "project-defense-memory-v1; same-topic/current-evidence/user-confirmed; max 3 records and 24576 JSON bytes"
		report["semantic_rubric"].(map[string]any)["dimensions"] = []string{"answer_feedback_correctness", "citation_entailment", "feasible_workload", "feedback_compliance", "no_fabricated_experience", "memory_added_value", "correction_compliance"}
		break
	}
	report["arms"] = arms
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if *output != "" {
		return os.WriteFile(*output, append(encoded, '\n'), 0600)
	}
	fmt.Println(string(encoded))
	return nil
}
