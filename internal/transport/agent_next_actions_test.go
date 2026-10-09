package transport

import (
	"encoding/json"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestExecutionDestinationsExcludeInvalidatedOrCancelledObservations(t *testing.T) {
	v := p.AgentExecution{State: "COMPLETED", Observations: []p.ExecutionObservation{{Tool: "get_match_result", Data: json.RawMessage(d.JSON(map[string]string{"job_id": strings.Repeat("a", 32), "title": "Go 后端", "state": "ANALYZED"}))}}}
	if !strings.Contains(d.JSON(executionResponse(v)), "next_actions") {
		t.Fatal("successful task has no next steps")
	}
	for _, state := range []string{"STALE", "CANCELLED"} {
		v.State = state
		if strings.Contains(d.JSON(executionResponse(v)), "next_actions") {
			t.Fatal("invalidated observations produced next steps", state)
		}
	}
}
