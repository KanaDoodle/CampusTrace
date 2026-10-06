package matching

import d "github.com/KanaDoodle/CampusTrace/internal/domain"

// Only fixed categories and counts belong in diagnostics, never input text.
type CapacityError struct {
	Reason string
	Actual int
	Limit  int
}

func (e *CapacityError) Error() string { return ErrCapacity.Error() }
func (e *CapacityError) Unwrap() error { return ErrCapacity }

type comparisonRequest struct {
	Candidate comparisonModelCandidate `json:"candidate"`
	Jobs      []MatchInput             `json:"jobs"`
}

func ComparisonBytes(c Candidate, jobs []MatchInput) int {
	return len(d.JSON(comparisonRequest{candidateWithExcerpts(c), jobs}))
}

// Pack complete jobs within both the input budget and the existing output
// budget. An oversized single job stays intact for an explicit capacity error.
func ComparisonBatchSize(c Candidate, jobs []MatchInput) int {
	count, requirements := 0, 0
	for count < len(jobs) && count < MaxBatch {
		next := len(jobs[count].Requirements)
		if count > 0 && (requirements+next > 24 || ComparisonBytes(c, jobs[:count+1]) > MaxComparisonText) {
			break
		}
		requirements += next
		count++
	}
	return count
}
