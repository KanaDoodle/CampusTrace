package domain

import (
	"encoding/json"
	"time"
)

const PreparationAgentSkill = "application-preparation-agent"

type PreparationInput struct {
	Defense       *DefenseInput `json:"defense,omitempty"`
	ApplicationID string        `json:"application_id"`
	Goal          string        `json:"goal"`
	Minutes       int           `json:"minutes"`
	Days          int           `json:"days"`
	ParentID      string        `json:"parent_id,omitempty"`
	Feedback      string        `json:"feedback,omitempty"`
	Mastered      []string      `json:"mastered,omitempty"`
	Completed     []string      `json:"completed,omitempty"`
	OtherReviews  bool          `json:"other_reviews"`
	RetrievalMode string        `json:"retrieval_mode,omitempty"`
}
type PreparationSource struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Text          string `json:"text"`
	RecordID      string `json:"record_id"`
	ApplicationID string `json:"application_id,omitempty"`
	Hash          string `json:"hash"`
}
type PreparationReference struct {
	SourceID string `json:"source_id"`
	Quote    string `json:"quote"`
	Supports string `json:"supports,omitempty"`
}
type PreparationWorkload struct {
	Mode  string `json:"mode"`
	Units int    `json:"units"`
	Scope string `json:"scope"`
}
type PreparationRetrieval struct {
	Step           int      `json:"step"`
	CallID         string   `json:"call_id"`
	Query          string   `json:"query"`
	Gap            string   `json:"gap"`
	ExpectedUse    string   `json:"expected_use"`
	Outcome        string   `json:"outcome"`
	NewSourceIDs   []string `json:"new_source_ids"`
	CitedSourceIDs []string `json:"cited_source_ids"`
}
type PreparationItem struct {
	Workload    *PreparationWorkload   `json:"workload,omitempty"`
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Minutes     int                    `json:"minutes"`
	Day         int                    `json:"day"`
	Basis       string                 `json:"basis"`
	Action      string                 `json:"action"`
	Deliverable string                 `json:"deliverable"`
	References  []PreparationReference `json:"references"`
}
type PreparationPlan struct {
	Summary string            `json:"summary"`
	Items   []PreparationItem `json:"items"`
}
type PreparationTrace struct {
	Kind      string `json:"kind"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status"`
	CallID    string `json:"call_id,omitempty"`
	Detail    string `json:"detail,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}
type PreparationState struct {
	Defense        *DefenseState          `json:"defense,omitempty"`
	ModelCallLimit int                    `json:"model_call_limit,omitempty"`
	QualityVersion string                 `json:"quality_version,omitempty"`
	Retrievals     []PreparationRetrieval `json:"retrievals,omitempty"`
	Engine         string                 `json:"engine,omitempty"`
	GraphSequence  int                    `json:"graph_sequence,omitempty"`
	GraphVersion   string                 `json:"graph_version,omitempty"`
	Input          PreparationInput       `json:"input"`
	ModelIdentity  string                 `json:"model_identity"`
	ModelLabel     string                 `json:"model_label"`
	EvidenceKind   string                 `json:"evidence_kind"`
	ReviewHash     string                 `json:"review_hash"`
	Revision       int                    `json:"revision"`
	ProviderState  string                 `json:"provider_state"`
	AttemptKeys    []string               `json:"attempt_keys"`
	Messages       json.RawMessage        `json:"messages"`
	Sources        []PreparationSource    `json:"sources"`
	Missing        []string               `json:"missing"`
	ModelCalls     int                    `json:"model_calls"`
	ToolCalls      int                    `json:"tool_calls"`
	SearchCalls    int                    `json:"search_calls"`
	Usage          json.RawMessage        `json:"usage"`
	Trace          []PreparationTrace     `json:"trace"`
	Question       string                 `json:"question,omitempty"`
	Plan           *PreparationPlan       `json:"result,omitempty"`
	PreviousPlan   *PreparationPlan       `json:"previous_plan,omitempty"`
	SavedAt        *time.Time             `json:"saved_at,omitempty"`
}
