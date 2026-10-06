package launcher

import (
	"context"
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/go-sql-driver/mysql"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRecoveryRoundTripAndImportIsolation(t *testing.T) {
	if os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("requires exclusively owned Compose MySQL fixture")
	}
	root, err := project("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	m := manager{root: root, out: io.Discard, errOut: io.Discard, execute: command}
	schema := "ct_recovery_fixture_" + d.ID()
	if _, err = m.adminSQL(ctx, "CREATE DATABASE `"+schema+"`; GRANT ALL ON `"+schema+"`.* TO 'campus'@'%';"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if _, e := m.adminSQL(clean, "DROP DATABASE `"+schema+"`;"); e != nil {
			t.Error(e)
		}
	})
	parsed, err := mysql.ParseDSN(os.Getenv("MYSQL_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	parsed.DBName = schema
	store, err := p.Open(ctx, parsed.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := store.NewUser(ctx, d.ID()+"@fixture.invalid", "fixture-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProfile(ctx, user, d.Profile{GraduationYear: 2027, Degree: "MASTER", Skills: []string{"Go", "MySQL"}}); err != nil {
		t.Fatal(err)
	}
	source := d.Source{ID: d.ID(), Name: "fixture", Type: "MANUAL", Trust: "MANUAL", Visibility: "PRIVATE", OwnerID: user, Timezone: "Asia/Shanghai", Adapter: "lever", Tenant: "fixture"}
	if err = store.SaveSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	o, err := store.Ingest(ctx, p.Ingest{SourceID: source.ID, Company: "fixture", Title: "后端", JobType: "FULL_TIME", ExternalID: "fixture", URL: "https://fixture.invalid/job", Text: "Go 后端项目", FetchStatus: "SUCCESS", HTTPStatus: 200})
	if err != nil {
		t.Fatal(err)
	}
	proj, fact := d.ID(), d.ID()
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{"INSERT INTO projects(id,user_id,body) VALUES(?,?,?)", []any{proj, user, d.JSON(d.Project{ID: proj, Name: "fixture project"})}},
		{"INSERT INTO project_facts(id,user_id,project_id,body) VALUES(?,?,?,?)", []any{fact, user, proj, d.JSON(d.ProjectFact{ID: fact, ProjectID: proj, Claim: "implemented fixture", Kind: "IMPLEMENTED", Verified: true, CreatedAt: time.Now().UTC()})}},
		{"INSERT INTO job_match_results(user_id,job_id,body) VALUES(?,?,?)", []any{user, o.JobID, `{"fixture":true}`}},
	} {
		if _, err = store.DB.ExecContext(ctx, stmt.q, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	watch, err := store.CreateWatch(ctx, user, d.WatchInput{SourceID: source.ID, CheckInterval: 3600, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	bulk, err := store.StartSourceImport(ctx, user, []p.SourceImportSpec{{Adapter: "meituan", Company: "synthetic", URL: "https://zhaopin.meituan.com/web/campus?hiringType=1_1"}, {Adapter: "jd", Company: "synthetic", URL: "https://campus.jd.com/#/jobs?type=present"}})
	if err != nil {
		t.Fatal(err)
	}
	prepare := p.NewTask("SOURCE_IMPORT", bulk.Items[0].ID)
	prepare.Generation = 1
	prepare.ID = p.SourceImportTaskID(prepare.EntityID, prepare.Generation)
	if _, err = store.PrepareSourceImport(ctx, prepare); err != nil {
		t.Fatal(err)
	}
	if err = store.QueueSourceImport(ctx, prepare, "meituan", "graduate", "synthetic campus"); err != nil {
		t.Fatal(err)
	}
	pending := p.NewTask("SOURCE_IMPORT", bulk.Items[1].ID)
	pending.Generation = 1
	pending.ID = p.SourceImportTaskID(pending.EntityID, pending.Generation)
	run := p.MatchRun{ID: d.ID(), State: "RUNNING", Version: 1, RequestKey: d.ID(), Items: []p.MatchRunItem{{JobID: o.JobID, InputKey: d.Hash("fixture"), State: "RUNNING"}}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if _, err = store.DB.ExecContext(ctx, "INSERT INTO match_runs(id,user_id,request_key,state,token,lease_until,updated_at,body) VALUES(?,?,?,?,?,?,?,?)", run.ID, user, run.RequestKey, run.State, d.ID(), time.Now().Add(time.Minute), run.UpdatedAt, d.JSON(run)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PrepareRecovery(ctx); err == nil {
		t.Fatal("prepared live/source schema")
	}
	baseline, err := store.ProofDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The backup target is only this newly-created fixture, not the personal database.
	configRaw := d.JSON(map[string]any{"services": map[string]any{"api": map[string]any{"environment": map[string]string{"MYSQL_DSN": parsed.FormatDSN()}}, "mysql": map[string]any{"ports": []map[string]string{{"published": "13306"}}}}})
	m.execute = func(ctx context.Context, dir string, args []string, out, errOut io.Writer) error {
		if strings.Contains(strings.Join(args, " "), "config --format json") {
			_, e := io.WriteString(out, configRaw)
			return e
		}
		return command(ctx, dir, args, out, errOut)
	}
	path := filepath.Join(t.TempDir(), "fixture.sql")
	if _, err = m.backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	report, err := m.verifyBackup(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline.Tables, report.BeforeMigration.Tables) || !reflect.DeepEqual(baseline.Tables, report.AfterMigration.Tables) {
		t.Fatal("round-trip changed rows or exact table digests")
	}
	if report.BeforeMigration.ForeignKeysChecked < 10 || report.DurationMS <= 0 {
		t.Fatal(report)
	}
	count, err := m.adminSQL(ctx, "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='"+report.Database+"';")
	if err != nil || count != "0" {
		t.Fatal("verification left disposable schema", count, err)
	}
	// A syntactically valid dump which attempts a cross-database mutation is denied.
	bad := filepath.Join(t.TempDir(), "cross-database.sql")
	os.WriteFile(bad, []byte("USE `"+schema+"`; DELETE FROM profiles;\n"), 0600)
	if err = backupMetadata(bad, time.Now().UTC(), report.ServerVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = m.verifyBackup(ctx, bad, false); err == nil {
		t.Fatal("import user changed another schema")
	}
	current, err := store.ProofDatabase(ctx)
	if err != nil || !reflect.DeepEqual(current.Tables, baseline.Tables) {
		t.Fatal("cross-schema import damaged source", err)
	}
	// A valid checksum is insufficient when FK checks concealed broken evidence.
	invalid := filepath.Join(t.TempDir(), "broken-reference.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\nSET FOREIGN_KEY_CHECKS=0; INSERT INTO evidence(id,observation_id,job_id,body) VALUES('missing-fixture','missing-observation','missing-job','{}'); SET FOREIGN_KEY_CHECKS=1;\n")...)
	os.WriteFile(invalid, raw, 0600)
	if err = backupMetadata(invalid, time.Now().UTC(), report.ServerVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = m.verifyBackup(ctx, invalid, false); err == nil {
		t.Fatal("accepted dangling evidence")
	}
	future := filepath.Join(t.TempDir(), "future-schema.sql")
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\nINSERT INTO schema_migrations(version) VALUES('future-incompatible-v999');\n")...)
	os.WriteFile(future, raw, 0600)
	if err = backupMetadata(future, time.Now().UTC(), report.ServerVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = m.verifyBackup(ctx, future, false); err == nil || !strings.Contains(err.Error(), "unsupported database migration") {
		t.Fatal("accepted future schema", err)
	}
	legacy := filepath.Join(t.TempDir(), "previous-schema.sql")
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\nDROP TABLE source_http_cache; DROP TABLE source_import_items; DROP TABLE source_import_batches; DELETE FROM schema_migrations WHERE version IN ('local-reliability-v1','source-import-v1');\n")...)
	os.WriteFile(legacy, raw, 0600)
	if err = backupMetadata(legacy, time.Now().UTC(), report.ServerVersion); err != nil {
		t.Fatal(err)
	}
	upgraded, err := m.verifyBackup(ctx, legacy, false)
	if err != nil {
		t.Fatal("previous backup failed migration", err)
	}
	if len(upgraded.BeforeMigration.Tables) != len(baseline.Tables)-3 || len(upgraded.AfterMigration.Tables) != len(baseline.Tables) {
		t.Fatal("new table not restored by migration", upgraded)
	}
	for _, name := range []string{"profiles", "projects", "project_facts", "jobs", "observations", "job_match_results"} {
		if upgraded.AfterMigration.Tables[name] != baseline.Tables[name] {
			t.Fatal("migration changed personal data", name)
		}
	}
	// Keep mode restores a usable separate schema and removes the importer account.
	kept, err := m.verifyBackup(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if _, e := m.adminSQL(clean, "DROP DATABASE `"+kept.Database+"`;"); e != nil {
			t.Error(e)
		}
	})
	parsed.DBName = kept.Database
	restored, err := p.Open(ctx, parsed.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.DB.Close()
	proof, err := restored.ProofDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range baseline.Tables {
		if name != "watch_targets" && name != "match_runs" && name != "source_import_items" && proof.Tables[name] != want {
			t.Fatal("kept data changed", name)
		}
	}
	recoveredWatch, err := restored.Watch(ctx, user, watch.ID)
	if err != nil || recoveredWatch.Enabled || recoveredWatch.ScheduleVersion != watch.ScheduleVersion+1 {
		t.Fatal("restored watch resumed without review", err)
	}
	var state, token string
	if err = restored.DB.QueryRowContext(ctx, "SELECT state,token FROM match_runs WHERE id=?", run.ID).Scan(&state, &token); err != nil || state != "WAITING_AUTH" || token != "" {
		t.Fatal("restored task retained execution", state, token, err)
	}
	if kept.RecoveryChanges["paused_watches"] != 2 || kept.RecoveryChanges["interrupted_matching_tasks"] != 1 {
		t.Fatal(kept.RecoveryChanges)
	}
	interrupted, err := restored.LatestSourceImport(ctx, user)
	if err != nil || interrupted.State != "COMPLETED_WITH_ERRORS" || interrupted.Failed != 2 {
		t.Fatal("restored source imports resumed without review", interrupted, err)
	}
	if _, err = restored.PrepareSourceImport(ctx, pending); err != p.ErrStaleSourceImport {
		t.Fatal("restored prepare task was replayable", err)
	}
	retry, err := restored.RetrySourceImport(ctx, user, bulk.ID)
	if err != nil || retry.State != "RUNNING" {
		t.Fatal("restored imports not retryable", retry, err)
	}
	files, err := filepath.Glob(path + ".verify-*.json")
	if err != nil || len(files) < 2 {
		t.Fatal("missing report", files, err)
	}
	encoded, err := os.ReadFile(files[0])
	if err != nil || !json.Valid(encoded) {
		t.Fatal("invalid private report", err)
	}
	t.Logf("synthetic recovery: %d tables, %d references, %d ms", len(report.AfterMigration.Tables), report.AfterMigration.ForeignKeysChecked, report.DurationMS)
}
