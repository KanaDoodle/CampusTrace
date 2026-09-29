package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	"os"
)

func main() {
	mode := flag.String("mode", "contract", "contract or journey")
	only := flag.String("only", "", "journey case ID or category")
	limit := flag.Int("limit", 0, "maximum number of journey cases (0 means all)")
	url := flag.String("url", "", "optional public HTTPS Chat Completions endpoint for synthetic journey evaluation")
	modelName := flag.String("model", "", "model name for the optional endpoint")
	keyEnv := flag.String("key-env", "", "name of an environment variable containing its API key")
	strict := flag.Bool("strict", true, "exit nonzero when any journey case fails")
	flag.Parse()
	if *mode == "journey" {
		if *limit < 0 {
			fail("limit cannot be negative")
		}
		options := agent.JourneyOptions{Only: *only, Limit: *limit}
		if *url != "" || *modelName != "" || *keyEnv != "" {
			if *url == "" || *modelName == "" || *keyEnv == "" {
				fail("url, model, and key-env must be supplied together")
			}
			cfg := modelconfig.Config{URL: *url, Model: *modelName, APIKey: os.Getenv(*keyEnv)}
			if err := cfg.Validate(); err != nil {
				fail("invalid public model configuration or missing key environment variable")
			}
			options.Mode = "external-model-synthetic-fixtures"
			options.ModelFactory = func() agent.Model {
				client := analysis.NewChat(cfg.URL, cfg.APIKey, cfg.Model, 1)
				client.HTTP = modelconfig.PublicClient()
				return agent.LiveModel{Client: client}
			}
		}
		report, err := agent.RunJourneyEval(options)
		if err != nil {
			fail(err.Error())
		}
		output(report)
		if *strict && !report.AllPassed() {
			os.Exit(1)
		}
		return
	}
	if *mode != "contract" {
		fail("mode must be contract or journey")
	}
	scores := agent.RunEval()
	output(agent.EvalSummary(scores))
	for _, s := range scores {
		if !s.Success || !s.SafetyPass {
			os.Exit(1)
		}
	}
}

func output(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fail("could not write evaluation report")
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "Agent evaluation:", message)
	os.Exit(2)
}
