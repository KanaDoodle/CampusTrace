// Offline proposal adapter: supplied controlled response only, no API client.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/defensestrategy"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type responseModel []byte

func (m responseModel) Next(_ context.Context, _ []agent.Message, _ []agent.Definition) (agent.Reply, error) {
	return agent.Reply{Calls: []agent.Call{{ID: "controlled-proposal", Name: "propose_defense_teaching_strategy", Args: json.RawMessage(m)}}}, nil
}
func run() error {
	in := flag.String("input", "", "development-only diagnostics")
	response := flag.String("response", "", "supplied controlled response; never performs a network call")
	out := flag.String("output", "", "new private candidate record")
	registry := flag.String("check-registry", "", "validate a registry for product use, no proposal")
	flag.Parse()
	if *registry != "" {
		p, e := defensestrategy.FromRegistry(os.DirFS(*registry))
		if e != nil {
			return e
		}
		fmt.Println(p.ID())
		return nil
	}
	if *in == "" || *response == "" || *out == "" {
		return fmt.Errorf("input, response and output required")
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	var input agent.StrategyDiagnostics
	if d.StrictLimit(raw, &input, 32768) != nil {
		return fmt.Errorf("invalid diagnostics")
	}
	raw, err = os.ReadFile(*response)
	if err != nil {
		return err
	}
	if len(raw) > 4096 {
		return fmt.Errorf("response too large")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	candidate, err := agent.ProposeDefenseStrategy(ctx, responseModel(raw), input)
	if err != nil {
		return err
	}
	record := map[string]any{"candidate": candidate, "strategy_sha256": candidate.Profile.ID(), "diagnostics_sha256": d.Hash(d.JSON(input)), "evidence": "supplied-controlled-model-response", "proposer_turns": 1, "external_model_calls": 0}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(record)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
