package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

// A bounded cache of immutable display JSON. It contains no JD text, project
// evidence or complete resume. SQL validates the version on every read. Write
// and export paths continue to read their own current database snapshots.
type inventoryViewCache struct {
	mu      sync.Mutex
	entries []inventoryViewEntry
	bytes   int
}
type inventoryViewEntry struct {
	scope, key string
	body       []byte
}

func (c *inventoryViewCache) get(scope, key string) (MatchSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, e := range c.entries {
		if e.scope == scope && e.key == key {
			copy(c.entries[i:], c.entries[i+1:])
			c.entries[len(c.entries)-1] = e
			var v MatchSnapshot
			if json.Unmarshal(e.body, &v) == nil {
				return v, true
			}
		}
	}
	return MatchSnapshot{}, false
}
func (c *inventoryViewCache) put(scope, key string, v MatchSnapshot) {
	body, err := json.Marshal(v)
	if err != nil || len(body) > 32<<20 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.scope == scope && e.key == key {
			return
		}
	}
	for len(c.entries) > 0 && (len(c.entries) >= 8 || c.bytes+len(body) > 64<<20) {
		c.bytes -= len(c.entries[0].body)
		c.entries[0] = inventoryViewEntry{}
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, inventoryViewEntry{scope, key, body})
	c.bytes += len(body)
}

func inventoryScope(user, model, mask string) string {
	return d.Hash(d.JSON([]string{user, model, mask, matching.LocalVersion}))
}

// Every dependency is hashed in the same repeatable-read transaction used to
// build a miss. Counts or MAX timestamps alone would miss edits and deletions.
func inventoryVersion(ctx context.Context, tx *sql.Tx, scope, user string) (string, error) {
	day := matchDay(time.Now())
	queries := []struct {
		sql  string
		args []any
	}{
		{`SELECT j.id,CONCAT(j.visibility,':',COALESCE(j.owner_id,''),':',SHA2(CAST(j.body AS CHAR),256),':',COALESCE((SELECT CONCAT(o.id,':',SHA2(CAST(o.body AS CHAR),256)) FROM observations o WHERE o.job_id=j.id ORDER BY o.observed_at DESC,o.id DESC LIMIT 1),'')) FROM jobs j WHERE j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?) ORDER BY j.id`, []any{user}},
	}
	for _, table := range []string{"profiles", "projects", "project_facts", "applications", "application_campaigns", "job_match_results", "match_settings"} {
		id := "id"
		if table == "job_match_results" {
			id = "job_id"
		}
		if table == "match_settings" {
			id = "user_id"
		}
		queries = append(queries, struct {
			sql  string
			args []any
		}{"SELECT " + id + ",SHA2(CAST(body AS CHAR),256) FROM " + table + " WHERE user_id=? ORDER BY " + id, []any{user}})
	}
	queries = append(queries, struct {
		sql  string
		args []any
	}{"SELECT job_id,disposition FROM user_job_preferences WHERE user_id=? ORDER BY job_id", []any{user}}, struct {
		sql  string
		args []any
	}{"SELECT usage_day,CAST(calls AS CHAR) FROM match_daily_usage WHERE user_id=? AND usage_day=?", []any{user, day}})
	// Length-delimited JSON prevents ambiguities between rows and tables.
	values := [][][]string{}
	for _, q := range queries {
		rows, err := tx.QueryContext(ctx, q.sql, q.args...)
		if err != nil {
			return "", err
		}
		part := [][]string{}
		for rows.Next() {
			var id, value string
			if err = rows.Scan(&id, &value); err != nil {
				rows.Close()
				return "", err
			}
			part = append(part, []string{id, value})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		values = append(values, part)
	}
	return d.Hash(scope + day + d.JSON(values)), ctx.Err()
}

func displayInventory(v MatchSnapshot) MatchSnapshot {
	v.Profile = d.Profile{}
	v.CandidateDocument = ""
	facts := v.Candidate.Facts[:0]
	for _, f := range v.Candidate.Facts {
		if f.Kind == "ROLE" || f.Kind == "CITY_PREFERRED" || f.Kind == "CITY_ACCEPTABLE" {
			facts = append(facts, matching.Fact{Kind: f.Kind, Text: f.Text})
		}
	}
	v.Candidate = matching.Candidate{Facts: facts}
	for i := range v.Jobs {
		r := &v.Jobs[i]
		j := r.Job
		r.Job = d.Job{ID: j.ID, CompanyID: j.CompanyID, Company: j.Company, Title: j.Title, Locations: j.Locations, JobType: j.JobType, CurrentStatus: j.CurrentStatus, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt}
		r.Text = ""
		r.Result = nil
	}
	return v
}

// known is a browsing version, never an access token. Another scope cannot use
// it to retrieve a cached snapshot. Eviction simply falls back to a full index.
func (s *Store) InventoryView(ctx context.Context, user, model, mask, known string) (current MatchSnapshot, key string, previous *MatchSnapshot, err error) {
	scope := inventoryScope(user, model, mask)
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return
	}
	defer tx.Rollback()
	key, err = inventoryVersion(ctx, tx, scope, user)
	if err != nil {
		return
	}
	var hit bool
	current, hit = s.inventoryViews.get(scope, key)
	if !hit {
		current, err = s.matchSnapshotTx(ctx, tx, user, model, mask, nil, true, "", false, false, true)
		if err != nil {
			return
		}
		current = displayInventory(current)
	}
	if err = tx.Commit(); err != nil {
		return
	}
	if !hit {
		s.inventoryViews.put(scope, key, current)
	}
	if known == key {
		previous = &current
	} else if known != "" {
		if old, ok := s.inventoryViews.get(scope, known); ok {
			previous = &old
		}
	}
	return
}
