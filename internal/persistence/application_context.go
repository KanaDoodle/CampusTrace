package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

// Contexts are append-only by phase. Legacy applications are deliberately not backfilled.
type ApplicationContext struct {
	Version           string                           `json:"schema_version"`
	Phase             string                           `json:"phase"`
	Key               string                           `json:"context_key"`
	CapturedAt        time.Time                        `json:"captured_at"`
	Reviewed          bool                             `json:"reviewed"`
	Job               d.Job                            `json:"job"`
	ObservationID     string                           `json:"observation_id"`
	Source            *ApplicationSource               `json:"source,omitempty"`
	JD                string                           `json:"jd"`
	Profile           d.Profile                        `json:"profile"`
	Candidate         matching.Candidate               `json:"candidate"`
	CandidateDocument string                           `json:"candidate_document"`
	CandidateHash     string                           `json:"candidate_hash"`
	ResumeVersion     string                           `json:"resume_version"`
	ResumeText        string                           `json:"resume_text,omitempty"`
	Analysis          *matching.Result                 `json:"analysis,omitempty"`
	AnalysisNotice    string                           `json:"analysis_notice"`
	Campaigns         []ApplicationCampaign            `json:"campaigns"`
	CompanyReports    []matching.HolisticCompanyReport `json:"company_reports"`
}
type ApplicationSource struct {
	PostingID  string    `json:"posting_id"`
	ExternalID string    `json:"external_id"`
	URL        string    `json:"url"`
	Name       string    `json:"name"`
	Trust      string    `json:"trust"`
	ObservedAt time.Time `json:"observed_at"`
	Timezone   string    `json:"timezone"`
}

func captureApplicationContext(ctx context.Context, q Queryer, user, job, resumeVersion, resumeText string, lock ...bool) (ApplicationContext, error) {
	v := ApplicationContext{Version: "application-context-v1", ResumeVersion: resumeVersion, ResumeText: resumeText, Campaigns: []ApplicationCampaign{}, CompanyReports: []matching.HolisticCompanyReport{}}
	if e := CheckJobAccess(ctx, q, user, job); e != nil {
		return v, e
	}
	var e error
	jobQuery := "SELECT body FROM jobs WHERE id=?"
	if len(lock) > 0 && lock[0] {
		jobQuery += " FOR UPDATE"
	}
	v.Job, e = One[d.Job](ctx, q, jobQuery, job)
	if e != nil {
		return v, e
	}
	o, e := One[d.Observation](ctx, q, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at DESC,id DESC LIMIT 1", job)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	if e == nil && o.FetchStatus == "SUCCESS" {
		posting, err := One[d.Posting](ctx, q, "SELECT body FROM postings WHERE id=?", o.PostingID)
		if err != nil {
			return v, err
		}
		src, err := One[d.Source](ctx, q, "SELECT body FROM sources WHERE id=? AND (JSON_UNQUOTE(JSON_EXTRACT(body,'$.visibility'))='GLOBAL' OR (JSON_UNQUOTE(JSON_EXTRACT(body,'$.visibility'))='PRIVATE' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.owner_id'))=?))", posting.SourceID, user)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return v, err
		}
		if err == nil {
			v.JD = o.Text
			v.ObservationID = o.ID
			v.Source = &ApplicationSource{PostingID: posting.ID, ExternalID: posting.ExternalID, URL: posting.URL, Name: src.Name, Trust: src.Trust, ObservedAt: o.ObservedAt, Timezone: o.Timezone}
		}
	}
	v.Profile, v.Candidate, e = matchCandidate(ctx, q, user, "")
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	if e == nil {
		v.CandidateDocument = matching.ReviewedDocument(v.Candidate)
		v.CandidateHash = v.Candidate.Hash()
	}
	r, e := One[matching.Result](ctx, q, "SELECT body FROM job_match_results WHERE user_id=? AND job_id=?", user, job)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	v.AnalysisNotice = "当时没有与留档材料一致的单岗分析；准备时直接核对留档原文。"
	if e == nil && r.CandidateHash == v.CandidateHash && r.RequirementsKey == matching.RequirementKey(cleanJobText(v.JD, ""), r.Model) && r.InputKey == matching.JobInputKey(v.Job, r.RequirementsKey, v.CandidateHash) {
		v.Analysis = &r
		v.AnalysisNotice = "留档时有效的单岗分析"
	}
	v.Campaigns, e = Many[ApplicationCampaign](ctx, q, "SELECT c.body FROM application_campaigns c JOIN application_campaign_jobs j ON j.campaign_id=c.id AND j.user_id=c.user_id WHERE j.user_id=? AND j.job_id=? ORDER BY c.id", user, job)
	if e != nil {
		return v, e
	}
	// Preserve provenance; reports may cover a subset and are labelled as historical evidence.
	v.CompanyReports, e = Many[matching.HolisticCompanyReport](ctx, q, "SELECT body FROM company_match_reports WHERE user_id=? AND company_name=? ORDER BY updated_at DESC,input_key LIMIT 5", user, v.Job.Company)
	if e != nil {
		return v, e
	}
	v.Key = d.Hash(d.JSON(v))
	return v, nil
}

func saveApplicationContext(ctx context.Context, tx *sql.Tx, user string, a d.Application, phase, key, resumeText string) error {
	v, e := captureApplicationContext(ctx, tx, user, a.JobID, a.ResumeVersion, resumeText, true)
	if e != nil {
		return e
	}
	if key != "" && key != v.Key {
		return ErrConflict
	}
	v.Phase = phase
	v.CapturedAt = time.Now().UTC()
	v.Reviewed = key != ""
	_, e = tx.ExecContext(ctx, "INSERT INTO application_contexts(application_id,phase,user_id,context_key,body) VALUES(?,?,?,?,?)", a.ID, phase, user, v.Key, d.JSON(v))
	return e
}

func (s *Store) ApplicationContextPreview(ctx context.Context, user, id, resumeText string) (ApplicationContext, error) {
	if len(id) != 32 || len(resumeText) > 60000 {
		return ApplicationContext{}, ErrValidation
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return ApplicationContext{}, e
	}
	defer tx.Rollback()
	a, e := One[d.Application](ctx, tx, "SELECT body FROM applications WHERE id=? AND user_id=?", id, user)
	if e != nil {
		return ApplicationContext{}, e
	}
	if a.AppliedAt != nil {
		return ApplicationContext{}, ErrConflict
	}
	return captureApplicationContext(ctx, tx, user, a.JobID, a.ResumeVersion, resumeText)
}

type ApplicationDossier struct {
	Application ApplicationRecord         `json:"application"`
	Context     *ApplicationContext       `json:"context"`
	Contexts    []ApplicationContext      `json:"contexts"`
	Legacy      bool                      `json:"legacy"`
	Preparation *matching.PreparationPlan `json:"preparation,omitempty"`
	Notices     []RecruitmentNotice       `json:"notices"`
	Interviews  []d.Interview             `json:"interviews"`
}

func (s *Store) ApplicationDossier(ctx context.Context, user, id string) (ApplicationDossier, error) {
	v := ApplicationDossier{}
	if len(id) != 32 {
		return v, ErrValidation
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	rows, e := applicationRecords(ctx, tx, user, id)
	if e != nil {
		return v, e
	}
	if len(rows) != 1 {
		return v, ErrNotFound
	}
	v.Application = rows[0]
	v.Contexts, e = Many[ApplicationContext](ctx, tx, "SELECT body FROM application_contexts WHERE application_id=? AND user_id=? ORDER BY phase", id, user)
	if e != nil {
		return v, e
	}
	for i := range v.Contexts {
		c := &v.Contexts[i]
		if v.Context == nil || c.Phase == "SUBMITTED" {
			v.Context = c
		}
	}
	v.Legacy = v.Context == nil
	if v.Context != nil && v.Context.Analysis != nil {
		c := v.Context
		p := matching.BuildPreparation(matching.DecisionInput{Job: c.Job, State: "ANALYZED", Result: c.Analysis}, c.Profile, c.CapturedAt)
		v.Preparation = &p
	}
	v.Notices, e = Many[RecruitmentNotice](ctx, tx, "SELECT body FROM recruitment_notices WHERE user_id=? AND application_id=? ORDER BY created_at,id", user, id)
	if e != nil {
		return v, e
	}
	v.Interviews, e = Many[d.Interview](ctx, tx, "SELECT body FROM interviews WHERE user_id=? AND application_id=? ORDER BY id", user, id)
	return v, e
}
