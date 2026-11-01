package domain

// Simulated project answers are not interview history or verified profile facts.
type DefenseInput struct {
	Topic  string `json:"topic"`
	Answer string `json:"answer"`
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
	PromptVersion string              `json:"prompt_version"`
	Version       string              `json:"version"`
	Topic         string              `json:"topic"`
	Title         string              `json:"title"`
	Question      string              `json:"question"`
	Answer        string              `json:"answer"`
	Round         int                 `json:"round"`
	EvidenceRead  int                 `json:"evidence_read"`
	Points        []DefensePoint      `json:"points"`
	Evidence      []PreparationSource `json:"evidence"`
	Review        *DefenseReview      `json:"review,omitempty"`
	Previous      *DefenseReview      `json:"previous_review,omitempty"`
}

type DefensePoint struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Sources []string `json:"source_ids"`
}
