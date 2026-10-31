package integration

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"testing"
	"time"
)

func TestKafkaTaskExpiredBeforeExecutionDoesNotCallModel(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Expiry", Title: "Go", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go", FetchStatus: "SUCCESS"})
	must(t, e)
	_, e = s.DB.ExecContext(ctx, "UPDATE outbox SET sent=TRUE")
	must(t, e)
	task := p.NewTask("ANALYZE", o.ID)
	task.OwnerID = u
	task, e = s.BindAnalysis(ctx, task, d.ProcessingVersion("", d.ParserVersion, o.ParserVersion))
	must(t, e)
	task.QueueUntil = time.Now().Add(-time.Second)
	must(t, q.Publish(ctx, task))
	analyzer := &countAnalysis{}
	w := worker(s, q)
	w.Analyzer = analyzer
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(run) }()
	defer func() { cancel(); must(t, <-done) }()
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		observation, e := s.Observation(ctx, o.ID)
		must(t, e)
		if observation.ErrorCategory == "QUEUE_EXPIRED" {
			if analyzer.calls.Load() != 0 {
				t.Fatal("expired task called model")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expired task not settled")
}
