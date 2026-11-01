package domain

import (
	"github.com/KanaDoodle/CampusTrace/internal/defensestrategy"
	"time"
)

// Simulated project answers are not interview history or verified profile facts.
type DefenseInput struct {
	Topic      string `json:"topic"`
	Answer     string `json:"answer"`
	MemoryMode string `json:"memory_mode,omitempty"`
}

type DefenseFinding struct {
	Point       string                 `json:"point"`
	Verdict     string                 `json:"verdict"`
	AnswerQuote string                 `json:"answer_quote"`
	Explanation string                 `json:"explanation"`
	References  []PreparationReference `json:"references"`
}

type DefenseReview struct {
	Summary      string           `json:"summary"`
	Findings     []DefenseFinding `json:"findings"`
	NextAction   string           `json:"next_action"`
	NextQuestion string           `json:"next_question"`
	Plan         PreparationPlan  `json:"plan"`
}

type DefenseState struct {
	TeachingStrategy *defensestrategy.Profile `json:"teaching_strategy,omitempty"`
	PromptVersion    string                   `json:"prompt_version"`
	Version          string                   `json:"version"`
	Topic            string                   `json:"topic"`
	Title            string                   `json:"title"`
	Question         string                   `json:"question"`
	Answer           string                   `json:"answer"`
	Round            int                      `json:"round"`
	EvidenceRead     int                      `json:"evidence_read"`
	Points           []DefensePoint           `json:"points"`
	Evidence         []PreparationSource      `json:"evidence"`
	Review           *DefenseReview           `json:"review,omitempty"`
	Previous         *DefenseReview           `json:"previous_review,omitempty"`
	Memories         []DefenseMemory          `json:"memories,omitempty"`
	MemoryExcluded   int                      `json:"memory_excluded,omitempty"`
}

// User-confirmed practice evidence, never an inferred skill or real interview.
type DefenseEpisode struct {
	StrategyID      string              `json:"strategy_id,omitempty"`
	ExecutionID     string              `json:"execution_id"`
	ApplicationID   string              `json:"application_id"`
	Revision        int                 `json:"revision"`
	Topic           string              `json:"topic"`
	Title           string              `json:"title"`
	EvidenceVersion string              `json:"evidence_version"`
	PromptVersion   string              `json:"prompt_version"`
	RecordedAt      time.Time           `json:"recorded_at"`
	Question        string              `json:"question"`
	Answer          string              `json:"answer"`
	Review          DefenseReview       `json:"review"`
	Sources         []PreparationSource `json:"sources"`
}

type DefenseMemory struct {
	ID        string          `json:"id"`
	Version   uint64          `json:"version"`
	Note      string          `json:"note"`
	Disputed  bool            `json:"disputed"`
	ExpiresAt time.Time       `json:"expires_at"`
	Episode   *DefenseEpisode `json:"episode"`
}

type DefensePoint struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Sources []string `json:"source_ids"`
}
