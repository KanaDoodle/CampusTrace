package transport

import "github.com/KanaDoodle/CampusTrace/internal/matching"

func addWholeDiagnostic(diag map[string]any, v *matching.ValidationError) {
	switch v.Scope {
	case "REQUIREMENT", "MATCH", "HOLISTIC_STRENGTH", "HOLISTIC_GAP", "HOLISTIC_BLOCKER", "HOLISTIC_GATE", "COMPANY_CHOICE":
		diag["item_scope"] = v.Scope
	}
	if v.CitationIndex > 0 && v.CitationIndex <= 4 {
		diag["citation_index"] = v.CitationIndex
	}
}
