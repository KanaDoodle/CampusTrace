package persistence

import (
	"context"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
)

type DecisionCompany struct {
	Company string `json:"company"`
	Total   int    `json:"total"`
}

type DecisionJobContext struct {
	JobID         string            `json:"job_id"`
	CurrentStatus string            `json:"current_status"`
	Deadline      *time.Time        `json:"deadline,omitempty"`
	DeadlineDate  string            `json:"deadline_date,omitempty"`
	OfficialURL   string            `json:"official_url,omitempty"`
	Application   *MatchApplication `json:"application,omitempty"`
	CampaignID    string            `json:"campaign_id,omitempty"`
}

type CompanyWorkflow struct {
	AsOf         time.Time             `json:"as_of"`
	Campaigns    []CampaignView        `json:"campaigns"`
	Applications []DecisionApplication `json:"applications"`
	Jobs         []DecisionJobContext  `json:"jobs"`
}

type DecisionApplication struct {
	MatchApplication
	Job *ApplicationJob `json:"job"`
}

// The small company catalog remains usable before a profile is completed and
// lets large employers be narrowed before the 200-job decision snapshot limit.
func (s *Store) DecisionCompanies(ctx context.Context, user string) ([]DecisionCompany, error) {
	return Many[DecisionCompany](ctx, s.DB, `SELECT JSON_OBJECT('company',company_name,'total',COUNT(*)) FROM jobs
 WHERE visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?) GROUP BY company_name ORDER BY company_name LIMIT 1001`, user)
}

func (s *Store) DecisionCatalog(ctx context.Context, user, company string) ([]ApplicationJob, error) {
	rows, err := Many[ApplicationJob](ctx, s.DB, `SELECT body FROM jobs WHERE company_name=? AND
 (visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?)) ORDER BY id LIMIT 10001`, company, user)
	if err == nil && len(rows) > 10000 {
		return nil, matching.ErrDecisionCapacity
	}
	for i := range rows {
		rows[i].Locations = d.CanonicalCities(rows[i].Locations)
	}
	return rows, err
}

// Called inside the same repeatable-read transaction as the candidate, JDs and
// individual analyses. A subset does not reset campaign usage: the whole rule
// and all visible company applications still participate in this context.
func companyWorkflow(ctx context.Context, q Queryer, user, company string, jobs []d.Job, now time.Time) (*CompanyWorkflow, error) {
	v := &CompanyWorkflow{AsOf: now, Campaigns: []CampaignView{}, Applications: []DecisionApplication{}, Jobs: []DecisionJobContext{}}
	rulesForCompany, err := Many[ApplicationCampaign](ctx, q, `SELECT body FROM application_campaigns WHERE user_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.company'))=? ORDER BY id`, user, company)
	if err != nil {
		return nil, err
	}
	byCampaign := map[string]string{}
	for _, rule := range rulesForCompany {
		planned, submitted, e := campaignUsage(ctx, q, user, rule.ID)
		if e != nil {
			return nil, e
		}
		remaining := max(0, rule.Limit-planned-submitted)
		v.Campaigns = append(v.Campaigns, CampaignView{rule, planned, submitted, remaining, planned+submitted > rule.Limit})
		for _, id := range rule.JobIDs {
			byCampaign[id] = rule.ID
		}
	}
	apps, err := Many[DecisionApplication](ctx, q, `SELECT JSON_OBJECT('id',a.id,'job_id',a.job_id,'current_state',JSON_EXTRACT(a.body,'$.current_state'),'version',a.version,'applied_at',JSON_EXTRACT(a.body,'$.applied_at'),'job',j.body) FROM applications a JOIN jobs j ON j.id=a.job_id WHERE a.user_id=? AND j.company_name=? AND
 (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?)) ORDER BY a.id LIMIT 10001`, user, company, user)
	if err != nil {
		return nil, err
	}
	if len(apps) > 10000 {
		return nil, matching.ErrDecisionCapacity
	}
	byApp := map[string]*MatchApplication{}
	for _, a := range apps {
		row := a.MatchApplication
		v.Applications = append(v.Applications, a)
		byApp[a.JobID] = &row
	}
	if len(jobs) == 0 {
		return v, nil
	}
	slots := []string{}
	args := []any{}
	for _, job := range jobs {
		slots = append(slots, "?")
		args = append(args, job.ID)
	}
	observations, err := Many[d.Observation](ctx, q, "SELECT body FROM observations WHERE job_id IN ("+strings.Join(slots, ",")+") ORDER BY observed_at,id", args...)
	if err != nil {
		return nil, err
	}
	evidence, err := Many[d.Evidence](ctx, q, "SELECT body FROM evidence WHERE job_id IN ("+strings.Join(slots, ",")+") ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	os := map[string][]d.Observation{}
	es := map[string][]d.Evidence{}
	for _, o := range observations {
		os[o.JobID] = append(os[o.JobID], o)
	}
	for _, e := range evidence {
		es[e.JobID] = append(es[e.JobID], e)
	}
	for _, job := range jobs {
		link, e := officialJobURL(ctx, q, user, job.ID)
		if e != nil {
			return nil, e
		}
		deadline, date := decisionDeadline(os[job.ID], es[job.ID], now)
		v.Jobs = append(v.Jobs, DecisionJobContext{JobID: job.ID, CurrentStatus: rules.Status(job.ID, os[job.ID], es[job.ID], now).Status, Deadline: deadline, DeadlineDate: date, OfficialURL: link, Application: byApp[job.ID], CampaignID: byCampaign[job.ID]})
	}
	return v, nil
}

func decisionDeadline(os []d.Observation, es []d.Evidence, now time.Time) (*time.Time, string) {
	current, _ := rules.Current(os, es)
	usable := []d.Observation{}
	for _, o := range current {
		if !o.ObservedAt.After(now.Add(time.Minute)) {
			usable = append(usable, o)
		}
	}
	deadline, _ := radarDeadline(usable, es, now)
	return deadline, decisionDeadlineDate(usable, es, now, deadline)
}

// Keep date-only source wording: its exclusive next-day instant is for judging
// closure, but displaying that instant would suggest an extra application day.
func decisionDeadlineDate(os []d.Observation, es []d.Evidence, now time.Time, deadline *time.Time) string {
	if deadline == nil {
		return ""
	}
	current, active := rules.Current(os, es)
	for _, o := range current {
		if o.FetchStatus != "SUCCESS" || o.ExtractionStatus != "COMPLETE" || now.Sub(o.ObservedAt) > 7*24*time.Hour || o.ObservedAt.After(now.Add(time.Minute)) {
			continue
		}
		for _, e := range active {
			if e.ObservationID != o.ID || e.Type != "DEADLINE" || e.Confidence < 0.8 {
				continue
			}
			if _, err := time.Parse("2006-01-02", e.Value); err != nil {
				continue
			}
			if at, err := d.DeadlineInstant(e.Value, o.Timezone); err == nil && at.Equal(*deadline) {
				return e.Value
			}
		}
	}
	return ""
}
