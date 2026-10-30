// Package sqlfixture creates test-owned databases and restricted accounts.
// It never resets, truncates, or drops the database supplied as a template.
package sqlfixture

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

var scopes = struct {
	sync.Mutex
	dsn map[*testing.T]map[string]string
}{dsn: make(map[*testing.T]map[string]string)}

// Database shares a database between calls in the same test/purpose. Subtests,
// repeated -count runs, packages and processes receive independent schemas.
// MYSQL_TEST_ADMIN_DSN is an explicit opt-in to create disposable resources.
func Database(t *testing.T, template, purpose string) string {
	t.Helper()
	scopes.Lock()
	defer scopes.Unlock()
	if value := scopes.dsn[t][purpose]; value != "" {
		return value
	}
	adminDSN := os.Getenv("MYSQL_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Fatal("MYSQL_TEST_ADMIN_DSN is required for isolated integration tests; use scripts/verify-integration-isolation.py")
	}
	adminConfig, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		t.Fatal("invalid test database administrator configuration")
	}
	if template == "" {
		template = adminDSN
	}
	cfg, err := mysql.ParseDSN(template)
	if err != nil {
		t.Fatal("invalid test database template")
	}
	if cfg.Net != adminConfig.Net || cfg.Addr != adminConfig.Addr {
		t.Fatal("test database template and administrator must use the same server")
	}
	admin, err := sql.Open("mysql", adminDSN)
	if err != nil {
		t.Fatal("invalid test database administrator configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	id := hex.EncodeToString(token[:])
	schema, account, password := "ct_test_"+id, "ct_test_"+id[:16], id
	created, userCreated := false, false
	// Register before creating anything, so a partial setup failure is cleaned.
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if created {
			if _, err := admin.ExecContext(cleanup, "DROP DATABASE `"+schema+"`"); err != nil {
				t.Error("cannot remove owned test database", schema, err)
			}
		}
		if userCreated {
			if _, err := admin.ExecContext(cleanup, "DROP USER '"+account+"'@'%'"); err != nil {
				t.Error("cannot remove owned test account", account, err)
			}
		}
		admin.Close()
		scopes.Lock()
		delete(scopes.dsn, t)
		scopes.Unlock()
	})
	if _, err = admin.ExecContext(ctx, "CREATE DATABASE `"+schema+"` CHARACTER SET utf8mb4"); err != nil {
		t.Fatal("cannot create isolated test database", err)
	}
	created = true
	if _, err = admin.ExecContext(ctx, "CREATE USER '"+account+"'@'%' IDENTIFIED BY '"+password+"'"); err != nil {
		t.Fatal("cannot create isolated test account", err)
	}
	userCreated = true
	if _, err = admin.ExecContext(ctx, "GRANT ALL ON `"+schema+"`.* TO '"+account+"'@'%'"); err != nil {
		t.Fatal("cannot grant isolated test database", err)
	}
	cfg.DBName, cfg.User, cfg.Passwd = schema, account, password
	if scopes.dsn[t] == nil {
		scopes.dsn[t] = make(map[string]string)
	}
	scopes.dsn[t][purpose] = cfg.FormatDSN()
	return cfg.FormatDSN()
}
