package integration

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/config"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/eventbus"
	"github.com/KanaDoodle/CampusTrace/internal/jobevent"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"github.com/KanaDoodle/CampusTrace/internal/searchservice"
	"github.com/KanaDoodle/CampusTrace/internal/testsupport/sqlfixture"
)

// Kafka's event history and consumer receipts need a whole owned schema. This
// wrapper also works with the original shared-schema fixture on a clean checkout;
// other integration suites need not opt into the newer isolation fixture.
func kafkaSetup(t *testing.T) (context.Context, *p.Store, *pipeline.Queue, string, string) {
	t.Helper()
	if os.Getenv("CAMPUS_INTEGRATION") != "1" || os.Getenv("MYSQL_TEST_ADMIN_DSN") == "" {
		t.Skip("requires isolated MySQL; use make kafka-test")
	}
	dsn := sqlfixture.Database(t, config.Env("MYSQL_TEST_DSN", config.Load().DSN), "integration")
	t.Setenv("MYSQL_TEST_DSN", dsn)
	return setup(t)
}

func kafkaConfig(t *testing.T) eventbus.Config {
	t.Helper()
	if os.Getenv("KAFKA_TEST_BROKERS") == "" || os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable Kafka and OpenSearch")
	}
	topic := "test-jobs-" + d.ID()
	cfg, e := eventbus.NewConfig(os.Getenv("KAFKA_TEST_BROKERS"), topic, topic+"-dlq", "")
	must(t, e)
	return cfg
}
func kafkaWait(t *testing.T, ctx context.Context, label string, check func() bool) {
	t.Helper()
	until := time.Now().Add(25 * time.Second)
	for time.Now().Before(until) && ctx.Err() == nil {
		if check() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(label)
}
func kafkaConsumer(t *testing.T, parent context.Context, cfg eventbus.Config, handler func(context.Context, []jobevent.Event) error) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	consumer, e := eventbus.NewConsumer(cfg, nil)
	must(t, e)
	var once sync.Once
	done := make(chan struct{})
	go func() { defer close(done); defer consumer.Close(); _ = consumer.Run(ctx, handler) }()
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

type lostEventAck struct{ target *eventbus.Producer }

func (publisher lostEventAck) Publish(ctx context.Context, events []jobevent.Event) error {
	if e := publisher.target.Publish(ctx, events); e != nil {
		return e
	}
	return errors.New("broker reply lost before SQL acknowledgement")
}
func TestKafkaSearchAndNotificationsIndependentRecovery(t *testing.T) {
	cfg := kafkaConfig(t)
	ctx, s, _, owner, _ := kafkaSetup(t)
	s.JobEvents = true
	producer, e := eventbus.NewProducer(cfg)
	must(t, e)
	t.Cleanup(producer.Close)
	must(t, producer.EnsureTopics(ctx))
	alias := "test-kafka-" + d.ID()
	backend, e := searchservice.NewBackend(os.Getenv("OPENSEARCH_TEST_URL"), alias)
	must(t, e)
	server, e := searchservice.NewServer(backend, searchQueryToken, searchWriteToken)
	must(t, e)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	t.Cleanup(func() {
		request, _ := http.NewRequest("DELETE", os.Getenv("OPENSEARCH_TEST_URL")+"/"+alias+"-*", nil)
		if response, e := http.DefaultClient.Do(request); e == nil {
			response.Body.Close()
		}
	})
	client, e := a.NewClient(httpServer.URL, searchQueryToken, searchWriteToken)
	must(t, e)
	s.Search = &eventbus.SearchTransport{HTTP: client, Producer: producer}
	cfg.Group = cfg.Topic + "-notify"
	stopNotify := kafkaConsumer(t, ctx, cfg, s.ConsumeNotificationEvents)
	input := p.Ingest{Company: "Synthetic Kafka " + d.ID(), Title: "服务器工程师", JobType: "FULL_TIME", Locations: []string{"上海"}, ExternalID: d.ID(), FetchStatus: "SUCCESS", Text: "Golang Redis firstmarker", ObservedAt: time.Now().UTC()}
	observation, e := s.IngestForUser(ctx, owner, input)
	must(t, e)
	_, e = s.PublishJobEventsOnce(ctx, lostEventAck{producer})
	if e == nil {
		t.Fatal("missing producer fault")
	}
	var pending int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_event_outbox WHERE published_at IS NULL").Scan(&pending))
	if pending != 1 {
		t.Fatal("lost reply removed durable event")
	}
	_, e = s.PublishJobEventsOnce(ctx, producer)
	must(t, e)
	events, e := p.Many[jobevent.Event](ctx, s.DB, "SELECT body FROM job_event_outbox ORDER BY event_version")
	must(t, e)
	if len(events) != 1 {
		t.Fatal("missing atomic event", len(events))
	}
	// No Search consumer is running: a broker ACK cannot clear this dirty row.
	e = s.SyncSearchOnce(ctx)
	if e != nil && !errors.Is(e, a.ErrPending) {
		t.Fatal(e)
	}
	var dirty int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_outbox").Scan(&dirty))
	if dirty == 0 {
		t.Fatal("broker ACK incorrectly acknowledged an unapplied index")
	}
	kafkaWait(t, ctx, "notification stalled behind Search", func() bool { rows, e := s.Notifications(ctx, owner); must(t, e); return len(rows) == 1 })
	cfg.Group = cfg.Topic + "-search"
	kafkaConsumer(t, ctx, cfg, server.ConsumeEvents)
	syncIndex := func(word string) bool {
		_, e := s.PublishJobEventsOnce(ctx, producer)
		must(t, e)
		e = s.SyncSearchOnce(ctx)
		if e != nil && !errors.Is(e, a.ErrPending) {
			t.Fatal(e)
		}
		result, e := s.SearchJobs(ctx, owner, a.Query{Text: word, Company: input.Company})
		must(t, e)
		return result.Mode == "fulltext" && len(result.IDs) == 1 && result.IDs[0] == observation.JobID
	}
	kafkaWait(t, ctx, "index did not catch up", func() bool { return syncIndex("firstmarker") })
	stopNotify()
	input.ObservedAt = input.ObservedAt.Add(time.Second)
	input.Text = "Golang Redis secondmarker"
	_, e = s.IngestForUser(ctx, owner, input)
	must(t, e)
	kafkaWait(t, ctx, "Search stopped behind notifications", func() bool { return syncIndex("secondmarker") })
	rows, e := s.Notifications(ctx, owner)
	must(t, e)
	if len(rows) != 1 {
		t.Fatal("stopped notification consumer processed an event")
	}
	cfg.Group = cfg.Topic + "-notify"
	kafkaConsumer(t, ctx, cfg, s.ConsumeNotificationEvents)
	kafkaWait(t, ctx, "notification resume lost update", func() bool { rows, e := s.Notifications(ctx, owner); must(t, e); return len(rows) == 2 })
	events, e = p.Many[jobevent.Event](ctx, s.DB, "SELECT body FROM job_event_outbox ORDER BY event_version")
	must(t, e)
	if len(events) != 2 {
		t.Fatal("immutable history missing", len(events))
	}
	must(t, producer.Publish(ctx, events)) // Duplicate/older events must be harmless.
	var latestSeen atomic.Bool
	cfg.Group = cfg.Topic + "-notify-replay"
	kafkaConsumer(t, ctx, cfg, func(ctx context.Context, batch []jobevent.Event) error {
		if e := s.ConsumeNotificationEvents(ctx, batch); e != nil {
			return e
		}
		for _, event := range batch {
			if event.ID == events[1].ID {
				latestSeen.Store(true)
			}
		}
		return nil
	})
	kafkaWait(t, ctx, "replay did not reach latest event", latestSeen.Load)
	rows, e = s.Notifications(ctx, owner)
	must(t, e)
	if len(rows) != 2 {
		t.Fatal("replay duplicated notifications", len(rows))
	}
	outsider, e := s.NewUser(ctx, d.ID()+"@kafka.invalid", "unused")
	must(t, e)
	rows, e = s.Notifications(ctx, outsider)
	must(t, e)
	if len(rows) != 0 {
		t.Fatal("private job notification leaked")
	}
	// A new index generation replays snapshots, which are not business changes.
	_, e = client.Rebuild(ctx)
	must(t, e)
	kafkaWait(t, ctx, "rebuild failed", func() bool { return syncIndex("secondmarker") })
	rows, e = s.Notifications(ctx, owner)
	must(t, e)
	if len(rows) != 2 {
		t.Fatal("snapshot replay generated notifications")
	}
	// Simulate an operator deletion, then verify the reconciled tombstone travels
	// through Kafka and that replaying an older real event cannot resurrect it.
	must(t, s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "DELETE FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id')) IN (SELECT id FROM observations WHERE job_id=?) OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id'))=?", observation.JobID, observation.JobID); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM agent_events WHERE job_id=?", observation.JobID); e != nil {
			return e
		}
		for _, query := range []string{"DELETE FROM job_assessment_schedule WHERE job_id=?", "DELETE FROM observations WHERE job_id=?", "DELETE FROM postings WHERE job_id=?", "DELETE FROM jobs WHERE id=?"} {
			if _, e := tx.ExecContext(ctx, query, observation.JobID); e != nil {
				return e
			}
		}
		return nil
	}))
	_, e = s.DB.ExecContext(ctx, "UPDATE search_revision_state SET next_reconcile_at=NULL WHERE id='jobs'")
	must(t, e)
	kafkaWait(t, ctx, "Kafka tombstone did not converge", func() bool {
		e = s.SyncSearchOnce(ctx)
		if e != nil && !errors.Is(e, a.ErrPending) {
			t.Fatal(e)
		}
		_, e = s.PublishJobEventsOnce(ctx, producer)
		must(t, e)
		tombstone, e := p.One[a.Document](ctx, s.DB, "SELECT body FROM search_documents WHERE job_id=?", observation.JobID)
		must(t, e)
		out, e := s.SearchJobs(ctx, owner, a.Query{Text: "secondmarker", Company: input.Company})
		must(t, e)
		return tombstone.Deleted && out.Mode == "fulltext" && len(out.IDs) == 0
	})
	must(t, producer.Publish(ctx, events))
	// A fresh verifier group observes all older events and the delete. Its read
	// runs after the real Search consumer has applied the retained tombstone.
	var deleted atomic.Bool
	cfg.Group = cfg.Topic + "-delete-verifier"
	kafkaConsumer(t, ctx, cfg, func(ctx context.Context, batch []jobevent.Event) error {
		if e := server.ConsumeEvents(ctx, batch); e != nil {
			return e
		}
		for _, event := range batch {
			if event.Kind == "deleted" {
				deleted.Store(true)
			}
		}
		return nil
	})
	kafkaWait(t, ctx, "delete event not observed", deleted.Load)
	status, e := client.Status(ctx)
	must(t, e)
	out, e := client.Query(ctx, a.Query{Generation: status.Generation, Dataset: events[0].Document.Dataset, Text: "secondmarker", User: owner, Limit: 10})
	must(t, e)
	if len(out.Hits) != 0 {
		t.Fatal("old event resurrected deleted document")
	}
}
func TestKafkaOutboxRollsBackWithBusinessWrite(t *testing.T) {
	if os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("requires isolated MySQL")
	}
	ctx, s, _, owner, _ := kafkaSetup(t)
	s.JobEvents = true
	_, e := s.DB.ExecContext(ctx, "RENAME TABLE job_event_outbox TO unavailable_job_events")
	must(t, e)
	t.Cleanup(func() { s.DB.Exec("RENAME TABLE unavailable_job_events TO job_event_outbox") })
	_, e = s.IngestForUser(ctx, owner, p.Ingest{Company: "Rollback", Title: "Go", JobType: "FULL_TIME", Text: "Golang Redis", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	if e == nil {
		t.Fatal("missing event journal did not fail transaction")
	}
	for _, table := range []string{"jobs", "observations", "search_documents", "search_outbox", "outbox"} {
		var n int
		must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n))
		if n != 0 {
			t.Fatal("partial business commit", table, n)
		}
	}
}

func TestKafkaNotificationAudienceAndCurrentStatus(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable OpenSearch and MySQL")
	}
	ctx, s, _, owner, source := kafkaSetup(t)
	s.JobEvents = true
	searchFixture(t, s)
	user := func() string { id, e := s.NewUser(ctx, d.ID()+"@events.invalid", "unused"); must(t, e); return id }
	saved, watching, ignored, outsider := user(), user(), user(), user()
	radarWatch(t, ctx, s, watching, source)
	radarWatch(t, ctx, s, ignored, source)
	input := ingest(t, ctx, s, source)
	input.Text = "Golang Redis synthetic event audience"
	observation, e := s.Ingest(ctx, input)
	must(t, e)
	must(t, s.SetPreference(ctx, saved, observation.JobID, "SAVED"))
	must(t, s.SetPreference(ctx, ignored, observation.JobID, "IGNORED"))
	readEvents := func() []jobevent.Event {
		events, e := p.Many[jobevent.Event](ctx, s.DB, "SELECT body FROM job_event_outbox ORDER BY event_version")
		must(t, e)
		return events
	}
	assertCount := func(user string, expected int) {
		rows, e := s.Notifications(ctx, user)
		must(t, e)
		if len(rows) != expected {
			t.Fatalf("notification audience count %d != %d", len(rows), expected)
		}
	}
	original := readEvents()
	if len(original) != 1 {
		t.Fatal("missing initial event")
	}
	must(t, s.ConsumeNotificationEvents(ctx, original))
	for _, recipient := range []string{saved, watching} {
		assertCount(recipient, 1)
	}
	for _, excluded := range []string{owner, ignored, outsider} {
		assertCount(excluded, 0)
	}
	// Reconciliation models an operator change. Keep status, authoritative ACL,
	// and JSON aligned; the emitted event uses the same production projection.
	_, e = s.DB.ExecContext(ctx, "UPDATE jobs SET body=JSON_SET(body,'$.current_status','CLOSED') WHERE id=?", observation.JobID)
	must(t, e)
	_, e = s.DB.ExecContext(ctx, "UPDATE search_revision_state SET next_reconcile_at=NULL WHERE id='jobs'")
	must(t, e)
	must(t, s.SyncSearchOnce(ctx))
	all := readEvents()
	if len(all) != 2 || all[1].Kind != "updated" {
		t.Fatal("status event missing")
	}
	must(t, s.ConsumeNotificationEvents(ctx, all))
	for _, recipient := range []string{saved, watching} {
		assertCount(recipient, 2)
		rows, e := s.Notifications(ctx, recipient)
		must(t, e)
		found := false
		for _, row := range rows {
			if row.Type == "JOB_STATUS_CHANGED" {
				found = true
			}
		}
		if !found {
			t.Fatal("status event did not produce its notification type")
		}

		_, _, e = s.RefreshNotifications(ctx, recipient)
		must(t, e)
		rows, e = s.Notifications(ctx, recipient)
		must(t, e)
		count := 0
		for _, row := range rows {
			if row.Type == "JOB_STATUS_CHANGED" {
				count++
			}
		}
		if count != 1 {
			t.Fatal("periodic scan duplicated Kafka status notification", count)
		}
	}
	// Once the current job becomes private, former public subscribers do not
	// receive either the delayed public event or the private visibility update.
	_, e = s.DB.ExecContext(ctx, "UPDATE jobs SET visibility='PRIVATE',owner_id=?,body=JSON_SET(body,'$.visibility','PRIVATE','$.owner_id',?) WHERE id=?", owner, owner, observation.JobID)
	must(t, e)
	_, e = s.DB.ExecContext(ctx, "UPDATE search_revision_state SET next_reconcile_at=NULL WHERE id='jobs'")
	must(t, e)
	must(t, s.SyncSearchOnce(ctx))
	all = readEvents()
	if len(all) != 3 {
		t.Fatal("visibility event missing")
	}
	must(t, s.ConsumeNotificationEvents(ctx, all))
	assertCount(owner, 1)
	for _, recipient := range []string{saved, watching} {
		assertCount(recipient, 2)
	}
	for _, excluded := range []string{ignored, outsider} {
		assertCount(excluded, 0)
	}
	// A new consumer group still sees SQL receipts, and snapshots are silent.
	must(t, s.ConsumeNotificationEvents(ctx, all))
	snapshot := jobevent.Snapshot(all[len(all)-1].Document, "synthetic-generation", "")
	must(t, s.ConsumeNotificationEvent(ctx, snapshot))
	assertCount(owner, 1)
}
