package transport

import (
	"net/http"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

const maxChatExportJobs = 1000
const maxChatExportBytes = 5 << 20

// An allowlist keeps account, source URLs, raw resumes and model credentials out
// of the manual-chat package. This route only reads local data; no model runs.
type chatExportJob struct {
	ID             string   `json:"job_id"`
	InputKey       string   `json:"input_key"`
	Company        string   `json:"company"`
	Title          string   `json:"title"`
	Locations      []string `json:"locations"`
	JobType        string   `json:"job_type"`
	Status         string   `json:"recruitment_status"`
	ExcludedReason string   `json:"local_note,omitempty"`
	Text           string   `json:"text"`
}

func (a *API) exportMatches(w http.ResponseWriter, r *http.Request) {
	var in struct {
		matchPreviewRequest
		JobIDs        []string `json:"job_ids"`
		CandidateHash string   `json:"candidate_hash"`
	}
	if err := decode(r, &in); err != nil {
		write(w, nil, err)
		return
	}
	if len(in.JobIDs) == 0 || len(in.JobIDs) > maxChatExportJobs {
		codedError(w, 400, "MATCH_EXPORT_CAPACITY")
		return
	}
	identity, err := a.matchIdentity(in.matchPreviewRequest)
	if err != nil {
		write(w, nil, err)
		return
	}
	mask := strings.TrimSpace(in.MaskName)
	snapshot, err := a.Store.MatchExportSnapshot(r.Context(), user(r), identity, mask, in.JobIDs)
	if err != nil {
		matchFailure(w, err)
		return
	}
	if snapshot.CandidateHash != in.CandidateHash {
		codedError(w, 409, "MATCH_INPUT_CHANGED")
		return
	}
	clean := func(s string) string {
		if mask != "" {
			s = strings.ReplaceAll(s, mask, "[已遮盖姓名]")
		}
		return resume.Redact(s)
	}
	cleanList := func(values []string) []string {
		out := make([]string, 0, len(values))
		for _, v := range values {
			out = append(out, clean(v))
		}
		return out
	}
	byID := make(map[string]chatExportJob, len(snapshot.Jobs))
	for _, row := range snapshot.Jobs {
		if strings.TrimSpace(row.Text) == "" {
			codedError(w, 409, "MATCH_EXPORT_TEXT_REQUIRED")
			return
		}
		byID[row.Job.ID] = chatExportJob{row.Job.ID, matching.InputKey(matching.RequirementKey(row.Text, matching.ChatIdentity), snapshot.CandidateHash), clean(row.Job.Company), clean(row.Job.Title), cleanList(row.Job.Locations), row.Job.JobType, row.Job.CurrentStatus, row.ExcludedReason, row.Text}
	}
	jobs := make([]chatExportJob, 0, len(in.JobIDs))
	for _, id := range in.JobIDs {
		jobs = append(jobs, byID[id])
	}
	out := struct {
		Version        string              `json:"version"`
		PromptRevision string              `json:"prompt_revision"`
		ExportedAt     time.Time           `json:"exported_at"`
		CandidateHash  string              `json:"candidate_hash"`
		Candidate      matching.Candidate  `json:"candidate"`
		Preferences    map[string][]string `json:"preferences"`
		Jobs           []chatExportJob     `json:"jobs"`
	}{matching.ChatVersion, matching.ChatPromptRevision, time.Now().UTC(), snapshot.CandidateHash, snapshot.Candidate, map[string][]string{
		"preferred_job_types": cleanList(snapshot.Profile.PreferredTypes),
		"preferred_cities":    cleanList(snapshot.Profile.PreferredCities),
		"acceptable_cities":   cleanList(snapshot.Profile.AcceptableCities),
	}, jobs}
	if len(d.JSON(out)) > maxChatExportBytes {
		codedError(w, 400, "MATCH_EXPORT_CAPACITY")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, out, nil)
}
