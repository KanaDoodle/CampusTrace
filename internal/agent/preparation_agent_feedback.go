package agent

import (
	"errors"
	"fmt"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

type preparationReferenceError struct {
	Item, Reference int
	Unavailable     bool
}

func (e *preparationReferenceError) Error() string {
	if e.Unavailable {
		return fmt.Sprintf("%s: item %d reference %d source_id is not available", p.ErrValidation, e.Item, e.Reference)
	}
	return fmt.Sprintf("%s: item %d reference %d must quote its source text literally", p.ErrValidation, e.Item, e.Reference)
}
func (e *preparationReferenceError) Unwrap() error { return p.ErrValidation }

// Find exact quotations only among material already read by this execution.
// The model must explicitly choose the right source; this never rebinds a
// reference or makes a new source available on the model's behalf.
func preparationPlanRepair(state *d.PreparationState, plan d.PreparationPlan, err error) map[string]any {
	out := map[string]any{
		"constraints": map[string]int{"total_minutes": state.Input.Minutes, "days": state.Input.Days, "minimum_item_minutes": 5},
	}
	var refError *preparationReferenceError
	if !errors.As(err, &refError) || refError.Item >= len(plan.Items) || refError.Reference >= len(plan.Items[refError.Item].References) {
		return out
	}
	ref := plan.Items[refError.Item].References[refError.Reference]
	if strings.TrimSpace(ref.Quote) == "" || len(ref.Quote) > 1000 {
		return out
	}
	type candidate struct {
		ID            string `json:"source_id"`
		Kind          string `json:"kind"`
		Title         string `json:"title"`
		ApplicationID string `json:"application_id,omitempty"`
	}
	matches := []candidate{}
	for _, source := range state.Sources {
		if source.ID != ref.SourceID && strings.Contains(source.Text, ref.Quote) {
			matches = append(matches, candidate{source.ID, source.Kind, prefixUTF8(source.Title, 240), source.ApplicationID})
			if len(matches) == 4 {
				break
			}
		}
	}
	out["citation"] = map[string]any{
		"item_index":       refError.Item,
		"reference_index":  refError.Reference,
		"matching_sources": matches,
		"instruction":      "The quotation does not belong to the supplied source. Matching sources, if any, are already-read material. Explicitly choose the correct source with its original scope/version, or quote the intended source verbatim. JD/HISTORY items still require their corresponding source kind. No reference has been rebound.",
	}
	return out
}
