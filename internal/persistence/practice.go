package persistence

import (
	"context"
	"time"
)

// PracticeRun is the retained historical wire shape. The main product no
// longer executes code or changes these records while reading them.
type PracticeRun struct {
	Code        string    `json:"code,omitempty"`
	Tests       string    `json:"tests,omitempty"`
	ID          string    `json:"id"`
	RequestKey  string    `json:"request_key"`
	Fingerprint string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	State       string    `json:"state"`
	Output      string    `json:"output"`
	ExitCode    int       `json:"exit_code"`
	Truncated   bool      `json:"truncated"`
	DurationMS  int64     `json:"duration_ms"`
	Policy      string    `json:"policy"`
}

func (s *Store) PracticeRuns(ctx context.Context, user string) ([]PracticeRun, error) {
	return Many[PracticeRun](ctx, s.DB, "SELECT body FROM practice_runs WHERE user_id=? ORDER BY created_at DESC,id LIMIT 20", user)
}
