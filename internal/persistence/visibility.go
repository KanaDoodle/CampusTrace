package persistence

import (
	"context"
	"database/sql"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
)

// Internal unscoped readers are for workers/operator ingestion only. Every
// user-facing path must enter through these readers or CheckJobAccess.
func CheckJobAccess(ctx context.Context, q Queryer, user, job string) error {
	if user == "" {
		return ErrNotFound
	}
	var id string
	return q.QueryRowContext(ctx, "SELECT id FROM jobs WHERE id=? AND (visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?))", job, user).Scan(&id)
}
func (s *Store) JobForUser(ctx context.Context, user, id string) (d.Job, error) {
	if err := CheckJobAccess(ctx, s.DB, user, id); err != nil {
		return d.Job{}, err
	}
	return s.Job(ctx, id)
}
func (s *Store) JobsForUser(ctx context.Context, user, query string) ([]d.Job, error) {
	if user == "" {
		return nil, ErrNotFound
	}
	pattern := "%" + strings.ToLower(query) + "%"
	return Many[d.Job](ctx, s.DB, "SELECT body FROM jobs WHERE (visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?)) AND (LOWER(JSON_UNQUOTE(JSON_EXTRACT(body,'$.title'))) LIKE ? OR LOWER(JSON_UNQUOTE(JSON_EXTRACT(body,'$.company'))) LIKE ?) ORDER BY id LIMIT 100", user, pattern, pattern)
}
func (s *Store) IngestForUser(ctx context.Context, user string, i Ingest) (d.Observation, error) {
	if user == "" {
		return d.Observation{}, ErrNotFound
	}
	if i.SourceID != "" && i.SourceID != "manual" {
		source, err := One[d.Source](ctx, s.DB, "SELECT body FROM sources WHERE id=?", i.SourceID)
		if err != nil {
			return d.Observation{}, err
		}
		if source.Visibility != "PRIVATE" || source.OwnerID != user {
			return d.Observation{}, ErrNotFound
		}
	} else {
		i.SourceID = d.Hash(d.JSON([]string{"manual-v2", user}))[:32]
		source := d.Source{ID: i.SourceID, Name: "我的手工录入", Type: "MANUAL", Trust: "MANUAL", OwnerID: user, Visibility: "PRIVATE", Timezone: "Asia/Shanghai"}
		err := s.SaveSource(ctx, source)
		if err != nil && !duplicate(err) {
			return d.Observation{}, err
		}
	}
	// Verify the resolved source again, including a pre-existing ID collision.
	source, err := One[d.Source](ctx, s.DB, "SELECT body FROM sources WHERE id=?", i.SourceID)
	if err != nil {
		return d.Observation{}, err
	}
	if source.OwnerID != user || source.Visibility != "PRIVATE" {
		return d.Observation{}, sql.ErrNoRows
	}
	return s.Ingest(ctx, i)
}
