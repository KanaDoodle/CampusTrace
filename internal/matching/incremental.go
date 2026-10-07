package matching

import (
	"fmt"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
)

// Separate paid comparison identity from the full reviewed-input identity.
// Unknown qualification formats keep all non-preference facts in scope. Never
// infer dependencies from the citations of a positive or NO_EVIDENCE result:
// newly added facts may change either conclusion.
const ComparisonVersion = "comparison-v4-scoped-experience"
const QualityVersion = "quality-v1-scoped-experience"
const ComparisonAbilities = "ABILITIES"
const ComparisonFull = "FULL"

func isLocalRequirement(r Requirement) bool {
	if r.Category != "QUALIFICATION" || r.Value == "" {
		return false
	}
	switch r.ClaimType {
	case "GRADUATION_REQUIREMENT", "EDUCATION_REQUIREMENT", "LOCATION", "JOB_TYPE":
		return true
	}
	return false
}

func ComparisonScope(reqs []Requirement) string {
	for _, r := range reqs {
		if r.Category == "QUALIFICATION" && !isLocalRequirement(r) {
			return ComparisonFull
		}
	}
	return ComparisonAbilities
}

func ModelRequirements(reqs []Requirement) []Requirement {
	out := []Requirement{}
	for _, r := range reqs {
		if !isLocalRequirement(r) {
			out = append(out, r)
		}
	}
	return out
}

func ModelCandidate(c Candidate, scope string) Candidate {
	v := Candidate{Facts: []Fact{}}
	for _, f := range c.Facts {
		if isPreference(f.Kind) || (scope == ComparisonAbilities && (f.Kind == "GRADUATION" || f.Kind == "DEGREE" || f.Kind == "EDUCATION")) {
			continue
		}
		v.Facts = append(v.Facts, f)
	}
	return v
}

func ComparisonCandidateHash(c Candidate, scope string) string {
	return d.Hash(d.JSON(candidateForComparison(ModelCandidate(c, scope))))
}

func ComparisonKey(requirementsKey, candidateHash, scope string) string {
	return d.Hash(ComparisonVersion + "\n" + scope + "\n" + requirementsKey + "\n" + candidateHash)
}

func (r Result) CanReuse(requirementsKey string, hashes map[string]string) bool {
	if r.ComparisonScope != ComparisonAbilities && r.ComparisonScope != ComparisonFull {
		return false
	}
	return hashes[r.ComparisonScope] != "" && r.ComparisonKey != "" && r.RequirementsKey == requirementsKey && r.ComparisonKey == ComparisonKey(requirementsKey, hashes[r.ComparisonScope], r.ComparisonScope)
}

// Assemble local and model judgments in original requirement order. Local
// education/year results use the same aggregate conflict handling as the
// eligibility table, so conflicting gates cannot silently become passes.
func AssembleMatches(p d.Profile, c Candidate, reqs []Requirement, modelMatches []Match) ([]Match, error) {
	if err := ValidateMatches(ModelCandidate(c, ComparisonScope(reqs)), ModelRequirements(reqs), modelMatches); err != nil {
		return nil, err
	}
	byID := map[string]Match{}
	for _, m := range modelMatches {
		byID[m.RequirementID] = m
	}
	qualification := Qualification(d.Job{}, p, reqs, time.Time{})
	byRule := map[string]d.RuleResult{}
	for _, r := range qualification.Results {
		byRule[r.Rule] = r
	}
	byKind := map[string]Fact{}
	for _, f := range c.Facts {
		byKind[f.Kind] = f
	}
	out := make([]Match, 0, len(reqs))
	for _, r := range reqs {
		if r.Category == "QUALIFICATION" && r.ClaimType == "MAJOR_REQUIREMENT" {
			m := byID[r.ID]
			gate := byRule[r.ClaimType]
			m.Result, m.Evidence, m.Explanation = "NO_EVIDENCE", []Citation{}, "专业归属或学历范围仍需核对，以资格表为准。"
			if gate.Result == "PASS" && r.Confidence >= .8 {
				allowedFacts := map[string]bool{}
				scope := d.MajorDegreeScope(r.Excerpt)
				for _, education := range p.Educations {
					if scope != "" && education.Degree != scope {
						continue
					}
					for i := range education.Majors {
						allowedFacts[fmt.Sprintf("education-%s-major-%d", education.ID, i)] = true
					}
				}
				for _, major := range p.MajorsForRequirement(r.Excerpt) {
					if !rules.MajorMatches(r.Value, []string{major}, r.Excerpt) {
						continue
					}
					for _, f := range c.Facts {
						if f.Kind == "MAJOR" && (len(p.Educations) == 0 || allowedFacts[f.ID]) && (f.Text == major || strings.HasSuffix(f.Text, "专业："+major)) {
							m.Result, m.Evidence, m.Explanation = "DIRECT", []Citation{{ID: f.ID, Excerpt: f.Text}}, "已按岗位限定的学历范围与已保存专业核对，满足该专业要求。"
							break
						}
					}
					if m.Result == "DIRECT" {
						break
					}
				}
			}
			out = append(out, m)
			continue
		}
		if !isLocalRequirement(r) {
			out = append(out, byID[r.ID])
			continue
		}
		m := Match{RequirementID: r.ID, Result: "NO_EVIDENCE", Explanation: "本地核对依据不足或条件存在冲突，请确认岗位原文与求职资料。", Evidence: []Citation{}}
		if gate := byRule[r.ClaimType]; gate.Result == "UNKNOWN" && strings.Contains(gate.Explanation, "毕业") {
			m.Explanation = gate.Explanation
		}
		if r.Confidence >= .8 && (r.ClaimType == "LOCATION" || r.ClaimType == "JOB_TYPE") {
			m = comparePreferences(c, []Requirement{r}, []Match{m})[0]
		} else if r.Confidence >= .8 {
			kind := "GRADUATION"
			if r.ClaimType == "EDUCATION_REQUIREMENT" {
				kind = "DEGREE"
			}
			f := byKind[kind]
			gate := byRule[r.ClaimType]
			if f.ID != "" && f.Text != "" && (gate.Result == "PASS" || gate.Result == "FAIL") {
				m.Result, m.Explanation = "DIRECT", "根据已保存资料与岗位明确条件在本地核对，满足该要求。"
				if gate.Result == "FAIL" {
					m.Result, m.Explanation = "MISMATCH", "根据已保存资料与岗位明确条件在本地核对，不满足该要求。"
				}
				m.Evidence = []Citation{{ID: f.ID, Excerpt: f.Text}}
			}
		}
		out = append(out, m)
	}
	return out, ValidateMatches(c, reqs, out)
}

// Read-only rebinding never changes the date of the paid analysis or writes
// history. The full input key still fences in-flight tasks and commits.
func RefreshResult(r Result, j d.Job, p d.Profile, c Candidate, inputKey, candidateHash string, now time.Time, sourceText ...string) (Result, error) {
	if ComparisonScope(r.Requirements) != r.ComparisonScope {
		return r, ErrInvalid
	}
	text := ""
	if len(sourceText) > 0 {
		text = sourceText[0]
	}
	r.Requirements = RepairQualifications(r.Requirements, text)
	for i, req := range r.Requirements {
		if softOnly(req) {
			r.Requirements[i].Aspect = "SOFT"
		}
	}
	modelIDs := map[string]bool{}
	for _, req := range ModelRequirements(r.Requirements) {
		modelIDs[req.ID] = true
	}
	modelMatches := []Match{}
	for _, m := range r.Matches {
		if modelIDs[m.RequirementID] {
			modelMatches = append(modelMatches, m)
		}
	}
	matches, err := AssembleMatches(p, c, r.Requirements, modelMatches)
	if err != nil {
		return r, err
	}
	r.LocallyRefreshed = r.InputKey != inputKey
	r.InputKey, r.CandidateHash, r.CandidateFacts, r.Matches = inputKey, candidateHash, c.Facts, matches
	r.Score, r.Coverage = Score(r.Requirements, matches)
	r.Breakdown = ScoreBreakdown(r.Requirements, matches)
	r.Qualifications = Qualification(j, p, r.Requirements, now)
	return r, nil
}
