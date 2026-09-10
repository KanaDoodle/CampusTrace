package main

import (
	"encoding/json"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"os"
)

func main() {
	scores := agent.RunEval()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(agent.EvalSummary(scores))
	for _, s := range scores {
		if !s.Success || !s.SafetyPass {
			os.Exit(1)
		}
	}
}
