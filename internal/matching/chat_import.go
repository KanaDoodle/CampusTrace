package matching

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
	"time"
)

const ChatVersion = "campustrace-chat-v3"
const ChatIdentity = "manual-chat\nChatGPT 聊天导入"
const ChatSource = "CHATGPT_IMPORT"

// Scores and qualifications supplied by chat are intentionally absent here.
// They are computed locally from checked requirements and candidate citations.
type ChatJob struct {
	ID           string        `json:"job_id"`
	InputKey     string        `json:"input_key"`
	Truncated    bool          `json:"truncated,omitempty"`
	Requirements []Requirement `json:"requirements"`
	Matches      []Match       `json:"matches"`
}
type ChatDocument struct {
	Version       string    `json:"version"`
	CandidateHash string    `json:"candidate_hash"`
	Jobs          []ChatJob `json:"jobs"`
}

func ImportChatJob(in ChatJob, text string, job d.Job, profile d.Profile, c Candidate, now time.Time) (Result, error) {
	if in.Truncated || len(in.Requirements) > MaxRequirements {
		return Result{}, ErrCapacity
	}
	if len(in.Requirements) == 0 {
		return Result{}, invalid("CHAT_REQUIREMENTS_EMPTY", 0)
	}
	reqs := append([]Requirement{}, in.Requirements...)
	seen := map[string]bool{}
	for i, r := range reqs {
		if strings.TrimSpace(r.ID) == "" || len(r.ID) > 48 || seen[r.ID] {
			return Result{}, invalid("REQUIREMENT_DUPLICATE", i+1)
		}
		seen[r.ID] = true
		if err := ValidateRequirement(r, text); err != nil {
			return Result{}, invalid("CHAT_REQUIREMENT_INVALID", i+1)
		}
	}
	if err := validateGroups(reqs); err != nil {
		return Result{}, err
	}
	// Never copy a single completed judgment into several atomic abilities.
	normalized := normalizeRequirementSemantics(append([]Requirement{}, reqs...), text)
	canonical := map[string]bool{}
	for i, r := range normalized {
		probe := r
		probe.Excerpt = r.Text
		if len(splitTestableRequirements([]Requirement{probe})) != 1 {
			return Result{}, invalid("CHAT_REQUIREMENT_COMPOSITE", i+1)
		}
		key := d.Hash(r.Category + "\n" + r.Text + "\n" + r.Excerpt + "\n" + r.Aspect + "\n" + r.GroupID)
		if canonical[key] {
			return Result{}, invalid("REQUIREMENT_DUPLICATE", i+1)
		}
		canonical[key] = true
	}
	if err := validateGroups(normalized); err != nil {
		return Result{}, err
	}
	reqs = normalized
	matches := append([]Match{}, in.Matches...)
	for i, m := range matches {
		if m.ReviewNote != "" {
			return Result{}, invalid("CHAT_REVIEW_NOTE", i+1)
		}
	}
	matches = comparePreferences(c, reqs, matches)
	if err := validateMatches(c, reqs, matches, false); err != nil {
		return Result{}, err
	}
	withdrawInvalidAbilityEvidence(c, reqs, matches)
	reqs = RepairQualifications(reqs, text)
	if len(reqs) > MaxRequirements {
		return Result{}, ErrCapacity
	}
	modelIDs := map[string]bool{}
	for _, r := range ModelRequirements(reqs) {
		modelIDs[r.ID] = true
	}
	modelMatches := []Match{}
	for _, m := range matches {
		if modelIDs[m.RequirementID] {
			modelMatches = append(modelMatches, m)
		}
	}
	matches, err := AssembleMatches(profile, c, reqs, modelMatches)
	if err != nil {
		return Result{}, err
	}
	key := RequirementKey(text, ChatIdentity)
	scope := ComparisonScope(reqs)
	score, coverage := Score(reqs, matches)
	return Result{JobID: job.ID, InputKey: InputKey(key, c.Hash()), RequirementsKey: key, CandidateHash: c.Hash(), ComparisonScope: scope, ComparisonKey: ComparisonKey(key, ComparisonCandidateHash(c, scope), scope), Model: ChatIdentity, Source: ChatSource, AnalyzedAt: now, Requirements: reqs, Matches: matches, CandidateFacts: c.Facts, Score: score, Coverage: coverage, Breakdown: ScoreBreakdown(reqs, matches), Qualifications: Qualification(job, profile, reqs, now)}, nil
}
