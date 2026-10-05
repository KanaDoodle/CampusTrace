package integration

import (
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestFixtureCleanupRemovesJobsBeforeLaterFreshness(t *testing.T) {
	ctx, s, _, owner, source := setup(t)
	retained, err := s.Ingest(ctx, ingest(t, ctx, s, source))
	must(t, err)
	retainedWatch := radarWatch(t, ctx, s, owner, source)
	var removedOwner, removedWatch, deletedWatch string
	var removed []d.Observation
	if !t.Run("finished-catalog", func(t *testing.T) {
		childCtx, child, q, user, src := setup(t)
		removedOwner = user
		late := radarWatch(t, childCtx, child, user, src)
		deletedWatch = late.ID
		reg, err := child.CreateCampusSource(childCtx, user, "kuaishou", "20271779425607", "fixture", d.WatchInput{Enabled: true, CheckInterval: 3600})
		must(t, err)
		removedWatch = reg.Watch.ID
		// Exercise the operator-owned source, the user's manual source, and a
		// registered recruiting source rather than just the original fixture ID.
		for _, sourceID := range []string{src, "", reg.Source.ID} {
			in := ingest(t, childCtx, child, sourceID)
			var observation d.Observation
			if sourceID == src {
				observation, err = child.Ingest(childCtx, in)
			} else {
				observation, err = child.IngestForUser(childCtx, user, in)
			}
			must(t, err)
			removed = append(removed, observation)
			must(t, worker(child, q).Process(childCtx, p.NewTask("ANALYZE", observation.ID)))
			must(t, child.Assess(childCtx, d.ID(), observation.JobID))
			if sourceID == src {
				// Another source can merge into this public job. Its observation
				// tasks must be removed before the shared job is deleted too.
				in.SourceID, in.ExternalID = d.ID(), d.ID()
				must(t, child.SaveSource(childCtx, d.Source{ID: in.SourceID, Name: "second fixture source", Trust: "OFFICIAL"}))
				merged, err := child.Ingest(childCtx, in)
				must(t, err)
				if merged.JobID != observation.JobID {
					t.Fatal("cross-source fixture did not merge")
				}
				removed = append(removed, merged)
			}
		}
		must(t, child.SaveProfile(childCtx, user, d.Profile{GraduationYear: 2027, Degree: "BACHELOR", Skills: []string{"go"}}))
		_, _, _, err = child.Evaluate(childCtx, user, removed[0].JobID)
		must(t, err)
		_, err = child.ScheduleWatches(childCtx, time.Now().UTC(), 100)
		must(t, err)
		must(t, child.DeleteWatch(childCtx, user, deletedWatch))
	}) {
		return
	}
	for _, observation := range removed {
		for _, query := range []string{
			"SELECT COUNT(*) FROM jobs WHERE id=?",
			"SELECT COUNT(*) FROM postings WHERE job_id=?",
			"SELECT COUNT(*) FROM observations WHERE job_id=?",
			"SELECT COUNT(*) FROM assessments WHERE job_id=?",
			"SELECT COUNT(*) FROM evidence WHERE job_id=?",
		} {
			var count int
			must(t, s.DB.QueryRowContext(ctx, query, observation.JobID).Scan(&count))
			if count != 0 {
				t.Fatalf("finished fixture remains: %s count=%d", query, count)
			}
		}
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM watch_targets WHERE user_id=?",
		"SELECT COUNT(*) FROM profiles WHERE user_id=?",
	} {
		var count int
		must(t, s.DB.QueryRowContext(ctx, query, removedOwner).Scan(&count))
		if count != 0 {
			t.Fatalf("finished owner remains: %s count=%d", query, count)
		}
	}
	must(t, s.EnqueueFreshness(ctx))
	for _, observation := range removed {
		var count int
		must(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id')) IN (?,?) OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id')) IN (?,?)`, observation.ID, observation.JobID, removedWatch, deletedWatch).Scan(&count))
		if count != 0 {
			t.Fatal("freshness retained or recreated work for a finished fixture", count)
		}
	}
	// Cleanup must preserve another active fixture's records and pending work.
	_, err = s.Job(ctx, retained.JobID)
	must(t, err)
	_, err = s.Watch(ctx, owner, retainedWatch.ID)
	must(t, err)
	var pending int
	must(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE sent=FALSE AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id'))=?`, retained.ID).Scan(&pending))
	if pending != 1 {
		t.Fatal("cleanup removed another fixture's pending observation", pending)
	}
}
