package domain

import (
	"errors"
	"strings"
	"time"
)

// Source identity and trust are operator-owned. A watch only selects and filters it.
type WatchInput struct {
	SourceID      string `json:"source_id"`
	CheckInterval int    `json:"check_interval"`
	Keyword       string `json:"keyword"`
	Direction     string `json:"direction,omitempty"`
	Enabled       bool   `json:"enabled"`
	Adaptive      bool   `json:"adaptive,omitempty"`
	Priority      bool   `json:"priority,omitempty"`
}

func (v WatchInput) Validate() error {
	if strings.TrimSpace(v.SourceID) == "" || len(v.SourceID) > 32 || v.CheckInterval < 300 || v.CheckInterval > 604800 || len(v.Keyword) > 100 || (v.Direction != "" && v.Direction != "rd" && v.Direction != "algorithm" && v.Direction != "non_tech") {
		return errors.New("validation: watch interval must be 300..604800 seconds and keyword <=100 bytes")
	}
	return nil
}

type WatchTarget struct {
	OneShot bool   `json:"one_shot,omitempty"`
	ID      string `json:"id"`
	UserID  string `json:"user_id"`
	WatchInput
	NextCheckAt       time.Time  `json:"next_check_at"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	ScheduleVersion   uint64     `json:"schedule_version"`
	LastOutcome       string     `json:"last_outcome"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	EffectiveInterval int        `json:"effective_interval,omitempty"`
	ScheduleReason    string     `json:"schedule_reason,omitempty"`
	StableRounds      int        `json:"stable_rounds,omitempty"`
	FailureRounds     int        `json:"failure_rounds,omitempty"`
	RoundChanged      bool       `json:"round_changed,omitempty"`
	RoundStartedAt    time.Time  `json:"round_started_at,omitempty"`
	DiscoveryHash     string     `json:"discovery_hash,omitempty"`
}
type UserJobPreference struct {
	UserID      string `json:"user_id"`
	JobID       string `json:"job_id"`
	Disposition string `json:"disposition"`
}

func ValidDisposition(v string) bool { return v == "NONE" || v == "SAVED" || v == "IGNORED" }

type Notification struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	Type         string     `json:"type"`
	EntityType   string     `json:"entity_type"`
	EntityID     string     `json:"entity_id"`
	EventVersion string     `json:"event_version"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	CreatedAt    time.Time  `json:"created_at"`
	ReadAt       *time.Time `json:"read_at,omitempty"`
}
type RadarJob struct {
	Job             Job          `json:"job"`
	Eligibility     string       `json:"eligibility"`
	Ranking         Ranking      `json:"ranking"`
	Application     *Application `json:"application,omitempty"`
	Disposition     string       `json:"disposition"`
	Deadline        *time.Time   `json:"deadline,omitempty"`
	DeadlineVersion string       `json:"deadline_version,omitempty"`
}
type ChangeItem struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	Title     string    `json:"title"`
	Type      string    `json:"type"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
type DailyDigest struct {
	Counts             map[string]int `json:"counts"`
	AsOf               time.Time      `json:"as_of"`
	NewJobs            []RadarJob     `json:"new_jobs"`
	RecommendedJobs    []RadarJob     `json:"recommended_jobs"`
	ClosingSoon        []RadarJob     `json:"closing_soon"`
	StatusChanges      []ChangeItem   `json:"status_changes"`
	RecentChanges      []ChangeItem   `json:"recent_changes"`
	UpcomingInterviews []Interview    `json:"upcoming_interviews"`
	Truncated          bool           `json:"truncated"`
}
