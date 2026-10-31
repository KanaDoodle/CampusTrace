package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/eventbus"
	"github.com/KanaDoodle/CampusTrace/internal/jobevent"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"github.com/KanaDoodle/CampusTrace/internal/searchservice"
	"github.com/KanaDoodle/CampusTrace/internal/testsupport/sqlfixture"
	"github.com/go-sql-driver/mysql"
)

// Only explicit test-owned containers are eligible. Both accounts are scoped
// to independently allocated schemas, so even a malformed dump cannot alter
// the original source. The fixture contains synthetic data only.
func recoveryDump(t *testing.T, ctx context.Context, dsn string) []byte {
	t.Helper()
	container := os.Getenv("MYSQL_TEST_CONTAINER")
	if !strings.HasPrefix(container, "ct-adversarial-mysql-") {
		t.Fatal("recovery requires the disposable Kafka harness")
	}
	cfg, e := mysql.ParseDSN(dsn)
	must(t, e)
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", "-e", "MYSQL_PWD="+cfg.Passwd, container, "mysqldump", "--single-transaction", "--skip-lock-tables", "--no-tablespaces", "--set-gtid-purged=OFF", "--column-statistics=0", "-u"+cfg.User, cfg.DBName)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if e = cmd.Run(); e != nil {
		t.Fatal("synthetic snapshot failed", e, stderr.String())
	}
	return out.Bytes()
}
func recoveryImport(t *testing.T, ctx context.Context, dsn string, dump []byte) *p.Store {
	t.Helper()
	cfg, e := mysql.ParseDSN(dsn)
	must(t, e)
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", "-e", "MYSQL_PWD="+cfg.Passwd, os.Getenv("MYSQL_TEST_CONTAINER"), "mysql", "--binary-mode=1", "--local-infile=0", "-u"+cfg.User, cfg.DBName)
	cmd.Stdin = bytes.NewReader(dump)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Run(); e != nil {
		t.Fatal("synthetic restore failed", e, stderr.String())
	}
	s, e := p.Open(ctx, dsn)
	must(t, e)
	t.Cleanup(func() { s.DB.Close() })
	must(t, s.Migrate(ctx))
	return s
}
func dataset(t *testing.T, ctx context.Context, s *p.Store) string {
	t.Helper()
	var v string
	must(t, s.DB.QueryRowContext(ctx, "SELECT dataset FROM search_revision_state WHERE id='jobs'").Scan(&v))
	return v
}
func recoveryDerived(name string) bool {
	switch name {
	case "search_rebuild_state", "search_rebuild_deltas", "search_documents", "search_outbox", "search_revision_state", "search_sync_state", "job_dataset_recovery":
		return true
	}
	return false
}

func TestKafkaRecoveryEpochIgnoresRetainedFutureEvents(t *testing.T) {
	cfg := kafkaConfig(t)
	ctx, source, _, owner, _ := kafkaSetup(t)
	source.JobEvents = true
	producer, e := eventbus.NewProducer(cfg)
	must(t, e)
	t.Cleanup(producer.Close)
	must(t, producer.EnsureTopics(ctx))
	alias := "test-recovery-" + d.ID()
	backend, e := searchservice.NewBackend(os.Getenv("OPENSEARCH_TEST_URL"), alias)
	must(t, e)
	server, e := searchservice.NewServer(backend, searchQueryToken, searchWriteToken)
	must(t, e)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	t.Cleanup(func() {
		req, _ := http.NewRequest("DELETE", os.Getenv("OPENSEARCH_TEST_URL")+"/"+alias+"-*", nil)
		if res, e := http.DefaultClient.Do(req); e == nil {
			res.Body.Close()
		}
	})
	client, e := a.NewClient(httpServer.URL, searchQueryToken, searchWriteToken)
	must(t, e)
	transport := &eventbus.SearchTransport{HTTP: client, Producer: producer}
	source.Search = transport
	cfg.Group = cfg.Topic + "-search"
	kafkaConsumer(t, ctx, cfg, server.ConsumeEvents)
	cfg.Group = cfg.Topic + "-notify"
	stopNotify := kafkaConsumer(t, ctx, cfg, source.ConsumeNotificationEvents)
	input := p.Ingest{Company: "Synthetic Recovery " + d.ID(), Title: "后端工程师", JobType: "FULL_TIME", Locations: []string{"上海"}, ExternalID: d.ID(), FetchStatus: "SUCCESS", Text: "Golang beforebackupmark", ObservedAt: time.Now().UTC()}
	obs, e := source.IngestForUser(ctx, owner, input)
	must(t, e)
	syncIndex := func(s *p.Store, word string) bool {
		_, e := s.PublishJobEventsOnce(ctx, producer)
		must(t, e)
		e = s.SyncSearchOnce(ctx)
		// Startup or alias replacement can race a real consumer. The worker
		// retries this conflict too; convergence must still occur within kafkaWait.
		if errors.Is(e, a.ErrConflict) {
			t.Log("retrying a transient Search generation conflict")
			return false
		}
		if e != nil && !errors.Is(e, a.ErrPending) {
			t.Fatal(e)
		}
		out, e := s.SearchJobs(ctx, owner, a.Query{Text: word, Company: input.Company})
		must(t, e)
		return out.Mode == "fulltext" && len(out.IDs) == 1 && out.IDs[0] == obs.JobID
	}
	kafkaWait(t, ctx, "initial source index", func() bool { return syncIndex(source, "beforebackupmark") })
	kafkaWait(t, ctx, "initial source notification", func() bool { rows, e := source.Notifications(ctx, owner); must(t, e); return len(rows) == 1 })
	stopNotify()
	input.ObservedAt = input.ObservedAt.Add(time.Second)
	input.Text = "Golang backupmark"
	_, e = source.IngestForUser(ctx, owner, input)
	must(t, e)
	before, e := source.ProofDatabase(ctx)
	must(t, e)
	sourceDSN := os.Getenv("MYSQL_TEST_DSN")
	dump := recoveryDump(t, ctx, sourceDSN) // t0: v2 is still pending in the SQL outbox.
	oldDataset := dataset(t, ctx, source)
	input.ObservedAt = input.ObservedAt.Add(time.Second)
	input.Text = "Golang futuremark"
	_, e = source.IngestForUser(ctx, owner, input)
	must(t, e)
	future := input
	future.ExternalID = d.ID()
	future.Title = "Future only"
	future.Text = "Golang futureonlymark"
	_, e = source.IngestForUser(ctx, owner, future)
	must(t, e)
	kafkaWait(t, ctx, "future source index", func() bool { return syncIndex(source, "futuremark") })
	retained, e := p.Many[jobevent.Event](ctx, source.DB, "SELECT body FROM job_event_outbox ORDER BY event_version")
	must(t, e)
	if len(retained) != 4 {
		t.Fatal("expected four real source changes", len(retained))
	}
	sourceFuture, e := source.ProofDatabase(ctx)
	must(t, e)
	restoredDSN := sqlfixture.RecoveryDatabase(t, sourceDSN, "restored-copy")
	restored := recoveryImport(t, ctx, restoredDSN, dump)
	restored.JobEvents = true
	restored.Search = transport
	copied, e := restored.ProofDatabase(ctx)
	must(t, e)
	if !reflect.DeepEqual(before.Tables, copied.Tables) {
		t.Fatal("real SQL roundtrip changed snapshot")
	}
	changes, e := restored.PrepareRecovery(ctx)
	must(t, e)
	newDataset := dataset(t, ctx, restored)
	if newDataset == oldDataset || changes["rotated_job_dataset"] != 1 || changes["retired_job_events"] != 1 {
		t.Fatal("namespace not retired", changes)
	}
	after, e := restored.ProofDatabase(ctx)
	must(t, e)
	for name, want := range before.Tables {
		if !recoveryDerived(name) && after.Tables[name] != want {
			t.Fatal("recovery changed authoritative table", name)
		}
	}
	n, e := restored.PublishJobEventsOnce(ctx, producer)
	must(t, e)
	if n != 0 {
		t.Fatal("published a pending event from the retired timeline")
	}
	n, _, e = restored.ReplayPublishedJobEvents(ctx, 0, 100)
	must(t, e)
	if n != 0 {
		t.Fatal("reopened retired events")
	}
	// Resume the original group against the restored copy: Kafka offsets retain
	// the future timeline. A verifier group also exercises all retained records.
	cfg.Group = cfg.Topic + "-notify"
	kafkaConsumer(t, ctx, cfg, restored.ConsumeNotificationEvents)
	var oldSeen atomic.Int64
	cfg.Group = cfg.Topic + "-restored-verifier"
	kafkaConsumer(t, ctx, cfg, func(ctx context.Context, batch []jobevent.Event) error {
		if e := restored.ConsumeNotificationEvents(ctx, batch); e != nil {
			return e
		}
		for _, event := range batch {
			if event.Document.Dataset == oldDataset && event.Kind != "snapshot" {
				oldSeen.Add(1)
			}
		}
		return nil
	})
	must(t, producer.Publish(ctx, retained))
	kafkaWait(t, ctx, "restored search baseline", func() bool { return syncIndex(restored, "backupmark") })
	kafkaWait(t, ctx, "retained events not exercised", func() bool { return oldSeen.Load() >= int64(len(retained)) })
	for _, word := range []string{"futuremark", "futureonlymark"} {
		out, e := restored.SearchJobs(ctx, owner, a.Query{Text: word, Company: input.Company})
		must(t, e)
		if out.Mode != "fulltext" || len(out.IDs) != 0 {
			t.Fatal("old index contaminated restored namespace", word, out)
		}
	}
	rows, e := restored.Notifications(ctx, owner)
	must(t, e)
	if len(rows) != 1 {
		t.Fatal("retained events or baseline generated notifications", len(rows))
	}
	var activeEvents int
	must(t, restored.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_event_outbox WHERE dataset=?", newDataset).Scan(&activeEvents))
	if activeEvents != 0 {
		t.Fatal("baseline created business events")
	}
	input.ObservedAt = input.ObservedAt.Add(time.Second)
	input.Text = "Golang afterrestoremark"
	_, e = restored.IngestForUser(ctx, owner, input)
	must(t, e)
	kafkaWait(t, ctx, "post-restore real change", func() bool { return syncIndex(restored, "afterrestoremark") })
	kafkaWait(t, ctx, "post-restore notification", func() bool { rows, e := restored.Notifications(ctx, owner); must(t, e); return len(rows) == 2 })
	changes, e = restored.PrepareRecovery(ctx)
	must(t, e)
	if changes["rotated_job_dataset"] != 0 || dataset(t, ctx, restored) != newDataset {
		t.Fatal("prepare was not idempotent", changes)
	}
	current, e := source.ProofDatabase(ctx)
	must(t, e)
	if !reflect.DeepEqual(sourceFuture.Tables, current.Tables) {
		t.Fatal("restore changed original source")
	}
	evidence := map[string]any{"synthetic": true, "real_mysqldump_import": true, "source_dataset": oldDataset, "restored_dataset": newDataset, "retained_business_events": len(retained), "retired_pending_events": 1, "baseline_business_events": activeEvents, "notifications_before": 1, "notifications_after_real_change": 2, "source_unchanged": true, "snapshot_tables": len(before.Tables), "rpo": "backup snapshot; later source writes deliberately absent"}
	raw, e := json.Marshal(evidence)
	must(t, e)
	t.Log(string(raw))
	if path := os.Getenv("CAMPUS_RECOVERY_EVIDENCE"); path != "" {
		must(t, os.WriteFile(path, append(raw, '\n'), 0600))
	}
}

func TestKafkaRecoveryRotationAtomicAndBusyLock(t *testing.T) {
	_, source, _, _, _ := kafkaSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dsn := sqlfixture.RecoveryDatabase(t, os.Getenv("MYSQL_TEST_DSN"), "atomic-copy")
	s, e := p.Open(ctx, dsn)
	must(t, e)
	t.Cleanup(func() { s.DB.Close() })
	must(t, s.Migrate(ctx))
	owner, e := s.NewUser(ctx, d.ID()+"@recovery.invalid", "unused")
	must(t, e)
	_, e = s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic", Title: "Backend", JobType: "FULL_TIME", ExternalID: d.ID(), FetchStatus: "SUCCESS", Text: "Go"})
	must(t, e)
	baseline, e := s.ProofDatabase(ctx)
	must(t, e)
	conn, e := s.DB.Conn(ctx)
	must(t, e)
	defer conn.Close()
	var name string
	must(t, conn.QueryRowContext(ctx, "SELECT CONCAT('ct-search-sync:',LEFT(SHA2(DATABASE(),256),32))").Scan(&name))
	var held int
	must(t, conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&held))
	if held != 1 {
		t.Fatal("test lock")
	}
	_, e = s.PrepareRecovery(ctx)
	if !errors.Is(e, p.ErrConflict) {
		t.Fatal("rotation did not refuse active sync", e)
	}
	_, e = conn.ExecContext(ctx, "DO RELEASE_LOCK(?)", name)
	must(t, e)
	// Fail after the namespace reset has been attempted, to prove transaction
	// rollback includes the marker, projection rows and revision together.
	_, e = s.DB.ExecContext(ctx, "RENAME TABLE watch_targets TO recovery_unavailable_watch_targets")
	must(t, e)
	_, prepareErr := s.PrepareRecovery(ctx)
	_, e = s.DB.ExecContext(ctx, "RENAME TABLE recovery_unavailable_watch_targets TO watch_targets")
	must(t, e)
	if prepareErr == nil {
		t.Fatal("missing injected failure")
	}
	after, e := s.ProofDatabase(ctx)
	must(t, e)
	if !reflect.DeepEqual(baseline.Tables, after.Tables) {
		t.Fatal("failed prepare left partial recovery changes")
	}
	// Recovery schemas are 52 characters; the previous concatenated lock was 67.
	var oldLength int
	must(t, s.DB.QueryRowContext(ctx, "SELECT LENGTH(CONCAT(DATABASE(),':search-sync-v1'))").Scan(&oldLength))
	if oldLength <= 64 || len(name) > 64 {
		t.Fatal("long-schema fixture does not exercise lock bug", oldLength, len(name))
	}
	var unused int
	if e = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(),':search-sync-v1'),0)").Scan(&unused); e == nil {
		t.Fatal("expected original overlong lock to fail")
	}
	changes, e := s.PrepareRecovery(ctx)
	must(t, e)
	if changes["rotated_job_dataset"] != 1 {
		t.Fatal(changes)
	}
	if dataset(t, ctx, s) == dataset(t, ctx, source) {
		t.Fatal("distinct schema namespace collision")
	}
	t.Logf("atomic rollback and lock fencing passed; original lock=%d bytes, replacement=%d", oldLength, len(name))
}

// The event passes its entrance check and reads the current job, then waits
// at the notification INSERT. Recovery commits before that INSERT unblocks;
// its final epoch check must roll back both notification and receipt.
func TestKafkaRecoveryInFlightNotificationFenced(t *testing.T) {
	_, _, _, _, _ = kafkaSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := sqlfixture.RecoveryDatabase(t, os.Getenv("MYSQL_TEST_DSN"), "inflight-notify")
	s, e := p.Open(ctx, dsn)
	must(t, e)
	t.Cleanup(func() { s.DB.Close() })
	must(t, s.Migrate(ctx))
	s.JobEvents = true
	owner, e := s.NewUser(ctx, d.ID()+"@recovery.invalid", "unused")
	must(t, e)
	obs, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic", Title: "Backend", JobType: "FULL_TIME", ExternalID: d.ID(), FetchStatus: "SUCCESS", Text: "Go"})
	must(t, e)
	event, e := p.One[jobevent.Event](ctx, s.DB, "SELECT body FROM job_event_outbox WHERE job_id=?", obs.JobID)
	must(t, e)
	blocker, e := s.DB.BeginTx(ctx, nil)
	must(t, e)
	defer blocker.Rollback()
	key := d.Hash(d.JSON([]string{"NEW_JOB", "JOB", obs.JobID, "event:" + event.ID}))
	_, e = blocker.ExecContext(ctx, "INSERT INTO notifications(id,user_id,dedup_key,created_at,body) VALUES(?,?,?,UTC_TIMESTAMP(6),'{}')", d.ID(), owner, key)
	must(t, e)
	admin, e := sql.Open("mysql", os.Getenv("MYSQL_TEST_ADMIN_DSN"))
	must(t, e)
	defer admin.Close()
	parsed, e := mysql.ParseDSN(dsn)
	must(t, e)
	done := make(chan error, 1)
	go func() { done <- s.ConsumeNotificationEvent(ctx, event) }()
	kafkaWait(t, ctx, "consumer did not reach its notification write", func() bool {
		var waiting int
		e := admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w
 JOIN performance_schema.data_locks l ON w.BLOCKING_ENGINE_LOCK_ID=l.ENGINE_LOCK_ID
 WHERE l.OBJECT_SCHEMA=? AND l.OBJECT_NAME='notifications'`, parsed.DBName).Scan(&waiting)
		must(t, e)
		return waiting > 0
	})
	changes, e := s.PrepareRecovery(ctx)
	must(t, e)
	if changes["rotated_job_dataset"] != 1 {
		t.Fatal(changes)
	}
	must(t, blocker.Rollback())
	select {
	case e = <-done:
		if !errors.Is(e, p.ErrConflict) {
			t.Fatal("in-flight old notification committed", e)
		}
	case <-ctx.Done():
		t.Fatal("consumer did not finish", ctx.Err())
	}
	rows, e := s.Notifications(ctx, owner)
	must(t, e)
	var receipts int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_notification_receipts").Scan(&receipts))
	if len(rows) != 0 || receipts != 0 {
		t.Fatal("notification side effects survived rollback", len(rows), receipts)
	}
	must(t, s.ConsumeNotificationEvent(ctx, event)) // Its retry is now a foreign epoch.
	t.Log("in-flight old event rolled back at commit; foreign-epoch retry discarded")
}
