package transport

import (
	"context"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const maxChatImportBytes = 2 << 20
const maxChatImportJobs = 100

type chatImportRequest struct {
	Document   matching.ChatDocument `json:"document"`
	MaskName   string                `json:"mask_name"`
	PreviewKey string                `json:"preview_key,omitempty"`
}
type chatImportRow struct {
	ID       string          `json:"job_id"`
	Title    string          `json:"title"`
	Company  string          `json:"company"`
	Replaces bool            `json:"replaces"`
	Result   matching.Result `json:"result"`
}
type chatImportPreview struct {
	Comparisons     []matching.HolisticCompanyReport `json:"comparisons"`
	CompanyIssues   []string                         `json:"company_issues"`
	Key             string                           `json:"preview_key"`
	Total           int                              `json:"total"`
	Jobs            []chatImportRow                  `json:"jobs"`
	Issues          []chatImportIssue                `json:"issues"`
	EvidenceReviews int                              `json:"evidence_reviews"`
}

type chatImportDiagnostic struct {
	Reason   string `json:"validation_reason"`
	Job      int    `json:"job_index"`
	Item     int    `json:"item_index"`
	Scope    string `json:"item_scope,omitempty"`
	Related  int    `json:"related_item_index,omitempty"`
	Expected int    `json:"expected,omitempty"`
	Actual   int    `json:"actual,omitempty"`
}
type chatImportIssue struct {
	ID         string               `json:"job_id"`
	Title      string               `json:"title"`
	Company    string               `json:"company"`
	Diagnostic chatImportDiagnostic `json:"diagnostic"`
}

func chatImportDiagnostics(err error) []matching.ValidationError {
	var many *matching.ChatValidationErrors
	if errors.As(err, &many) {
		return many.Issues
	}
	var one *matching.ValidationError
	if errors.As(err, &one) {
		return []matching.ValidationError{*one}
	}
	reason := "CHAT_REQUIREMENT_INVALID"
	if errors.Is(err, matching.ErrCapacity) {
		reason = "CHAT_REQUIREMENTS_LIMIT"
	}
	if errors.Is(err, p.ErrStaleInput) {
		reason = "CHAT_JOB_STALE"
	}
	return []matching.ValidationError{{Reason: reason}}
}

func decodeChatImport(r *http.Request, in *chatImportRequest) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxChatImportBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxChatImportBytes {
		return matching.ErrCapacity
	}
	return d.StrictLimit(raw, in, maxChatImportBytes)
}
func chatImportFailure(w http.ResponseWriter, err error, job int) {
	var v *matching.ValidationError
	if errors.As(err, &v) {
		diagnostic := map[string]any{"validation_reason": v.Reason, "job_index": job, "item_index": v.ItemIndex, "related_item_index": v.RelatedItemIndex}
		slog.Warn("chat matching import rejected", "request_id", w.Header().Get("X-Request-ID"), "diagnostic", diagnostic)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{"error": "MATCH_CHAT_INVALID", "code": "MATCH_CHAT_INVALID", "diagnostic": diagnostic})
	} else if errors.Is(err, matching.ErrInvalid) {
		codedError(w, 400, "MATCH_CHAT_INVALID")
	} else if errors.Is(err, matching.ErrCapacity) {
		codedError(w, 400, "MATCH_CHAT_CAPACITY")
	} else if errors.Is(err, p.ErrStaleInput) {
		codedError(w, 409, "MATCH_CHAT_STALE")
	} else if errors.Is(err, p.ErrMatchRunBusy) {
		codedError(w, 409, "MATCH_BUSY")
	} else {
		write(w, nil, err)
	}
}
func (a *API) reviewChatImport(r *http.Request, in chatImportRequest) (chatImportPreview, []matching.Result, map[string]string, error) {
	out := chatImportPreview{Jobs: []chatImportRow{}, Issues: []chatImportIssue{}}
	doc := in.Document
	if doc.Version != matching.ChatVersion && doc.Version != matching.HolisticChatVersion {
		return out, nil, nil, &matching.ValidationError{Reason: "CHAT_VERSION"}
	}
	if len(doc.Jobs) == 0 || len(doc.Jobs) > maxChatImportJobs {
		return out, nil, nil, matching.ErrCapacity
	}
	if len(doc.PromptRevision) > 80 {
		return out, nil, nil, matching.ErrInvalid
	}
	out.Total = len(doc.Jobs)
	if _, err := a.matchIdentity(matchPreviewRequest{MaskName: in.MaskName}); err != nil {
		return out, nil, nil, err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, job := range doc.Jobs {
		if seen[job.ID] || strings.TrimSpace(job.ID) == "" {
			return out, nil, nil, &matching.ValidationError{Reason: "JOB_COUNT"}
		}
		seen[job.ID] = true
		ids = append(ids, job.ID)
	}
	snapshot, err := a.Store.MatchImportSnapshot(r.Context(), user(r), strings.TrimSpace(in.MaskName), ids)
	if err != nil {
		return out, nil, nil, err
	}
	if snapshot.CandidateHash != doc.CandidateHash {
		return out, nil, nil, p.ErrStaleInput
	}
	rows := map[string]p.MatchJob{}
	for _, row := range snapshot.Jobs {
		rows[row.Job.ID] = row
	}
	baseSnapshot := snapshot
	if strings.TrimSpace(in.MaskName) != "" {
		baseSnapshot, err = a.Store.MatchImportSnapshot(r.Context(), user(r), "", ids)
		if err != nil {
			return out, nil, nil, err
		}
	}
	baseRows := map[string]p.MatchJob{}
	for _, row := range baseSnapshot.Jobs {
		baseRows[row.Job.ID] = row
	}
	results := []matching.Result{}
	validIDs := []string{}
	for i, job := range doc.Jobs {
		row := rows[job.ID]
		// Keep the preview identity deterministic across revalidation. The actual
		// import time is assigned only after the preview has been confirmed.
		var result matching.Result
		var e error
		if row.Text == "" || job.InputKey != row.InputKey {
			e = p.ErrStaleInput
		} else {
			if (doc.Version == matching.HolisticChatVersion) != (job.Assessment != nil) {
				e = &matching.ValidationError{Reason: "HOLISTIC_MIXED_FORMAT"}
			} else {
				result, e = matching.ImportChatJob(job, row.Text, row.Job, snapshot.Profile, snapshot.Candidate, time.Time{})
			}
		}
		if e != nil {
			for _, v := range chatImportDiagnostics(e) {
				out.Issues = append(out.Issues, chatImportIssue{row.Job.ID, row.Job.Title, row.Job.Company, chatImportDiagnostic{v.Reason, i + 1, v.ItemIndex, v.Scope, v.RelatedItemIndex, v.Expected, v.Actual}})
			}
			continue
		}
		result.SourceContextKey = matching.JobInputKey(baseRows[job.ID].Job, matching.RequirementKey(baseRows[job.ID].Text, matching.ChatIdentity), baseSnapshot.CandidateHash)
		results = append(results, result)
		validIDs = append(validIDs, job.ID)
		out.EvidenceReviews += matching.EvidenceReviewCount(result.Matches)
		out.Jobs = append(out.Jobs, chatImportRow{row.Job.ID, row.Job.Title, row.Job.Company, false, result})
	}
	accepted := map[string]bool{}
	for _, id := range validIDs {
		accepted[id] = true
	}
	seenReports := map[string]bool{}
	for _, report := range doc.Comparisons {
		jobs := []matching.HolisticJob{}
		valid := doc.Version == matching.HolisticChatVersion && !seenReports[report.Company]
		seenReports[report.Company] = true
		for _, choice := range report.Choices {
			row, ok := rows[choice.ID]
			if !ok || !accepted[choice.ID] || row.ExcludedReason != "" {
				valid = false
				break
			}
			jobs = append(jobs, p.WholeJobs([]p.MatchJob{row}, in.MaskName)...)
		}
		report.Model = matching.ChatIdentity
		if report.CandidateHash != snapshot.CandidateHash || report.InputKey != matching.CompanyInputKey(snapshot.Candidate, jobs, matching.ChatIdentity) {
			valid = false
		}
		if !valid || matching.ValidateHolisticInput(snapshot.Candidate, jobs, true) != nil || matching.ValidateCompanyReport(report, snapshot.Candidate, jobs) != nil {
			out.CompanyIssues = append(out.CompanyIssues, "同公司比较未保存：请核对 "+report.Company+" 的比较范围、编号和关键引用。单岗有效结果仍可导入。")
			continue
		}
		report.Company = rows[report.Choices[0].ID].Job.Company
		out.Comparisons = append(out.Comparisons, report)
	}
	previous, err := a.Store.MatchResultVersions(r.Context(), user(r), validIDs)
	if err != nil {
		return out, nil, nil, err
	}
	for i := range out.Jobs {
		out.Jobs[i].Replaces = previous[out.Jobs[i].ID] != "NONE"
	}
	keyRows := append([]chatImportRow{}, out.Jobs...)
	for i := range keyRows {
		// Eligibility's ephemeral assessment ID is not part of the reviewed
		// content and is newly generated on each local calculation.
		keyRows[i].Result.Qualifications.ID = ""
	}
	if len(results) > 0 {
		// Bind the complete submitted document as well as the accepted subset.
		// An edit, changed input, or newly accepted job requires a new preview.
		out.Key = d.Hash(user(r) + "\n" + strings.TrimSpace(in.MaskName) + "\n" + d.JSON(doc) + "\n" + d.JSON(keyRows) + "\n" + d.JSON(out.Issues) + "\n" + d.JSON(out.Comparisons) + "\n" + d.JSON(previous))
	}
	return out, results, previous, nil
}
func (a *API) importChatMatches(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in chatImportRequest
	if err := decodeChatImport(r, &in); err != nil {
		if errors.Is(err, matching.ErrCapacity) {
			chatImportFailure(w, err, 0)
		} else {
			codedError(w, 400, "MATCH_CHAT_FORMAT")
		}
		return
	}
	preview, results, previous, err := a.reviewChatImport(r, in)
	if err != nil {
		var v *matching.ValidationError
		job := 0
		if errors.As(err, &v) {
			job = v.JobIndex
		}
		chatImportFailure(w, err, job)
		return
	}
	if r.PathValue("action") == "preview" {
		if len(d.JSON(preview)) > maxChatImportBytes {
			codedError(w, 400, "MATCH_CHAT_CAPACITY")
			return
		}
		decisionWrite(w, preview)
		return
	}
	if len(results) == 0 || in.PreviewKey == "" || in.PreviewKey != preview.Key {
		codedError(w, 409, "MATCH_CHAT_STALE")
		return
	}
	// Share the API matching lease, so importing cannot race an in-flight call.
	if a.Queue != nil {
		key := a.Queue.Prefix + "matching:lease:" + user(r)
		lease := d.ID()
		acquired, e := a.Queue.R.SetNX(r.Context(), key, lease, 2*time.Minute).Result()
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
			a.Queue.R.Eval(cleanup, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{key}, lease)
		}()
	}
	now := time.Now().UTC()
	for i := range results {
		results[i].AnalyzedAt = now
	}
	if err = a.Store.SaveChatMatches(r.Context(), user(r), strings.TrimSpace(in.MaskName), results, previous, preview.Comparisons...); err != nil {
		chatImportFailure(w, err, 0)
		return
	}
	write(w, map[string]any{"imported": len(results), "skipped": preview.Total - len(results), "evidence_reviews": preview.EvidenceReviews, "comparisons": len(preview.Comparisons)}, nil)
}
