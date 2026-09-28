package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
)

const Version = "matching-v2-evidence"
const MaxBatch = 3
const MaxBatchText = 24000
const MaxCandidateText = 12000
const MaxRequirements = 36

var ErrInvalid = errors.New("unverifiable matching output")
var ErrCapacity = errors.New("matching input capacity exceeded")

type Settings struct {
	RoundLimit int  `json:"round_limit"`
	DailyCalls int  `json:"daily_calls"`
	AutoNew    bool `json:"auto_new"`
}

func Defaults() Settings { return Settings{RoundLimit: 30, DailyCalls: 40} }
func (s Settings) Validate() error {
	if s.RoundLimit < 1 || s.RoundLimit > 100 || s.DailyCalls < 1 || s.DailyCalls > 200 {
		return ErrInvalid
	}
	return nil
}

type Fact struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	ProjectName string `json:"project_name,omitempty"`
}
type Candidate struct {
	Revision uint64 `json:"revision"`
	Facts    []Fact `json:"facts"`
}

func CandidateFrom(p d.Profile, facts []d.ProjectFact, maskName string) (Candidate, error) {
	return CandidateWithProjects(p, facts, nil, maskName)
}

func CandidateWithProjects(p d.Profile, facts []d.ProjectFact, projects []d.Project, maskName string) (Candidate, error) {
	c := Candidate{Revision: p.Revision, Facts: []Fact{}}
	clean := func(s string) string {
		if maskName != "" {
			s = strings.ReplaceAll(s, maskName, "[已遮盖姓名]")
		}
		return resume.Redact(s)
	}
	add := func(id, kind, text string) {
		if text != "" {
			c.Facts = append(c.Facts, Fact{ID: id, Kind: kind, Text: clean(text)})
		}
	}
	if p.GraduationYear != 0 {
		add("graduation", "GRADUATION", fmt.Sprint(p.GraduationYear))
	} else if p.GraduationFrom != 0 {
		add("graduation", "GRADUATION", fmt.Sprintf("%d-%d", p.GraduationFrom, p.GraduationTo))
	}
	add("degree", "DEGREE", p.Degree)
	add("experience", "EXPERIENCE", fmt.Sprintf("%d 个月", p.ExperienceMonths))
	for _, group := range []struct {
		kind   string
		values []string
	}{{"MAJOR", p.Majors}, {"SKILL", p.Skills}, {"LANGUAGE", p.Languages}, {"ROLE", p.TargetRoles}, {"CITY_PREFERRED", p.PreferredCities}, {"CITY_ACCEPTABLE", p.AcceptableCities}, {"JOB_TYPE_PREFERENCE", p.PreferredTypes}} {
		for i, value := range group.values {
			add(fmt.Sprintf("%s-%d", strings.ToLower(group.kind), i), group.kind, value)
		}
	}
	projectNames := map[string]string{}
	for _, project := range projects {
		projectNames[project.ID] = clean(project.Name)
	}
	for _, f := range facts {
		if !f.Verified || f.Kind == "PLANNED" {
			continue
		}
		add(f.ID, f.Kind, f.Claim)
		if f.Claim != "" {
			c.Facts[len(c.Facts)-1].ProjectName = projectNames[f.ProjectID]
		}
	}
	if len(c.Facts) > 180 || len(d.JSON(c)) > MaxCandidateText {
		return c, ErrCapacity
	}
	return c, nil
}
func (c Candidate) Hash() string               { return d.Hash(Version + "\n" + d.JSON(c)) }
func ModelIdentity(url, model string) string   { return url + "\n" + model }
func RequirementKey(text, model string) string { return d.Hash(Version + "\n" + model + "\n" + text) }
func InputKey(requirementsKey, candidateHash string) string {
	return d.Hash(requirementsKey + "\n" + candidateHash)
}

type JobText struct {
	ID   string `json:"job_id"`
	Text string `json:"text"`
}
type Requirement struct {
	ID           string  `json:"id"`
	Category     string  `json:"category"`
	Text         string  `json:"text"`
	Excerpt      string  `json:"excerpt"`
	ClaimType    string  `json:"claim_type,omitempty"`
	Value        string  `json:"value,omitempty"`
	Confidence   float64 `json:"confidence"`
	Aspect       string  `json:"aspect,omitempty"`
	GroupID      string  `json:"group_id,omitempty"`
	GroupExcerpt string  `json:"group_excerpt,omitempty"`
}
type Requirements struct {
	Items []Requirement `json:"requirements"`
}
type Citation struct {
	ID      string `json:"id"`
	Excerpt string `json:"excerpt"`
}
type Match struct {
	RequirementID string     `json:"requirement_id"`
	Result        string     `json:"result"`
	Explanation   string     `json:"explanation"`
	Evidence      []Citation `json:"evidence"`
}
type MatchInput struct {
	ID           string        `json:"job_id"`
	Requirements []Requirement `json:"requirements"`
}
type Result struct {
	JobID           string         `json:"job_id"`
	InputKey        string         `json:"input_key"`
	RequirementsKey string         `json:"requirements_key"`
	CandidateHash   string         `json:"candidate_hash"`
	Model           string         `json:"model"`
	AnalyzedAt      time.Time      `json:"analyzed_at"`
	Requirements    []Requirement  `json:"requirements"`
	Matches         []Match        `json:"matches"`
	CandidateFacts  []Fact         `json:"candidate_facts"`
	Score           *float64       `json:"score"`
	Coverage        float64        `json:"coverage"`
	Qualifications  d.Eligibility  `json:"qualifications"`
	Breakdown       []SectionScore `json:"breakdown,omitempty"`
}

type jsonCompleter interface {
	CompleteJSON(context.Context, any, any) (json.RawMessage, error)
}

func complete(ctx context.Context, m resume.Completer, prompt string, input any, out any) error {
	messages := []map[string]string{{"role": "system", "content": prompt}, {"role": "user", "content": d.JSON(input)}}
	var raw json.RawMessage
	var err error
	if structured, ok := m.(jsonCompleter); ok {
		raw, err = structured.CompleteJSON(ctx, messages, nil)
	} else {
		raw, err = m.Complete(ctx, messages, nil)
	}
	if err != nil {
		return err
	}
	var msg struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return invalid("RESPONSE_MESSAGE", 0)
	}
	if len(msg.Content) == 0 || len(msg.Content) > 65536 {
		return invalid("RESPONSE_SIZE", 0)
	}
	if !json.Valid([]byte(msg.Content)) {
		return invalid("RESPONSE_JSON", 0)
	}
	if d.Strict([]byte(msg.Content), out) != nil {
		return invalid("RESPONSE_SCHEMA", 0)
	}
	return nil
}
func Extract(ctx context.Context, m resume.Completer, jobs []JobText) (map[string]Requirements, error) {
	if len(jobs) == 0 || len(jobs) > MaxBatch {
		return nil, ErrCapacity
	}
	total := 0
	for _, j := range jobs {
		total += len(j.Text)
	}
	if total > MaxBatchText {
		return nil, ErrCapacity
	}
	var out struct {
		Jobs []struct {
			ID        string        `json:"job_id"`
			Items     []Requirement `json:"requirements"`
			Truncated bool          `json:"truncated,omitempty"`
		} `json:"jobs"`
	}
	err := complete(ctx, m, extractionPrompt, jobs, &out)
	if err != nil {
		return nil, err
	}
	texts := map[string]string{}
	for _, j := range jobs {
		if _, ok := texts[j.ID]; ok {
			return nil, ErrInvalid
		}
		texts[j.ID] = j.Text
	}
	result := map[string]Requirements{}
	for _, j := range out.Jobs {
		text, ok := texts[j.ID]
		if !ok {
			return nil, ErrInvalid
		}
		if _, ok = result[j.ID]; ok {
			return nil, ErrInvalid
		}
		if j.Truncated {
			return nil, ErrCapacity
		}
		if len(j.Items) > MaxRequirements {
			return nil, ErrInvalid
		}
		if err := validateGroups(j.Items); err != nil {
			return nil, err
		}
		j.Items = normalizeRequirements(j.Items, text)
		if err := validateGroups(j.Items); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for i, r := range j.Items {
			if err := ValidateRequirement(r, text); err != nil {
				return nil, err
			}
			r.ID = d.Hash(r.Category + "\n" + r.Text + "\n" + r.Excerpt + "\n" + r.Aspect + "\n" + r.GroupID)[:24]
			if seen[r.ID] {
				return nil, ErrInvalid
			}
			seen[r.ID] = true
			j.Items[i] = r
		}
		if j.Items == nil {
			j.Items = []Requirement{}
		}
		result[j.ID] = Requirements{j.Items}
	}
	if len(result) != len(texts) {
		return nil, ErrInvalid
	}
	return result, nil
}
func ValidateRequirement(r Requirement, text string) error {
	if r.Category != "QUALIFICATION" && r.Category != "REQUIRED" && r.Category != "BONUS" && r.Category != "RESPONSIBILITY" {
		return ErrInvalid
	}
	if strings.TrimSpace(r.Text) == "" || len(r.Text) > 600 || len(r.Excerpt) == 0 || len(r.Excerpt) > 600 || !strings.Contains(text, r.Excerpt) || math.IsNaN(r.Confidence) || r.Confidence < 0 || r.Confidence > 1 || resume.HasSensitive(r.Text) {
		return ErrInvalid
	}
	if genericExperience.MatchString(strings.TrimSpace(r.Excerpt)) && strings.TrimSpace(r.Text) != strings.TrimSpace(r.Excerpt) {
		return ErrInvalid
	}
	if r.Aspect != "" && r.Aspect != "TECHNICAL" && r.Aspect != "SOFT" {
		return ErrInvalid
	}
	if r.GroupID != "" || r.GroupExcerpt != "" {
		if r.Category == "QUALIFICATION" || len(r.GroupID) == 0 || len(r.GroupID) > 48 || len(r.GroupExcerpt) == 0 || len(r.GroupExcerpt) > 600 || !strings.Contains(text, r.GroupExcerpt) || !alternativeCue.MatchString(r.GroupExcerpt) {
			return ErrInvalid
		}
	}
	if r.ClaimType != "" || r.Value != "" {
		allowed := map[string]bool{"GRADUATION_REQUIREMENT": true, "EDUCATION_REQUIREMENT": true, "EXPERIENCE_REQUIREMENT": true, "MAJOR_REQUIREMENT": true, "JOB_TYPE": true, "LOCATION": true}
		if r.Category != "QUALIFICATION" || !allowed[r.ClaimType] {
			return ErrInvalid
		}
		claim := d.Claim{Type: r.ClaimType, Value: r.Value, Excerpt: r.Excerpt, Method: "LLM", Confidence: r.Confidence}
		if claim.Validate(text) != nil {
			return ErrInvalid
		}
	}
	return nil
}
func Compare(ctx context.Context, m resume.Completer, c Candidate, jobs []MatchInput) (map[string][]Match, error) {
	if len(jobs) == 0 || len(jobs) > MaxBatch || len(d.JSON(jobs))+len(d.JSON(c)) > 54000 {
		return nil, ErrCapacity
	}
	var out struct {
		Jobs []struct {
			ID      string  `json:"job_id"`
			Matches []Match `json:"matches"`
		} `json:"jobs"`
	}
	err := complete(ctx, m, comparisonPrompt, struct {
		Candidate Candidate    `json:"candidate"`
		Jobs      []MatchInput `json:"jobs"`
	}{c, jobs}, &out)
	if err != nil {
		return nil, err
	}
	inputs := map[string][]Requirement{}
	positions := map[string]int{}
	for i, j := range jobs {
		if _, exists := inputs[j.ID]; exists {
			return nil, invalid("INPUT_JOB_DUPLICATE", 0)
		}
		inputs[j.ID] = j.Requirements
		positions[j.ID] = i + 1
	}
	result := map[string][]Match{}
	for _, j := range out.Jobs {
		reqs, ok := inputs[j.ID]
		if !ok {
			return nil, invalid("JOB_UNKNOWN", 0)
		}
		if _, ok = result[j.ID]; ok {
			return nil, &ValidationError{Reason: "JOB_DUPLICATE", JobIndex: positions[j.ID]}
		}
		// Ignore model judgments about location/type preferences. These are computed
		// from the supplied saved values; validate the rewritten result afterward.
		// Missing, unknown or duplicate requirement IDs still fail validation.
		j.Matches = comparePreferences(c, reqs, j.Matches)
		if err := ValidateMatches(c, reqs, j.Matches); err != nil {
			var validation *ValidationError
			if errors.As(err, &validation) {
				validation.JobIndex = positions[j.ID]
			}
			return nil, err
		}
		result[j.ID] = j.Matches
	}
	if len(result) != len(inputs) {
		return nil, &ValidationError{Reason: "JOB_COUNT", Expected: len(inputs), Actual: len(result)}
	}
	return result, nil
}
func ValidateMatches(c Candidate, reqs []Requirement, matches []Match) error {
	if len(matches) != len(reqs) {
		return &ValidationError{Reason: "MATCH_COUNT", Expected: len(reqs), Actual: len(matches)}
	}
	facts := map[string]Fact{}
	for _, f := range c.Facts {
		facts[f.ID] = f
	}
	remaining := map[string]bool{}
	reqByID := map[string]Requirement{}
	for _, r := range reqs {
		remaining[r.ID] = true
		reqByID[r.ID] = r
	}
	for i, m := range matches {
		item := i + 1
		if _, known := reqByID[m.RequirementID]; !known {
			return invalid("REQUIREMENT_UNKNOWN", item)
		}
		if !remaining[m.RequirementID] {
			return invalid("REQUIREMENT_DUPLICATE", item)
		}
		delete(remaining, m.RequirementID)
		if strings.TrimSpace(m.Explanation) == "" {
			return invalid("EXPLANATION_EMPTY", item)
		}
		if len(m.Explanation) > 1000 {
			return invalid("EXPLANATION_LENGTH", item)
		}
		if resume.HasSensitive(m.Explanation) {
			return invalid("EXPLANATION_SENSITIVE", item)
		}
		if len(m.Evidence) > 8 {
			return invalid("EVIDENCE_COUNT", item)
		}
		positive := m.Result == "DIRECT" || m.Result == "PARTIAL" || m.Result == "TRANSFERABLE"
		if !positive && m.Result != "NO_EVIDENCE" && m.Result != "MISMATCH" {
			return invalid("RESULT_UNKNOWN", item)
		}
		if (positive || m.Result == "MISMATCH") && len(m.Evidence) == 0 {
			return invalid("EVIDENCE_REQUIRED", item)
		}
		for _, e := range m.Evidence {
			f, ok := facts[e.ID]
			if !ok {
				return invalid("FACT_UNKNOWN", item)
			}
			if e.Excerpt == "" {
				return invalid("EXCERPT_EMPTY", item)
			}
			if len(e.Excerpt) > 600 {
				return invalid("EXCERPT_LENGTH", item)
			}
			if !strings.Contains(f.Text, e.Excerpt) {
				return invalid("EXCERPT_NOT_EXACT", item)
			}
			if positive && (f.Kind == "LIMITATION" || f.Kind == "ROLE" || (isPreference(f.Kind) && reqByID[m.RequirementID].Category != "QUALIFICATION")) {
				return invalid("FACT_NOT_ABILITY", item)
			}
		}
	}
	return nil
}

// Missing evidence lowers core coverage, rather than asserting lack of ability.
// Work duties, bonuses and soft traits have separate breakdowns.
func Score(reqs []Requirement, matches []Match) (*float64, float64) {
	section := ScoreBreakdown(reqs, matches)[0]
	return section.Score, section.Coverage
}
func Qualification(j d.Job, p d.Profile, reqs []Requirement, now time.Time) d.Eligibility {
	evidence := []d.Evidence{}
	for _, r := range reqs {
		if r.Category == "QUALIFICATION" && r.ClaimType != "" {
			evidence = append(evidence, d.Evidence{ID: r.ID, Claim: d.Claim{Type: r.ClaimType, Value: r.Value, Confidence: r.Confidence}})
		}
	}
	if j.JobType == "FULL_TIME" || j.JobType == "INTERNSHIP" {
		found := false
		for _, e := range evidence {
			if e.Type == "JOB_TYPE" {
				found = true
			}
		}
		if !found {
			evidence = append(evidence, d.Evidence{ID: "job-metadata", Claim: d.Claim{Type: "JOB_TYPE", Value: j.JobType, Confidence: 1}})
		}
	}
	// Major names need semantic interpretation; do not turn differing spellings
	// into a definitive rejection. Location and type remain preferences.
	e := rules.Eligibility(j, p, evidence, now)
	e.Status = "ELIGIBLE"
	for i := range e.Results {
		r := &e.Results[i]
		if r.Rule == "MAJOR_REQUIREMENT" && r.Result == "FAIL" {
			r.Result = "UNKNOWN"
			r.Explanation = "专业表述需要结合原文核验"
		}
		if r.Result == "FAIL" {
			e.Status = "INELIGIBLE"
		} else if r.Result == "UNKNOWN" && e.Status != "INELIGIBLE" {
			e.Status = "UNKNOWN"
		} else if r.Result == "CONDITIONAL" && e.Status == "ELIGIBLE" {
			e.Status = "CONDITIONAL"
		}
	}
	return e
}

func Normalize(s string) string {
	s = d.Normalize(s)
	aliases := map[string]string{"golang": "go", "shanghai": "上海", "hangzhou": "杭州", "beijing": "北京", "shenzhen": "深圳", "guangzhou": "广州", "chengdu": "成都", "wuhan": "武汉", "suzhou": "苏州", "backend": "后端", "server-side": "后端", "服务端": "后端", "服务器端": "后端"}
	for _, pair := range []struct{ from, to string }{{"服务器端", "后端"}, {"服务端", "后端"}, {"server-side", "后端"}, {"backend", "后端"}} {
		s = strings.ReplaceAll(s, pair.from, pair.to)
	}
	if v, ok := aliases[s]; ok {
		s = v
	}
	return d.CanonicalCity(s)
}

// Preliminary remains a compatibility entry point. Snapshot screening prepares
// project evidence once with NewLocalScreener; time only breaks ordering ties.
func Preliminary(j d.Job, p d.Profile, text string, _ time.Time) (float64, string) {
	candidate, err := CandidateFrom(p, nil, "")
	if err != nil {
		return 0, "求职资料超出初筛容量，请精简后重试"
	}
	v := NewLocalScreener(p, candidate).Screen(j, text)
	return v.Score, v.ExcludedReason
}
