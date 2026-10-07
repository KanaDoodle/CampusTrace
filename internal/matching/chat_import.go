package matching

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"regexp"
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
	seen := map[string]int{}
	for i, r := range reqs {
		if strings.TrimSpace(r.ID) == "" || len(r.ID) > 48 {
			return Result{}, invalid("CHAT_REQUIREMENT_ID_INVALID", i+1)
		}
		if previous := seen[r.ID]; previous > 0 {
			return Result{}, &ValidationError{Reason: "CHAT_REQUIREMENT_ID_DUPLICATE", ItemIndex: i + 1, RelatedItemIndex: previous}
		}
		seen[r.ID] = i + 1
		if err := ValidateRequirement(r, text); err != nil {
			return Result{}, invalid("CHAT_REQUIREMENT_INVALID", i+1)
		}
	}
	if err := validateGroups(reqs); err != nil {
		return Result{}, err
	}
	// Never copy a single completed judgment into several atomic abilities.
	normalized := normalizeRequirementSemantics(append([]Requirement{}, reqs...), text)
	for i, original := range reqs {
		// A full-sentence chat label can include another qualification's
		// preference. Do not demote a mandatory degree because the major is
		// preferred, or a mandatory major because the degree is preferred.
		if original.Category == "QUALIFICATION" && normalized[i].Category == "BONUS" && chatMandatoryQualification(original) {
			normalized[i].Category, normalized[i].ClaimType, normalized[i].Value = original.Category, original.ClaimType, original.Value
		}
	}
	canonical := map[string]int{}
	for i, r := range normalized {
		probe := r
		probe.Excerpt = r.Text
		if len(splitTestableRequirements([]Requirement{probe})) != 1 {
			return Result{}, invalid("CHAT_REQUIREMENT_COMPOSITE", i+1)
		}
		// Degree and major conditions may cite the same full source sentence.
		// Keep the validated original facet even if preference normalization
		// clears its claim type. Different values of the same facet do not make
		// duplicate source conditions independent requirements.
		facet := reqs[i].ClaimType
		category := r.Category
		if facet != "" {
			category = "QUALIFICATION"
		}
		key := d.Hash(d.JSON([]string{category, r.Text, r.Excerpt, r.Aspect, r.GroupID, facet}))
		if previous := canonical[key]; previous > 0 {
			return Result{}, &ValidationError{Reason: "CHAT_REQUIREMENT_CONTENT_DUPLICATE", ItemIndex: i + 1, RelatedItemIndex: previous}
		}
		canonical[key] = i + 1
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

var chatQualificationClauses = regexp.MustCompile(`[，,。；;\n（）()]`)

func chatMandatoryQualification(r Requirement) bool {
	degree := map[string]string{"ASSOCIATE": "专科", "BACHELOR": "本科", "MASTER": "硕士", "PHD": "博士"}[r.Value]
	found := false
	for _, clause := range chatQualificationClauses.Split(r.Excerpt, -1) {
		own := r.ClaimType == "EDUCATION_REQUIREMENT" && degree != "" && strings.Contains(clause, degree) || r.ClaimType == "MAJOR_REQUIREMENT" && strings.Contains(clause, "专业")
		if !own {
			continue
		}
		found = true
		if preferredCue.MatchString(clause) {
			return false
		}
	}
	return found
}
