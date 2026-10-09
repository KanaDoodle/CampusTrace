package matcheval

import (
	"context"
	"encoding/json"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

type usageModel struct {
	client *analysis.ChatClient
	usage  analysis.Usage
}

func (m *usageModel) Complete(ctx context.Context, messages any, tools any) (json.RawMessage, error) {
	return m.CompleteJSON(ctx, messages, tools)
}
func (m *usageModel) CompleteJSON(ctx context.Context, messages any, tools any) (json.RawMessage, error) {
	raw, u, err := m.client.CompleteJSONWithUsage(ctx, messages, tools)
	m.usage = u
	return raw, err
}

// One explicit request per trial, through the same full-context prompt and
// output validator as the application. There is no automatic paid retry.
func RunLive(ctx context.Context, c Case, attempt int, client *analysis.ChatClient) Trial {
	t := Trial{CaseID: c.ID, Attempt: attempt, Model: client.Model}
	start := time.Now()
	m := &usageModel{client: client}
	r, err := matching.CompareHolistically(ctx, m, c.Candidate, c.Jobs, client.Model)
	elapsed := time.Since(start).Milliseconds()
	t.LatencyMS = &elapsed
	t.Usage = m.usage
	if err != nil {
		// Never copy provider bodies, URLs, keys or candidate text into errors.
		t.ErrorCode = "MODEL_OR_VALIDATION_FAILED"
		if ctx.Err() != nil {
			t.ErrorCode = "TIMEOUT_OR_CANCELLED"
		}
	} else {
		t.Report = &r
	}
	return t
}
