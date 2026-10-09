package matching

import (
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"regexp"
	"strings"
	"time"
)

const ChatVersion = "campustrace-chat-v3"
const ChatPromptRevision = "chat-prompt-2026-10-08"
const ChatIdentity = "manual-chat\nChatGPT 聊天导入"
const ChatSource = "CHATGPT_IMPORT"

// Scores and qualifications supplied by chat are intentionally absent here.
// They are computed locally from checked requirements and candidate citations.
type ChatJob struct {
	Assessment   *HolisticAssessment `json:"assessment,omitempty"`
	ID           string              `json:"job_id"`
	InputKey     string              `json:"input_key"`
	Truncated    bool                `json:"truncated,omitempty"`
	Requirements []Requirement       `json:"requirements,omitempty"`
	Matches      []Match             `json:"matches,omitempty"`
}
type ChatDocument struct {
	Comparisons    []HolisticCompanyReport `json:"comparisons,omitempty"`
	Version        string                  `json:"version"`
	PromptRevision string                  `json:"prompt_revision,omitempty"`
	CandidateHash  string                  `json:"candidate_hash"`
	Jobs           []ChatJob               `json:"jobs"`
}

func ImportChatJob(in ChatJob, text string, job d.Job, profile d.Profile, c Candidate, now time.Time) (Result, error) {
	if in.Assessment != nil {
		if len(in.Requirements) > 0 || len(in.Matches) > 0 || in.Truncated {
			return Result{}, invalid("HOLISTIC_MIXED_FORMAT", 0)
		}
		prepared, err := PrepareHolisticAssessment(*in.Assessment, text, c)
		if err != nil {
			return Result{}, err
		}
		reqs, err := HolisticRequirements(prepared, text)
		if err != nil {
			return Result{}, err
		}
		return Result{QualityVersion: QualityVersion, Holistic: &prepared, JobID: in.ID, InputKey: in.InputKey, RequirementsKey: RequirementKey(text, ChatIdentity), CandidateHash: c.Hash(), Model: ChatIdentity, Source: ChatSource, AnalyzedAt: now, Requirements: reqs, Matches: []Match{}, CandidateFacts: c.Facts, Qualifications: Qualification(job, profile, reqs, now), Breakdown: []SectionScore{}}, nil
	}
	if in.Truncated || len(in.Requirements) > MaxRequirements {
		return Result{}, ErrCapacity
	}
	if len(in.Matches) > MaxRequirements {
		return Result{}, invalid("CHAT_MATCHES_LIMIT", 0)
	}
	if len(in.Requirements) == 0 {
		return Result{}, invalid("CHAT_REQUIREMENTS_EMPTY", 0)
	}
	reqs := append([]Requirement{}, in.Requirements...)
	positions := make([]int, len(reqs))
	for i := range positions {
		positions[i] = i + 1
	}
	issues := []ValidationError{}
	reported := map[ValidationError]bool{}
	add := func(reason, scope string, item, related int) {
		v := ValidationError{Reason: reason, Scope: scope, ItemIndex: item, RelatedItemIndex: related}
		if !reported[v] {
			issues = append(issues, v)
			reported[v] = true
		}
	}
	valid := map[int]bool{}
	seen := map[string]int{}
	ignored := map[string]bool{}
	for i, r := range reqs {
		if strings.TrimSpace(r.ID) == "" || len(r.ID) > 48 {
			add("CHAT_REQUIREMENT_ID_INVALID", "REQUIREMENT", i+1, 0)
			continue
		}
		if previous := seen[r.ID]; previous > 0 {
			add("CHAT_REQUIREMENT_ID_DUPLICATE", "REQUIREMENT", i+1, previous)
			continue
		}
		seen[r.ID] = i + 1
		if ignorableChatHeading(r, text, c, in.Matches) {
			ignored[r.ID] = true
			continue
		}
		r = repairUnrestrictedMajor(r)
		reqs[i] = r
		if err := ValidateRequirement(r, text); err != nil {
			add(chatRequirementFailure(r, text), "REQUIREMENT", i+1, 0)
			continue
		}
		valid[i+1] = true
	}
	if len(ignored) > 0 {
		kept := []Requirement{}
		keptPositions := []int{}
		for i, r := range reqs {
			if !ignored[r.ID] {
				kept = append(kept, r)
				keptPositions = append(keptPositions, positions[i])
			}
		}
		reqs = kept
		positions = keptPositions
		if len(reqs) == 0 {
			return Result{}, invalid("CHAT_REQUIREMENTS_EMPTY", 0)
		}
	}
	checkGroups := func(items []Requirement) {
		groups := map[string]int{}
		for i, r := range items {
			if !valid[positions[i]] || r.GroupID == "" {
				continue
			}
			if previous, ok := groups[r.GroupID]; ok {
				p := items[previous]
				if p.Category != r.Category || p.Aspect != r.Aspect || p.GroupExcerpt != r.GroupExcerpt {
					add("CHAT_GROUP_CONFLICT", "REQUIREMENT", positions[i], positions[previous])
				}
			} else {
				groups[r.GroupID] = i
			}
		}
	}
	checkGroups(reqs)
	// Never copy a single completed judgment into several atomic abilities.
	categorized, restoredCategories := restoreChatCategories(reqs, text)
	normalized := normalizeRequirementSemantics(append([]Requirement{}, categorized...), text)
	for i, original := range reqs {
		// A full-sentence chat label can include another qualification's
		// preference. Do not demote a mandatory degree because the major is
		// preferred, or a mandatory major because the degree is preferred.
		if original.Category == "QUALIFICATION" && normalized[i].Category == "BONUS" && chatMandatoryQualification(original) {
			normalized[i].Category, normalized[i].ClaimType, normalized[i].Value = original.Category, original.ClaimType, original.Value
		}
		if categorized[i].Category == "QUALIFICATION" && categorized[i].ClaimType == "" && normalized[i].Category == "BONUS" {
			// A restored, unstructured academic gate can include a preferred
			// major next to a mandatory degree. Keep it for local/manual checks.
			for _, clause := range qualificationClauses.Split(categorized[i].Text, -1) {
				if degreeMinimum.MatchString(clause) && !preferredCue.MatchString(clause) && !strings.Contains(clause, "不限") {
					normalized[i] = categorized[i]
					break
				}
			}
		}
	}
	normalized = removeCoveredSoftClauses(normalized)
	canonical := map[string]int{}
	for i, r := range normalized {
		item := positions[i]
		if !valid[item] {
			continue
		}
		probe := r
		probe.Excerpt = r.Text
		if len(splitTestableRequirements([]Requirement{probe})) != 1 {
			add("CHAT_REQUIREMENT_COMPOSITE", "REQUIREMENT", item, 0)
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
			add("CHAT_REQUIREMENT_CONTENT_DUPLICATE", "REQUIREMENT", item, previous)
		} else {
			canonical[key] = item
		}
	}
	checkGroups(normalized)
	reqs = normalized
	matches := []Match{}
	matchPositions := []int{}
	for i, m := range in.Matches {
		if m.ReviewNote != "" {
			add("CHAT_REVIEW_NOTE", "MATCH", i+1, 0)
		}
		if !ignored[m.RequirementID] {
			matches = append(matches, m)
			matchPositions = append(matchPositions, i+1)
		}
	}
	matches = comparePreferences(c, reqs, matches)
	if len(matches) != len(reqs) {
		issues = append(issues, ValidationError{Reason: "MATCH_COUNT", Expected: len(reqs), Actual: len(matches)})
	}
	byID := map[string]Requirement{}
	for _, r := range reqs {
		byID[r.ID] = r
	}
	matched := map[string]int{}
	for i, m := range matches {
		item := matchPositions[i]
		r, ok := byID[m.RequirementID]
		if !ok {
			add("REQUIREMENT_UNKNOWN", "MATCH", item, 0)
			continue
		}
		if previous := matched[m.RequirementID]; previous > 0 {
			add("REQUIREMENT_DUPLICATE", "MATCH", item, previous)
		} else {
			matched[m.RequirementID] = item
		}
		if err := validateMatches(c, []Requirement{r}, []Match{m}, false); err != nil {
			var v *ValidationError
			if errors.As(err, &v) {
				add(v.Reason, "MATCH", item, 0)
			}
		}
	}
	for _, r := range reqs {
		if matched[r.ID] == 0 {
			add("CHAT_MATCH_MISSING", "REQUIREMENT", seen[r.ID], 0)
		}
	}
	if len(issues) > 0 {
		return Result{}, &ChatValidationErrors{Issues: issues}
	}
	withdrawInvalidAbilityEvidence(c, reqs, matches)
	coveredLanguages := coveredLanguageUmbrellas(reqs)
	if len(coveredLanguages) > 0 {
		keptReqs, keptMatches := []Requirement{}, []Match{}
		for _, r := range reqs {
			if !coveredLanguages[r.ID] {
				keptReqs = append(keptReqs, r)
			}
		}
		for _, m := range matches {
			if !coveredLanguages[m.RequirementID] {
				keptMatches = append(keptMatches, m)
			}
		}
		reqs, matches = keptReqs, keptMatches
	}
	reqs, matches = repairChatContext(reqs, matches, text)
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
	return Result{QualityVersion: QualityVersion, IgnoredHeadings: len(ignored), RestoredCategories: restoredCategories, JobID: job.ID, InputKey: JobInputKey(job, key, c.Hash()), RequirementsKey: key, CandidateHash: c.Hash(), ComparisonScope: scope, ComparisonKey: ComparisonKey(key, ComparisonCandidateHash(c, scope), scope), Model: ChatIdentity, Source: ChatSource, AnalyzedAt: now, Requirements: reqs, Matches: matches, CandidateFacts: c.Facts, Score: score, Coverage: coverage, Breakdown: ScoreBreakdown(reqs, matches), Qualifications: Qualification(job, profile, reqs, now)}, nil
}

// Explain repairable quote failures without returning any source or candidate
// text. All of these still fail the same requirement validation above.
func chatRequirementFailure(r Requirement, source string) string {
	switch {
	case len(r.Text) > 600:
		return "CHAT_REQUIREMENT_TEXT_LENGTH"
	case len(r.Excerpt) > 600:
		return "CHAT_REQUIREMENT_EXCERPT_LENGTH"
	case r.Excerpt == "":
		return "CHAT_REQUIREMENT_EXCERPT_EMPTY"
	case !strings.Contains(source, r.Excerpt):
		return "CHAT_REQUIREMENT_EXCERPT_NOT_EXACT"
	default:
		return "CHAT_REQUIREMENT_INVALID"
	}
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
