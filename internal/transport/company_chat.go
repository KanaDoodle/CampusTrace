package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

type companyChatScope struct {
	Company  string   `json:"company"`
	JobIDs   []string `json:"job_ids"`
	MaskName string   `json:"mask_name"`
}

func (a *API) companyChatSnapshot(r *http.Request, in companyChatScope) (p.MatchSnapshot, []matching.HolisticJob, error) {
	if strings.TrimSpace(in.Company) == "" || len(in.Company) > 1024 || len(in.JobIDs) == 0 || len(in.JobIDs) > matching.MaxCompanyJobs {
		return p.MatchSnapshot{}, nil, p.ErrValidation
	}
	if _, err := a.matchIdentity(matchPreviewRequest{MaskName: in.MaskName}); err != nil {
		return p.MatchSnapshot{}, nil, err
	}
	seen := map[string]bool{}
	for _, id := range in.JobIDs {
		if id == "" || len(id) > 128 || seen[id] {
			return p.MatchSnapshot{}, nil, p.ErrValidation
		}
		seen[id] = true
	}
	snapshot, err := a.Store.MatchImportSnapshot(r.Context(), user(r), strings.TrimSpace(in.MaskName), in.JobIDs)
	if err != nil {
		return snapshot, nil, err
	}
	jobs := p.WholeJobs(snapshot.Jobs, strings.TrimSpace(in.MaskName))
	for i, row := range snapshot.Jobs {
		// The import document uses the redacted company label; exports start
		// with the actual selected company. Both resolve to one local scope.
		if row.Job.Company != in.Company && jobs[i].Company != in.Company {
			return snapshot, nil, p.ErrValidation
		}
		if row.ExcludedReason != "" {
			return snapshot, nil, p.ErrMatchJobUnavailable
		}
	}
	if err := matching.ValidateHolisticInput(snapshot.Candidate, jobs, true); err != nil {
		return snapshot, nil, err
	}
	return snapshot, jobs, nil
}

func (a *API) exportCompanyChat(w http.ResponseWriter, r *http.Request) {
	var in companyChatScope
	if err := decode(r, &in); err != nil {
		write(w, nil, err)
		return
	}
	in.Company = strings.TrimSpace(in.Company)
	snapshot, jobs, err := a.companyChatSnapshot(r, in)
	if err != nil {
		matchFailure(w, err)
		return
	}
	ids := []string{}
	choices := []matching.CompanyChoice{}
	for _, job := range jobs {
		ids = append(ids, job.ID)
		choices = append(choices, matching.CompanyChoice{ID: job.ID, Evidence: []matching.Citation{}})
	}
	doc := matching.CompanyChatDocument{Version: matching.CompanyChatVersion, PromptRevision: matching.HolisticPromptRevision,
		Company: jobs[0].Company, CandidateHash: snapshot.CandidateHash, InputKey: matching.CompanyInputKey(snapshot.Candidate, jobs, matching.ChatIdentity),
		JobIDs: ids, Choices: choices, Questions: []string{}}
	decisionWrite(w, struct {
		Document   matching.CompanyChatDocument `json:"document"`
		Candidate  matching.Candidate           `json:"candidate"`
		Jobs       []matching.HolisticJob       `json:"jobs"`
		Prompt     string                       `json:"prompt"`
		ExportedAt time.Time                    `json:"exported_at"`
	}{doc, matching.ReviewedCandidate(snapshot.Candidate), jobs, matching.CompanyChatPrompt, time.Now().UTC()})
}

type companyChatImportRequest struct {
	Document   matching.CompanyChatDocument `json:"document"`
	MaskName   string                       `json:"mask_name"`
	PreviewKey string                       `json:"preview_key,omitempty"`
}

func (a *API) importCompanyChat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in companyChatImportRequest
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxChatImportBytes+1))
	if err != nil || len(raw) > maxChatImportBytes {
		codedError(w, 400, "MATCH_CHAT_CAPACITY")
		return
	}
	if err := d.StrictLimit(raw, &in, maxChatImportBytes); err != nil {
		codedError(w, 400, "MATCH_CHAT_FORMAT")
		return
	}
	doc := in.Document
	if doc.Version != matching.CompanyChatVersion || doc.PromptRevision != matching.HolisticPromptRevision {
		chatImportFailure(w, &matching.ValidationError{Reason: "CHAT_VERSION"}, 0)
		return
	}
	snapshot, jobs, err := a.companyChatSnapshot(r, companyChatScope{Company: doc.Company, JobIDs: doc.JobIDs, MaskName: in.MaskName})
	if err != nil {
		if errors.Is(err, p.ErrMatchJobUnavailable) {
			matchFailure(w, err)
		} else {
			chatImportFailure(w, err, 0)
		}
		return
	}
	if doc.CandidateHash != snapshot.CandidateHash || doc.InputKey != matching.CompanyInputKey(snapshot.Candidate, jobs, matching.ChatIdentity) {
		chatImportFailure(w, p.ErrStaleInput, 0)
		return
	}
	report, err := matching.PrepareCompanyChat(doc, snapshot.Candidate, jobs)
	if err != nil {
		chatImportFailure(w, err, 0)
		return
	}
	report.Company = snapshot.Jobs[0].Job.Company
	base := snapshot
	if strings.TrimSpace(in.MaskName) != "" {
		base, err = a.Store.MatchImportSnapshot(r.Context(), user(r), "", doc.JobIDs)
		if err != nil {
			chatImportFailure(w, err, 0)
			return
		}
	}
	baseKey := matching.CompanyInputKey(base.Candidate, p.WholeJobs(base.Jobs), matching.ChatIdentity)
	previous, err := a.Store.CompanyChatVersion(r.Context(), user(r), report.Company, baseKey)
	if err != nil {
		write(w, nil, err)
		return
	}
	key := d.Hash(user(r) + "\n" + strings.TrimSpace(in.MaskName) + "\n" + d.JSON(doc) + "\n" + d.JSON(report) + "\n" + previous)
	if r.PathValue("action") == "preview" {
		decisionWrite(w, struct {
			Report   matching.HolisticCompanyReport `json:"report"`
			Jobs     []matching.HolisticJob         `json:"jobs"`
			Key      string                         `json:"preview_key"`
			Replaces bool                           `json:"replaces"`
		}{report, jobs, key, previous != "NONE"})
		return
	}
	if r.PathValue("action") != "confirm" || in.PreviewKey == "" || in.PreviewKey != key {
		codedError(w, 409, "MATCH_CHAT_STALE")
		return
	}
	// Coordinate with both synchronous API calls and persistent worker runs.
	if a.Queue != nil {
		leaseKey, lease := a.Queue.Prefix+"matching:lease:"+user(r), d.ID()
		acquired, e := a.Queue.R.SetNX(r.Context(), leaseKey, lease, 2*time.Minute).Result()
		if e != nil {
			write(w, nil, p.ErrBackendUnavailable)
			return
		}
		if !acquired {
			codedError(w, 409, "MATCH_BUSY")
			return
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
			defer cancel()
			a.Queue.R.Eval(cleanup, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{leaseKey}, lease)
		}()
	}
	err = a.Store.SaveReviewedCompanyReport(r.Context(), user(r), strings.TrimSpace(in.MaskName), report, jobs, previous)
	if err != nil {
		if errors.Is(err, p.ErrStaleInput) {
			codedError(w, 409, "MATCH_CHAT_STALE")
		} else {
			chatImportFailure(w, err, 0)
		}
		return
	}
	decisionWrite(w, map[string]any{"company": report.Company, "imported": len(report.Choices)})
}
