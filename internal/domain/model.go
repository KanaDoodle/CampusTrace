package domain

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const AnalysisVersion = "claims-v3-semantics"
const ParserVersion = "generic-v2-lines"
const RuleVersion = "rules-v3"
const SourceParserVersion = "public-http-v2-complete"

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Normalize(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
func Hash(s string) string      { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Strict(raw []byte, dst any) error {
	if len(raw) == 0 || len(raw) > 65536 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("invalid JSON size or null")
	}
	// Reject null at any depth: optional values are omitted, never ambiguous nulls.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	var check func(any) bool
	check = func(v any) bool {
		if v == nil {
			return false
		}
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				if !check(e) {
					return false
				}
			}
		case map[string]any:
			for _, e := range x {
				if !check(e) {
					return false
				}
			}
		}
		return true
	}
	if !check(v) {
		return errors.New("null values are not accepted")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

type Company struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Job struct {
	Revision      uint64    `json:"revision"`
	OwnerID       string    `json:"owner_id,omitempty"`
	Visibility    string    `json:"visibility"`
	ID            string    `json:"id"`
	CompanyID     string    `json:"company_id"`
	Company       string    `json:"company"`
	Title         string    `json:"title"`
	JobType       string    `json:"job_type"`
	Locations     []string  `json:"locations"`
	Fingerprint   string    `json:"fingerprint"`
	CurrentStatus string    `json:"current_status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type Source struct {
	OwnerID    string `json:"owner_id,omitempty"`
	Visibility string `json:"visibility"`
	Timezone   string `json:"timezone"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Trust      string `json:"trust"`
}
type Posting struct {
	ID          string    `json:"id"`
	JobID       string    `json:"job_id"`
	SourceID    string    `json:"source_id"`
	ExternalID  string    `json:"external_id"`
	URL         string    `json:"url"`
	FirstSeen   time.Time `json:"first_seen_at"`
	LastSeen    time.Time `json:"last_seen_at"`
	MergeReason string    `json:"merge_reason"`
}
type Observation struct {
	ActiveAnalysisGeneration  uint64    `json:"active_analysis_generation,omitempty"`
	CurrentAnalysisGeneration uint64    `json:"current_analysis_generation,omitempty"`
	DesiredProcessingVersion  string    `json:"desired_processing_version,omitempty"`
	AnalysisVersion           string    `json:"analysis_version,omitempty"`
	Timezone                  string    `json:"timezone"`
	ID                        string    `json:"id"`
	PostingID                 string    `json:"source_posting_id"`
	JobID                     string    `json:"job_id"`
	ObservedAt                time.Time `json:"observed_at"`
	FetchStatus               string    `json:"fetch_status"`
	HTTPStatus                int       `json:"http_status"`
	Hash                      string    `json:"normalized_content_hash"`
	Text                      string    `json:"text"`
	ApplySignal               string    `json:"apply_signal"`
	DeadlineSignal            string    `json:"deadline_signal"`
	ParserVersion             string    `json:"parser_version"`
	ExtractionStatus          string    `json:"extraction_status"`
	ErrorCategory             string    `json:"error_category"`
	Trust                     string    `json:"trust"`
}
type Claim struct {
	Type       string  `json:"type"`
	Value      string  `json:"value"`
	Excerpt    string  `json:"excerpt"`
	Method     string  `json:"extraction_method"`
	Confidence float64 `json:"confidence"`
}
type Claims struct {
	Claims []Claim `json:"claims"`
}

var EvidenceTypes = map[string]bool{"GRADUATION_REQUIREMENT": true, "EDUCATION_REQUIREMENT": true, "JOB_TYPE": true, "LOCATION": true, "EXPERIENCE_REQUIREMENT": true, "TECH_STACK": true, "LANGUAGE_REQUIREMENT": true, "MAJOR_REQUIREMENT": true, "APPLY_ACTION": true, "DEADLINE": true, "OPEN_SIGNAL": true, "CLOSED_SIGNAL": true}

func (c Claim) Validate(text string) error {
	if !EvidenceTypes[c.Type] || c.Value == "" || len(c.Value) > 1000 || c.Excerpt == "" || len(c.Excerpt) > 2000 || c.Confidence < 0 || c.Confidence > 1 {
		return errors.New("invalid candidate claim")
	}
	if c.Method != "RULE" && c.Method != "LLM" && c.Method != "MANUAL" {
		return errors.New("invalid extraction method")
	}
	if !strings.Contains(Normalize(text), Normalize(c.Excerpt)) {
		return errors.New("excerpt is not bound to observation")
	}

	switch c.Type {
	case "GRADUATION_REQUIREMENT":
		if !regexp.MustCompile(`^20[0-9]{2}(-20[0-9]{2})?$`).MatchString(c.Value) {
			return errors.New("schema: graduation must be year or year range")
		}
		v := strings.Split(c.Value, "-")
		if len(v) == 2 && v[0] > v[1] {
			return errors.New("schema: reversed graduation range")
		}
	case "EDUCATION_REQUIREMENT":
		if c.Value != "ASSOCIATE" && c.Value != "BACHELOR" && c.Value != "MASTER" && c.Value != "PHD" {
			return errors.New("schema: invalid degree")
		}
	case "JOB_TYPE":
		if c.Value != "FULL_TIME" && c.Value != "INTERNSHIP" {
			return errors.New("schema: invalid job type")
		}
	case "EXPERIENCE_REQUIREMENT":
		n, err := strconv.Atoi(c.Value)
		if err != nil || n < 0 || n > 600 {
			return errors.New("schema: invalid experience months")
		}
	case "DEADLINE":
		if _, err := DeadlineInstant(c.Value, "Asia/Shanghai"); err != nil {
			return errors.New("schema: invalid deadline")
		}
	case "APPLY_ACTION":
		if c.Value != "PRESENT" && c.Value != "ABSENT" && c.Value != "UNKNOWN" {
			return errors.New("schema: invalid apply signal")
		}
	case "OPEN_SIGNAL":
		if c.Value != "OPEN" {
			return errors.New("schema: invalid open signal")
		}
	case "CLOSED_SIGNAL":
		if c.Value != "CLOSED" {
			return errors.New("schema: invalid closed signal")
		}
	}
	return c.ValidateMeaning()
}

type Evidence struct {
	AnalysisVersion string `json:"analysis_version,omitempty"`
	ID              string `json:"id"`
	ObservationID   string `json:"observation_id"`
	JobID           string `json:"job_id"`
	Claim
	CreatedAt time.Time `json:"created_at"`
}
type Assessment struct {
	ID          string    `json:"id"`
	JobID       string    `json:"job_id"`
	Status      string    `json:"status"`
	RuleVersion string    `json:"rule_version"`
	AssessedAt  time.Time `json:"assessed_at"`
	EvidenceIDs []string  `json:"evidence_ids"`
	Reason      string    `json:"reason"`
}
type Change struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	From      string    `json:"from_observation"`
	To        string    `json:"to_observation"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}
type Profile struct {
	Revision         uint64   `json:"revision"`
	UserID           string   `json:"user_id"`
	GraduationYear   int      `json:"graduation_year"`
	GraduationFrom   int      `json:"graduation_from"`
	GraduationTo     int      `json:"graduation_to"`
	Degree           string   `json:"degree"`
	Majors           []string `json:"majors,omitempty"`
	PreferredTypes   []string `json:"preferred_job_types,omitempty"`
	PreferredCities  []string `json:"preferred_cities,omitempty"`
	AcceptableCities []string `json:"acceptable_cities,omitempty"`
	TargetRoles      []string `json:"target_roles,omitempty"`
	Skills           []string `json:"technical_skills,omitempty"`
	Languages        []string `json:"target_languages,omitempty"`
	ExperienceMonths int      `json:"experience_months"`
}
type RuleResult struct {
	Rule        string   `json:"rule"`
	Result      string   `json:"result"`
	Requirement string   `json:"requirement"`
	Candidate   string   `json:"candidate_value"`
	EvidenceIDs []string `json:"evidence_ids"`
	Explanation string   `json:"explanation"`
}
type Eligibility struct {
	InputIdentity string       `json:"input_identity"`
	ID            string       `json:"id"`
	JobID         string       `json:"job_id"`
	UserID        string       `json:"user_id"`
	Status        string       `json:"status"`
	RuleVersion   string       `json:"rule_version"`
	Results       []RuleResult `json:"results"`
	AssessedAt    time.Time    `json:"assessed_at"`
}
type Ranking struct {
	InputIdentity    string             `json:"input_identity"`
	BreakdownSources map[string]string  `json:"breakdown_sources"`
	JobID            string             `json:"job_id"`
	UserID           string             `json:"user_id"`
	Score            float64            `json:"score"`
	Breakdown        map[string]float64 `json:"breakdown"`
	RuleVersion      string             `json:"rule_version"`
	AssessedAt       time.Time          `json:"assessed_at"`
}
type Application struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	JobID         string     `json:"job_id"`
	State         string     `json:"current_state"`
	Version       int        `json:"version"`
	AppliedAt     *time.Time `json:"applied_at,omitempty"`
	ResumeVersion string     `json:"resume_version"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
type ApplicationEvent struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"application_id"`
	From          string    `json:"from_state"`
	To            string    `json:"to_state"`
	OccurredAt    time.Time `json:"occurred_at"`
	Note          string    `json:"note"`
	Actor         string    `json:"actor"`
}
type Interview struct {
	ID            string     `json:"id"`
	ApplicationID string     `json:"application_id"`
	Round         int        `json:"round"`
	ScheduledAt   time.Time  `json:"scheduled_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Result        string     `json:"result"`
	Notes         string     `json:"notes"`
}
type WeakCandidate struct {
	Topic    string `json:"topic"`
	Weight   int    `json:"weight"`
	Evidence string `json:"evidence"`
}
type Review struct {
	ID          string          `json:"id"`
	InterviewID string          `json:"interview_id"`
	Questions   []string        `json:"actual_questions"`
	Evaluation  string          `json:"self_evaluation"`
	Missed      []string        `json:"missed_points"`
	FollowUp    string          `json:"follow_up_notes"`
	Topics      []WeakCandidate `json:"weak_topics"`
}

func (r Review) Validate() error {
	if r.InterviewID == "" || r.Evaluation == "" || r.Missed == nil || r.Topics == nil || len(r.Questions) == 0 || len(r.Questions) > 30 || len(r.Topics) > 30 {
		return errors.New("invalid interview review")
	}
	for _, q := range r.Questions {
		if strings.TrimSpace(q) == "" || len(q) > 2000 {
			return errors.New("invalid question")
		}
	}
	if len(r.Evaluation) > 8000 || len(r.FollowUp) > 8000 || len(r.Missed) > 30 {
		return errors.New("review too large")
	}
	for _, t := range r.Topics {
		if len(t.Topic) == 0 || len(t.Topic) > 120 || t.Weight < 1 || t.Weight > 5 || t.Evidence == "" {
			return errors.New("invalid weak topic")
		}
		if !strings.Contains(Normalize(strings.Join(r.Missed, " ")+" "+r.Evaluation+" "+strings.Join(r.Questions, " ")), Normalize(t.Evidence)) {
			return errors.New("weak topic evidence not in review")
		}
	}
	return nil
}

type WeakTopic struct {
	Topic     string    `json:"topic"`
	Weight    int       `json:"weight"`
	Evidence  []string  `json:"evidence_sources"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Count     int       `json:"occurrence_count"`
}
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ProjectFact struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Kind      string    `json:"kind"`
	Claim     string    `json:"claim"`
	Verified  bool      `json:"verified"`
	Reference string    `json:"reference"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (f ProjectFact) Validate() error {
	if f.ProjectID == "" || f.Claim == "" || len(f.Claim) > 4000 {
		return errors.New("invalid fact")
	}
	if f.Kind != "IMPLEMENTED" && f.Kind != "LIMITATION" && f.Kind != "PLANNED" {
		return errors.New("invalid fact kind")
	}
	return nil
}

var transitions = map[string][]string{"PLANNED": {"APPLIED", "WITHDRAWN"}, "APPLIED": {"OA", "INTERVIEW", "HR", "OFFER", "REJECTED", "WITHDRAWN"}, "OA": {"INTERVIEW", "REJECTED", "WITHDRAWN"}, "INTERVIEW": {"INTERVIEW", "HR", "OFFER", "REJECTED", "WITHDRAWN"}, "HR": {"OFFER", "REJECTED", "WITHDRAWN"}}

func Transition(from, to string) error {
	for _, s := range transitions[from] {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("invalid transition %s -> %s", from, to)
}

// EffectiveAnalysisVersion includes the repair's semantic generation even when
// an old deployment keeps its previous ANALYSIS_VERSION environment setting.
// Custom extractor/model/prompt versions are hashed to stay inside SQL limits.
func EffectiveAnalysisVersion(configured string) string {
	if configured == "" || configured == "claims-v1" || configured == AnalysisVersion {
		return AnalysisVersion
	}
	return AnalysisVersion + "-" + Hash(configured)[:16]
}

// ProcessingVersion is shared by SQL receipts, queued tasks and Redis cache.
// Versions are identities, never ordered strings. Generation order is allocated in SQL.
func ProcessingVersion(configured, parser, sourceParser string) string {
	return "processing-v3-" + Hash(JSON([]string{EffectiveAnalysisVersion(configured), parser, SourceParserVersion, sourceParser}))[:48]
}
