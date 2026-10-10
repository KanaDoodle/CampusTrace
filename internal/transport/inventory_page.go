package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

var inventoryFields = []string{"id", "company", "title", "cities", "input_key", "state", "analysis_mode", "excluded_reason", "disposition", "preliminary_score", "role", "tier", "direction", "score", "priority", "fit", "created_at", "updated_at", "application", "text_bytes", "job_type", "current_status", "company_placement", "coverage", "campaign"}

type inventoryIndex struct {
	Fields  []string `json:"fields"`
	Rows    [][]any  `json:"rows,omitempty"`
	Upserts [][]any  `json:"upserts,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Full    bool     `json:"full"`
}

func inventoryTuples(v inventorySnapshot) [][]any {
	out := make([][]any, 0, len(v.Jobs))
	for _, r := range v.Jobs {
		role, tier, direction, fit := "", "", "UNCERTAIN", ""
		if r.Local != nil {
			role, tier, direction = r.Local.Role, r.Local.Tier, r.Local.Direction.Status
		}
		if r.Holistic != nil {
			fit = r.Holistic.Fit
		}
		out = append(out, []any{r.Job.ID, r.Job.Company, r.Job.Title, r.Cities, r.InputKey, r.State, r.AnalysisMode, r.ExcludedReason, r.Disposition, r.PreliminaryScore, role, tier, direction, r.Score, r.Priority, fit, r.Job.CreatedAt, r.Job.UpdatedAt, r.Application, r.TextBytes, r.Job.JobType, r.Job.CurrentStatus, r.CompanyPlacement, r.Coverage, r.Campaign})
	}
	return out
}
func inventoryChanges(now, old [][]any, full bool) inventoryIndex {
	out := inventoryIndex{Fields: inventoryFields, Full: full}
	if full {
		out.Rows = now
		return out
	}
	previous := map[string]string{}
	for _, r := range old {
		body, _ := json.Marshal(r)
		previous[r[0].(string)] = string(body)
	}
	for _, r := range now {
		id := r[0].(string)
		body, _ := json.Marshal(r)
		if previous[id] != string(body) {
			out.Upserts = append(out.Upserts, r)
		}
		delete(previous, id)
	}
	for id := range previous {
		out.Removed = append(out.Removed, id)
	}
	return out
}

func (a *API) inventoryRoute(on func(string, http.HandlerFunc)) {
	on("POST /api/matching/inventory", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			matchPreviewRequest
			Known string   `json:"known_snapshot"`
			IDs   []string `json:"job_ids"`
		}
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		if len(in.Known) > 64 || len(in.IDs) > 50 {
			write(w, nil, p.ErrValidation)
			return
		}
		seen := map[string]bool{}
		for _, id := range in.IDs {
			if len(id) != 32 || seen[id] {
				write(w, nil, p.ErrValidation)
				return
			}
			seen[id] = true
		}
		model, err := a.matchIdentity(in.matchPreviewRequest)
		if err != nil {
			write(w, nil, err)
			return
		}
		current, key, previous, err := a.Store.InventoryView(r.Context(), user(r), model, strings.TrimSpace(in.MaskName), in.Known)
		if err != nil {
			if errors.Is(err, p.ErrNotFound) {
				codedError(w, 409, "MATCH_PROFILE_REQUIRED")
			} else {
				matchFailure(w, err)
			}
			return
		}
		view := inventoryPreview(current)
		index := inventoryIndex{Fields: inventoryFields}
		if key != in.Known || previous == nil {
			var old [][]any
			if previous != nil {
				old = inventoryTuples(inventoryPreview(*previous))
			}
			index = inventoryChanges(inventoryTuples(view), old, previous == nil)
		}
		cards := []inventoryRow{}
		for _, row := range view.Jobs {
			if seen[row.Job.ID] {
				cards = append(cards, row)
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, map[string]any{"snapshot_key": key, "index": index, "jobs": cards, "candidate": view.Candidate, "candidate_hash": view.CandidateHash, "settings": view.Settings, "calls_today": view.CallsToday, "city_aliases": view.CityAliases}, nil)
	})
}
