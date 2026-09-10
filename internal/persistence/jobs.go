package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"net/url"
	"strings"
	"time"
)

type Ingest struct {
	Company     string    `json:"company"`
	Title       string    `json:"title"`
	JobType     string    `json:"job_type"`
	Locations   []string  `json:"locations"`
	SourceID    string    `json:"source_id"`
	ExternalID  string    `json:"external_id"`
	URL         string    `json:"url"`
	Text        string    `json:"text"`
	FetchStatus string    `json:"fetch_status"`
	HTTPStatus  int       `json:"http_status"`
	ObservedAt  time.Time `json:"observed_at"`
}

func (i Ingest) Validate() error {
	if i.Company == "" || len(i.Company) > 200 || i.Title == "" || len(i.Title) > 300 || i.SourceID == "" || len(i.Text) > 60000 || len(i.Locations) > 30 || len(i.URL) > 2000 || (i.JobType != "FULL_TIME" && i.JobType != "INTERNSHIP" && i.JobType != "UNKNOWN") {
		return ErrValidation
	}
	switch i.FetchStatus {
	case "SUCCESS":
		if i.Text == "" {
			return ErrValidation
		}
	case "BLOCKED", "TIMEOUT", "HTTP_ERROR", "PARSE_ERROR":
	default:
		return ErrValidation
	}
	return nil
}

type Task struct {
	Generation        uint64    `json:"generation,omitempty"`
	ProcessingVersion string    `json:"processing_version,omitempty"`
	ID                string    `json:"task_id"`
	Type              string    `json:"task_type"`
	EntityID          string    `json:"entity_id"`
	Attempt           int       `json:"attempt"`
	CreatedAt         time.Time `json:"created_at"`
	CorrelationID     string    `json:"correlation_id"`
	Version           int       `json:"payload_version"`
	FirstFailed       time.Time `json:"first_failed,omitempty"`
}

func NewTask(typ, id string) Task {
	return Task{ID: d.ID(), Type: typ, EntityID: id, Attempt: 1, CreatedAt: time.Now().UTC(), CorrelationID: d.ID(), Version: 1}
}
func Outbox(ctx context.Context, tx *sql.Tx, t Task) error {
	if id := observability.From(ctx).RequestID; id != "" {
		t.CorrelationID = id
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO outbox(id,body,created_at) VALUES(?,?,?)", t.ID, d.JSON(t), t.CreatedAt)
	return err
}
func (s *Store) SaveSource(ctx context.Context, v d.Source) error {
	if v.ID == "" || v.Name == "" || (v.Trust != "OFFICIAL" && v.Trust != "THIRD_PARTY" && v.Trust != "MANUAL") {
		return ErrValidation
	}
	if v.Visibility == "" {
		v.Visibility = "GLOBAL"
		if v.Trust == "MANUAL" {
			v.Visibility = "PRIVATE"
		}
	}
	if v.Visibility != "GLOBAL" && v.Visibility != "PRIVATE" {
		return ErrValidation
	}
	if (v.Trust == "MANUAL" && v.Visibility != "PRIVATE") || (v.Visibility == "GLOBAL" && v.OwnerID != "") {
		return ErrValidation
	}
	if v.Timezone == "" {
		v.Timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(v.Timezone); err != nil {
		return ErrValidation
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO sources(id,body) VALUES(?,?)", v.ID, d.JSON(v))
	return err
}
func (s *Store) Ingest(ctx context.Context, i Ingest) (d.Observation, error) {
	var o d.Observation
	if err := i.Validate(); err != nil {
		return o, err
	}
	if i.ObservedAt.IsZero() {
		i.ObservedAt = time.Now().UTC()
	}
	if i.ObservedAt.After(time.Now().Add(time.Minute)) {
		return o, ErrValidation
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		source, err := One[d.Source](ctx, tx, "SELECT body FROM sources WHERE id=?", i.SourceID)
		if err != nil {
			return err
		}
		if source.Visibility == "" {
			source.Visibility = "GLOBAL"
			if source.Trust == "MANUAL" {
				source.Visibility = "PRIVATE"
			}
		}
		if source.Visibility == "PRIVATE" && source.OwnerID == "" {
			return ErrValidation
		}
		// Lock a normalized company row to serialize canonicalization within that company.
		company := d.Company{ID: d.ID(), Name: i.Company}
		_, err = tx.ExecContext(ctx, "INSERT INTO companies(id,normalized_name,body) VALUES(?,?,?) ON DUPLICATE KEY UPDATE id=id", company.ID, d.Normalize(i.Company), d.JSON(company))
		if err != nil {
			return err
		}
		company, err = One[d.Company](ctx, tx, "SELECT body FROM companies WHERE normalized_name=? FOR UPDATE", d.Normalize(i.Company))
		if err != nil {
			return err
		}
		key := SourceKey(i)

		posting, err := One[d.Posting](ctx, tx, "SELECT body FROM postings WHERE source_key=? FOR UPDATE", key)
		var job d.Job
		if errors.Is(err, sql.ErrNoRows) {
			fingerprint := rules.Fingerprint(i.Company, i.Title, i.JobType, i.Locations, i.Text)
			candidates, e := Many[d.Job](ctx, tx, "SELECT body FROM jobs WHERE fingerprint=? AND visibility=? AND owner_id=? ORDER BY id", fingerprint, source.Visibility, source.OwnerID)
			if e != nil {
				return e
			}
			reason := "new v2 identity; no compatible company/title/type/location/source candidate"
			for _, candidate := range candidates {
				if !rules.Compatible(candidate, company.ID, i.Title, i.JobType, i.Locations) {
					continue
				}
				// Distinct identities in one source may designate different BU/requisitions.
				// A digest match is only a candidate, never authority to merge.
				var count int
				if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM postings WHERE job_id=? AND source_id=? AND source_key<>?", candidate.ID, source.ID, key).Scan(&count); e != nil {
					return e
				}
				if count > 0 {
					continue
				}
				original, e := One[d.Observation](ctx, tx, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at,id LIMIT 1", candidate.ID)
				if errors.Is(e, sql.ErrNoRows) {
					continue
				}
				if e != nil {
					return e
				}
				// Recheck actual content too: a stale/corrupted hash candidate must not
				// merge distinct BU or source details omitted from the top-level metadata.
				if original.Text != i.Text {
					continue
				}
				job, e = One[d.Job](ctx, tx, "SELECT body FROM jobs WHERE id=? FOR UPDATE", candidate.ID)
				if e != nil {
					return e
				}
				err = nil
				reason = "v2 verified company_id, normalized title, job_type, location set, exact original content and compatible source identity"
				break
			}
			if job.ID == "" {
				job = d.Job{ID: d.ID(), CompanyID: company.ID, Company: company.Name, Title: i.Title, JobType: i.JobType, Locations: i.Locations, Fingerprint: fingerprint, CurrentStatus: "UNKNOWN", CreatedAt: i.ObservedAt, UpdatedAt: i.ObservedAt, Visibility: source.Visibility, OwnerID: source.OwnerID}
				_, err = tx.ExecContext(ctx, "INSERT INTO jobs(id,company_id,fingerprint,visibility,owner_id,body) VALUES(?,?,?,?,?,?)", job.ID, company.ID, fingerprint, source.Visibility, source.OwnerID, d.JSON(job))
			}

			if err != nil {
				return err
			}
			posting = d.Posting{ID: d.ID(), JobID: job.ID, SourceID: source.ID, ExternalID: i.ExternalID, URL: i.URL, FirstSeen: i.ObservedAt, LastSeen: i.ObservedAt, MergeReason: reason}
			_, err = tx.ExecContext(ctx, "INSERT INTO postings(id,job_id,source_id,source_key,body) VALUES(?,?,?,?,?)", posting.ID, job.ID, source.ID, key, d.JSON(posting))
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			job, err = One[d.Job](ctx, tx, "SELECT body FROM jobs WHERE id=? FOR UPDATE", posting.JobID)
			if err != nil {
				return err
			}
			if job.Visibility == "" {
				job.Visibility = "GLOBAL"
			}
			if job.CompanyID != company.ID || job.Visibility != source.Visibility || job.OwnerID != source.OwnerID {
				return ErrValidation
			}
			if i.ObservedAt.After(posting.LastSeen) {
				posting.LastSeen = i.ObservedAt
				if i.ObservedAt.After(job.UpdatedAt) && !rules.Compatible(job, company.ID, i.Title, i.JobType, i.Locations) {
					job.Title, job.JobType, job.Locations = i.Title, i.JobType, i.Locations
					job.UpdatedAt = i.ObservedAt
					job.Fingerprint = rules.Fingerprint(i.Company, i.Title, i.JobType, i.Locations, i.Text)
					if _, err = tx.ExecContext(ctx, "UPDATE jobs SET fingerprint=?,body=? WHERE id=?", job.Fingerprint, d.JSON(job), job.ID); err != nil {
						return err
					}
				}
			}
			_, err = tx.ExecContext(ctx, "UPDATE postings SET body=? WHERE id=?", d.JSON(posting), posting.ID)
			if err != nil {
				return err
			}
		}
		job.Revision++
		if _, err = tx.ExecContext(ctx, "UPDATE jobs SET body=? WHERE id=?", d.JSON(job), job.ID); err != nil {
			return err
		}
		o = d.Observation{ID: d.ID(), PostingID: posting.ID, JobID: posting.JobID, ObservedAt: i.ObservedAt, FetchStatus: i.FetchStatus, HTTPStatus: i.HTTPStatus, Text: i.Text, ParserVersion: d.ParserVersion, Timezone: source.Timezone, ExtractionStatus: "PENDING", Trust: source.Trust, ApplySignal: "UNKNOWN"}
		if i.FetchStatus == "SUCCESS" {
			o.Hash = d.Hash(i.Text)
		} else {
			o.ErrorCategory = i.FetchStatus
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO observations(id,posting_id,job_id,observed_at,body) VALUES(?,?,?,?,?)", o.ID, o.PostingID, o.JobID, o.ObservedAt, d.JSON(o))
		if err != nil {
			return err
		}
		return Outbox(ctx, tx, NewTask("ANALYZE", o.ID))
	})
	return o, err
}
func (s *Store) Analyzed(ctx context.Context, id, version string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM analysis_results WHERE observation_id=? AND analysis_version=?", id, version).Scan(&n)
	return n > 0, err
}
func (s *Store) PersistAnalysis(ctx context.Context, id, version string, claims []d.Claim) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		o, err := lockAnalysis(ctx, tx, id)
		if err != nil {
			return err
		}
		var generation uint64
		if err = tx.QueryRowContext(ctx, "SELECT generation FROM analysis_generations WHERE observation_id=? AND processing_version=?", id, version).Scan(&generation); err != nil {
			return err
		}
		for _, c := range claims {
			if err = c.Validate(o.Text); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO analysis_results(observation_id,analysis_version,created_at) VALUES(?,?,?)", id, version, time.Now().UTC())
		if duplicate(err) {
			return reconcileAnalysis(ctx, tx, o, version, generation)
		}
		if err != nil {
			return err
		}
		for _, c := range claims {
			e := d.Evidence{ID: d.ID(), ObservationID: id, JobID: o.JobID, Claim: c, AnalysisVersion: version, CreatedAt: time.Now().UTC()}
			if _, err = tx.ExecContext(ctx, "INSERT INTO evidence(id,observation_id,job_id,body) VALUES(?,?,?,?)", e.ID, id, o.JobID, d.JSON(e)); err != nil {
				return err
			}
		}
		return reconcileAnalysis(ctx, tx, o, version, generation)
	})
}
func (s *Store) Assess(ctx context.Context, taskID, jobID string) error {
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		j, err := One[d.Job](ctx, tx, "SELECT body FROM jobs WHERE id=? FOR UPDATE", jobID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO completed_tasks(id,completed_at) VALUES(?,?)", taskID, time.Now().UTC())
		if duplicate(err) {
			return nil
		}
		if err != nil {
			return err
		}
		os, err := Many[d.Observation](ctx, tx, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at,id", jobID)
		if err != nil {
			return err
		}
		es, err := Many[d.Evidence](ctx, tx, "SELECT body FROM evidence WHERE job_id=?", jobID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		a := rules.Status(jobID, os, es, now)
		j.CurrentStatus = a.Status
		for _, o := range os {
			if o.ObservedAt.After(j.UpdatedAt) {
				j.UpdatedAt = o.ObservedAt
			}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assessments(id,job_id,assessed_at,body) VALUES(?,?,?,?)", a.ID, jobID, a.AssessedAt, d.JSON(a))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE jobs SET body=? WHERE id=?", d.JSON(j), jobID)
		if err != nil {
			return err
		}
		previous := map[string]d.Observation{}
		for _, o := range os {
			if o.FetchStatus != "SUCCESS" {
				continue
			}
			old := previous[o.PostingID]
			get := func(id string) []d.Evidence {
				v := []d.Evidence{}
				for _, e := range es {
					if e.ObservationID == id {
						v = append(v, e)
					}
				}
				return v
			}
			changes := []d.Change{}
			if old.ExtractionStatus == "COMPLETE" && o.ExtractionStatus == "COMPLETE" {
				changes = rules.Changes(old, o, get(old.ID), get(o.ID))
			}
			for _, ch := range changes {
				_, err = tx.ExecContext(ctx, "INSERT INTO changes(id,job_id,change_key,body) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE id=id", ch.ID, jobID, ch.From+":"+ch.To+":"+ch.Type, d.JSON(ch))
				if err != nil {
					return err
				}
			}
			previous[o.PostingID] = o
		}
		return nil
	})
	if err != nil {
		return err
	}
	profiles, err := Many[d.Profile](ctx, s.DB, "SELECT body FROM profiles")
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if CheckJobAccess(ctx, s.DB, profile.UserID, jobID) != nil {
			continue
		}
		v, e := s.ComputeEvaluation(ctx, profile.UserID, jobID)
		if e != nil {
			return e
		}
		if e = s.PersistAssessment(ctx, v); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) AnalysisTaskFailed(ctx context.Context, t Task, category string) error {
	var generation uint64
	err := s.DB.QueryRowContext(ctx, "SELECT generation FROM analysis_task_generations WHERE task_id=? AND observation_id=?", t.ID, t.EntityID).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.analysisFailed(ctx, t.EntityID, category, generation)
}
func (s *Store) AnalysisFailed(ctx context.Context, id, category string) error {
	return s.analysisFailed(ctx, id, category, 0)
}
func (s *Store) analysisFailed(ctx context.Context, id, category string, generation uint64) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		o, err := lockAnalysis(ctx, tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if (generation != 0 && generation != o.ActiveAnalysisGeneration) || o.ExtractionStatus == "COMPLETE" || o.ExtractionStatus == "FAILED" {
			return nil
		}
		o.ExtractionStatus = "FAILED"
		o.ErrorCategory = category
		_, err = tx.ExecContext(ctx, "UPDATE observations SET body=? WHERE id=?", d.JSON(o), id)
		if err != nil {
			return err
		}
		return Outbox(ctx, tx, NewTask("ASSESS", o.JobID))
	})
}

// Hourly freshness assessment uses stored observations; it never silently fetches
// a recruiting site. Dedup key makes competing schedulers harmless.
func (s *Store) EnqueueFreshness(ctx context.Context) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var exists string
		bucket := "freshness:" + time.Now().UTC().Format("2006010215")
		err := tx.QueryRowContext(ctx, "SELECT id FROM completed_tasks WHERE id=?", bucket).Scan(&exists)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO completed_tasks(id,completed_at) VALUES(?,?)", bucket, time.Now().UTC())
		if duplicate(err) {
			return nil
		}
		if err != nil {
			return err
		}
		jobs, err := Many[d.Job](ctx, tx, "SELECT body FROM jobs")
		if err != nil {
			return err
		}
		for _, j := range jobs {
			if err = Outbox(ctx, tx, NewTask("ASSESS", j.ID)); err != nil {
				return err
			}
		}
		return nil
	})
}

// SourceKey preserves case-sensitive external IDs, paths and queries. It does
// not fold slashes, remove query parameters or decode escaped path segments.
func SourceKey(i Ingest) string {
	kind, value := "external", i.ExternalID
	if value == "" {
		kind, value = "url", i.URL
		if value != "" {
			if u, err := url.Parse(value); err == nil {
				u.Scheme = strings.ToLower(u.Scheme)
				u.Host = strings.ToLower(u.Host)
				value = u.String()
			}
		} else {
			kind, value = "manual", rules.Fingerprint(i.Company, i.Title, i.JobType, i.Locations, i.Text)
		}
	}
	return d.Hash(d.JSON([]string{"source-v2", i.SourceID, kind, value}))
}
