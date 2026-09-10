package main

import (
	"encoding/json"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"log"
	"os"
	"time"
)

func main() {
	ctx, cancel := bootstrap.Root()
	defer cancel()
	app, err := bootstrap.Open(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()
	must := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}
	must(app.Store.Migrate(ctx))
	email := "demo@campustrace.local"
	password := "Synthetic-demo-2027"
	user, _, err := app.Store.Credentials(ctx, email)
	if err != nil {
		user, err = (auth.Service{Store: app.Store}).Register(ctx, email, password)
		must(err)
	}
	for _, src := range []d.Source{{ID: "manual", Name: "Manual submission (unverified)", Type: "MANUAL", Trust: "MANUAL"}, {ID: "seed-official", Name: "Synthetic official careers", Type: "OFFICIAL", Trust: "OFFICIAL"}, {ID: "seed-third", Name: "Synthetic third-party board", Type: "THIRD_PARTY", Trust: "THIRD_PARTY"}} {
		var count int
		must(app.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sources WHERE id=?", src.ID).Scan(&count))
		if count == 0 {
			must(app.Store.SaveSource(ctx, src))
		}
	}
	must(app.Store.SaveProfile(ctx, user, d.Profile{GraduationYear: 2027, Degree: "BACHELOR", PreferredTypes: []string{"FULL_TIME"}, PreferredCities: []string{"Shanghai"}, AcceptableCities: []string{"Hangzhou"}, TargetRoles: []string{"backend"}, Skills: []string{"go", "redis", "mysql"}, Languages: []string{"English"}, Majors: []string{"Computer Science"}}))
	var n int
	must(app.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM postings WHERE source_id='seed-official'").Scan(&n))
	if n > 0 {
		fmt.Printf("Synthetic seed already exists. Login: %s / %s\n", email, password)
		return
	}
	now := time.Now().UTC()
	base := "graduation: 2027\ndegree: BACHELOR\njob_type: FULL_TIME\nlocation: Shanghai\nexperience_months: 0\ntech: REQUIRED:go\napply: PRESENT\n"
	jobs := []string{}
	cases := []struct{ company, title, source, text, status string }{{"Synthetic Cedar", "Go backend engineer", "seed-official", base, "SUCCESS"}, {"Synthetic Harbor", "Go platform engineer (closed)", "seed-official", base + "closed: CLOSED\ndeadline: 2020-01-01", "SUCCESS"}, {"Synthetic Maple", "Go backend engineer (blocked)", "seed-official", "", "BLOCKED"}, {"Synthetic Pine", "Go backend engineer (unverified)", "seed-third", base, "SUCCESS"}, {"Synthetic Cedar", "Java backend graduate", "seed-official", "graduation: 2026\ndegree: MASTER\njob_type: FULL_TIME\nlocation: Hangzhou\ntech: REQUIRED:java+spring\napply: PRESENT", "SUCCESS"}, {"Synthetic Harbor", "Backend unknown eligibility", "seed-official", "apply: PRESENT\ntech: go|java", "SUCCESS"}}
	for idx, c := range cases {
		i := p.Ingest{Company: c.company, Title: c.title, JobType: "FULL_TIME", Locations: []string{"Shanghai"}, SourceID: c.source, ExternalID: fmt.Sprintf("synthetic-%d", idx), URL: fmt.Sprintf("https://example.invalid/synthetic/%d", idx), Text: c.text, FetchStatus: c.status, HTTPStatus: 200, ObservedAt: now.Add(-2 * time.Hour)}
		if c.status == "BLOCKED" {
			i.HTTPStatus = 403
		}
		o, err := app.Store.Ingest(ctx, i)
		must(err)
		jobs = append(jobs, o.JobID)
		if idx == 0 {
			i.Text = base + "deadline: 2099-12-31\ntech: redis"
			i.ObservedAt = now.Add(-time.Hour)
			_, err = app.Store.Ingest(ctx, i)
			must(err)
		}
	}
	raw, err := app.Store.ApplyAction(ctx, user, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: jobs[0]})))
	must(err)
	var application d.Application
	must(json.Unmarshal(raw, &application))
	for _, state := range []string{"APPLIED", "OA", "INTERVIEW"} {
		raw, err = app.Store.ApplyAction(ctx, user, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: application.ID, State: state, Version: application.Version, Note: "Synthetic demo timeline"})))
		must(err)
		must(json.Unmarshal(raw, &application))
	}
	interview, err := app.Store.SaveInterview(ctx, user, d.Interview{ApplicationID: application.ID, Round: 1, ScheduledAt: now.Add(-time.Hour), Result: "PASS", Notes: "Synthetic interview"})
	must(err)
	_, err = app.Store.FinishInterview(ctx, user, interview.ID, "PASS", "Synthetic review completed")
	must(err)
	review := d.Review{InterviewID: interview.ID, Questions: []string{"How do Redis Streams recover pending messages?"}, Evaluation: "Need to revisit PEL recovery", Missed: []string{"PEL recovery"}, FollowUp: "Read worker recovery integration tests", Topics: []d.WeakCandidate{{Topic: "Redis Streams", Weight: 4, Evidence: "PEL recovery"}}}
	_, err = app.Store.ApplyAction(ctx, user, d.ID(), "record_interview_review", []byte(d.JSON(review)))
	must(err)
	project, err := app.Store.SaveProject(ctx, user, d.Project{Name: "Synthetic Learning Backend"})
	must(err)
	for _, f := range []d.ProjectFact{{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: "A synthetic exercise implements bounded workers and database idempotency", Verified: true, Reference: "Synthetic example fact, not a user's real project"}, {ProjectID: project.ID, Kind: "LIMITATION", Claim: "No production deployment or real traffic measurements", Verified: true}, {ProjectID: project.ID, Kind: "PLANNED", Claim: "Add semantic embedding provider", Verified: false}} {
		_, err = app.Store.SaveFact(ctx, user, f)
		must(err)
	}
	for _, doc := range []rag.Document{{Title: "Synthetic Go backend handbook", Text: "Redis Streams consumer groups retain unacknowledged messages in the PEL. XAUTOCLAIM transfers idle pending entries. ACK only after transaction commit. Unique database keys make duplicate processing safe. Context cancellation bounds request lifetimes."}, {Title: "MyRPC integration notes", Text: "KanaRPC uses etcd lease registration and round-robin discovery. Timeout cancellation exists on the client. The server supports net/rpc signatures and needs explicit deadline envelopes. This is an educational RPC framework."}, {Title: "Synthetic interview review", Text: "Weak point: Redis Streams PEL recovery and retry ZSET atomic scheduling. Review database unique constraints and failure after persistence before ACK."}} {
		_, err = app.Tools.RAG.Ingest(ctx, user, doc)
		must(err)
	}
	out := map[string]any{"synthetic": true, "user_id": user, "email": email, "password": password, "job_ids": jobs, "application_id": application.ID}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	if os.Getenv("SEED_OUTPUT") != "" {
		must(os.WriteFile(os.Getenv("SEED_OUTPUT"), b, 0600))
	}

}
