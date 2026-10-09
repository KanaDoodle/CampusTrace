package matching

import "errors"

// Only formatting differences can be repaired at the model/chat boundary.
// Persistence validators still require literal substrings of the exact source.
// Copy nested slices so import previews never change the submitted document.
func PrepareHolisticAssessment(h HolisticAssessment, source string, c Candidate) (HolisticAssessment, error) {
	lists := []*[]HolisticFinding{&h.Strengths, &h.Gaps, &h.Blockers}
	for n, list := range lists {
		if len(*list) > 5 {
			return HolisticAssessment{}, invalid("HOLISTIC_FINDINGS", 0)
		}
		*list = append([]HolisticFinding{}, (*list)...)
		for i := range *list {
			f := &(*list)[i]
			quote, evidence, err := prepareWholeCitation(c, f.JobExcerpt, f.Evidence, source)
			if err != nil {
				return HolisticAssessment{}, wholeLocation(err, 0, i+1, wholeFindingScope(n))
			}
			f.JobExcerpt, f.Evidence = quote, evidence
		}
	}
	if len(h.Gates) > 8 {
		return HolisticAssessment{}, invalid("HOLISTIC_GATES", 0)
	}
	h.Gates = append([]HolisticGate{}, h.Gates...)
	for i := range h.Gates {
		quote, reason := restoreSourceExcerpt(source, h.Gates[i].Excerpt, 600)
		if reason != "" {
			return HolisticAssessment{}, wholeLocation(invalid("HOLISTIC_JOB_"+reason, 0), 0, i+1, "HOLISTIC_GATE")
		}
		h.Gates[i].Excerpt = quote
	}
	if err := ValidateHolistic(h, source, c); err != nil {
		return HolisticAssessment{}, err
	}
	return h, nil
}

func PrepareCompanyReport(r HolisticCompanyReport, c Candidate, jobs []HolisticJob) (HolisticCompanyReport, error) {
	if len(jobs) == 0 || len(r.Choices) != len(jobs) || len(jobs) > MaxCompanyJobs {
		return HolisticCompanyReport{}, invalid("COMPANY_SCOPE", 0)
	}
	byID, positions := wholeJobPositions(jobs)
	r.Choices = append([]CompanyChoice{}, r.Choices...)
	for i := range r.Choices {
		v := &r.Choices[i]
		j, ok := byID[v.ID]
		if !ok {
			return HolisticCompanyReport{}, invalid("COMPANY_SCOPE", 0)
		}
		quote, evidence, err := prepareWholeCitation(c, v.JobExcerpt, v.Evidence, j.Text)
		if err != nil {
			return HolisticCompanyReport{}, wholeLocation(err, positions[v.ID], i+1, "COMPANY_CHOICE")
		}
		v.JobExcerpt, v.Evidence = quote, evidence
	}
	if err := ValidateCompanyReport(r, c, jobs); err != nil {
		return HolisticCompanyReport{}, err
	}
	return r, nil
}

func prepareWholeCitation(c Candidate, excerpt string, evidence []Citation, source string) (string, []Citation, error) {
	quote, reason := restoreSourceExcerpt(source, excerpt, 1200)
	if reason != "" {
		return "", nil, invalid("HOLISTIC_JOB_"+reason, 0)
	}
	if len(evidence) > 4 {
		return "", nil, invalid("HOLISTIC_EVIDENCE_REQUIRED", 0)
	}
	materials := CandidateMaterials(c)
	out := append([]Citation{}, evidence...)
	for i := range out {
		f, known := materials[out[i].ID]
		if !known {
			return "", nil, &ValidationError{Reason: "FACT_UNKNOWN", CitationIndex: i + 1}
		}
		var reason string
		out[i].Excerpt, reason = restoreSourceExcerpt(f.Text, out[i].Excerpt, 1200)
		if reason != "" {
			return "", nil, &ValidationError{Reason: "HOLISTIC_EVIDENCE_" + reason, CitationIndex: i + 1}
		}
	}
	return quote, out, nil
}

func wholeFindingScope(n int) string {
	return []string{"HOLISTIC_STRENGTH", "HOLISTIC_GAP", "HOLISTIC_BLOCKER"}[n]
}

func wholeJobPositions(jobs []HolisticJob) (map[string]HolisticJob, map[string]int) {
	byID, positions := map[string]HolisticJob{}, map[string]int{}
	for i, j := range jobs {
		byID[j.ID], positions[j.ID] = j, i+1
	}
	return byID, positions
}

func wholeLocation(err error, job, item int, scope string) error {
	var v *ValidationError
	if !errors.As(err, &v) {
		return err
	}
	copy := *v
	if job > 0 {
		copy.JobIndex = job
	}
	if item > 0 {
		copy.ItemIndex = item
	}
	if scope != "" {
		copy.Scope = scope
	}
	return &copy
}
