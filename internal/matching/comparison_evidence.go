package matching

import "regexp"

// Review notes are assigned locally, never accepted from model output.
const InvalidAbilityEvidence = "INVALID_ABILITY_EVIDENCE"
const ExperienceUnconfirmed = "EXPERIENCE_UNCONFIRMED"
const ContextUnconfirmed = "CONTEXT_UNCONFIRMED"

var practicalExperience = regexp.MustCompile(`(?i)(?:实际|实战|实践|使用|搭建|构建|建设|开发|项目|编码|落地)(?:的)?经验|实际(?:使用|搭建|构建|开发|实现)|hands.on experience|practical experience`)

type comparisonCandidate struct {
	Revision    uint64 `json:"revision"`
	Facts       []Fact `json:"facts"`
	Limitations []Fact `json:"limitations"`
}

func candidateForComparison(c Candidate) comparisonCandidate {
	v := comparisonCandidate{Revision: c.Revision, Facts: []Fact{}, Limitations: []Fact{}}
	for _, f := range c.Facts {
		switch {
		case isPreference(f.Kind):
			// Intent, cities and job types are used only by local preference rules.
		case f.Kind == "LIMITATION":
			v.Limitations = append(v.Limitations, f)
		default:
			v.Facts = append(v.Facts, f)
		}
	}
	return v
}

// Keep the wire schema separate from Match, so the model cannot forge the
// local review_note field in a saved result.
type comparisonMatch struct {
	RequirementID string               `json:"requirement_id"`
	Result        string               `json:"result"`
	Explanation   string               `json:"explanation"`
	Evidence      []comparisonCitation `json:"evidence"`
}

func abilityCitation(f Fact, r Requirement) bool {
	if f.Kind == "EDUCATION" && r.Category != "QUALIFICATION" {
		return false
	}
	if f.Kind == "LIMITATION" {
		return false
	}
	if !isPreference(f.Kind) {
		return true
	}
	// These two comparisons are computed locally, with their own saved facts.
	return r.Category == "QUALIFICATION" && ((r.ClaimType == "LOCATION" && (f.Kind == "CITY_PREFERRED" || f.Kind == "CITY_ACCEPTABLE")) || (r.ClaimType == "JOB_TYPE" && f.Kind == "JOB_TYPE_PREFERENCE"))
}

// Call only after structural and exact-citation validation. Withdraw the whole
// affected judgment even when it also cites usable facts: its reasoning may
// depend on the forbidden citation. Never treat missing proof as inability.
func withdrawInvalidAbilityEvidence(c Candidate, reqs []Requirement, matches []Match) {
	facts := map[string]Fact{}
	for _, f := range c.Facts {
		facts[f.ID] = f
	}
	requirements := map[string]Requirement{}
	for _, r := range reqs {
		requirements[r.ID] = r
	}
	for i, m := range matches {
		if m.Result != "DIRECT" && m.Result != "PARTIAL" && m.Result != "TRANSFERABLE" {
			continue
		}
		onlyDeclarations := len(m.Evidence) > 0
		for _, citation := range m.Evidence {
			kind := facts[citation.ID].Kind
			onlyDeclarations = onlyDeclarations && (kind == "SKILL" || kind == "LANGUAGE")
			if !abilityCitation(facts[citation.ID], requirements[m.RequirementID]) {
				matches[i].Result = "NO_EVIDENCE"
				matches[i].Explanation = "模型引用了求职偏好或项目局限来证明能力，原结论已撤销。此项暂无足够的有效依据，请核对真实经历并补充资料；不代表你不会。"
				matches[i].Evidence = []Citation{}
				matches[i].ReviewNote = InvalidAbilityEvidence
				break
			}
		}
		r := requirements[m.RequirementID]
		// Declared skills help application triage but cannot prove hands-on
		// work. Keep partial support without rejecting the whole batch.
		if matches[i].Result != "NO_EVIDENCE" && onlyDeclarations && (r.Category == "REQUIRED" || r.Category == "BONUS") && r.Aspect != "SOFT" && practicalExperience.MatchString(r.Text) {
			if matches[i].Result == "DIRECT" {
				matches[i].Result = "PARTIAL"
			}
			matches[i].Explanation = "已填写相关技能，可作为部分匹配参考；当前引用只有技能或语言自述，尚不能确认实际使用或项目经验。可先考虑投递，并核对自己能否讲清相关实践。"
			matches[i].ReviewNote = ExperienceUnconfirmed
		}
	}
}

func EvidenceReviewCount(matches []Match) int {
	n := 0
	for _, m := range matches {
		if m.ReviewNote == InvalidAbilityEvidence || m.ReviewNote == ExperienceUnconfirmed || m.ReviewNote == ContextUnconfirmed {
			n++
		}
	}
	return n
}
