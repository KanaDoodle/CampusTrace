package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"github.com/KanaDoodle/CampusTrace/migrations"
	"github.com/go-sql-driver/mysql"
	"strings"
	"time"
)

var ErrConflict = errors.New("version conflict or duplicate")
var ErrNotFound = sql.ErrNoRows
var ErrBackendUnavailable = errors.New("backend unavailable")
var ErrValidation = errors.New("business validation")

type Store struct {
	DB      *sql.DB
	Weights rules.Weights
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(3 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, Weights: rules.DefaultWeights()}, nil
}
func (s *Store) Migrate(ctx context.Context) error {
	for _, stmt := range strings.Split(migrations.SQL, ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	}
	return s.migrateRepair(ctx)
}

type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func One[T any](ctx context.Context, q Queryer, query string, args ...any) (T, error) {
	var v T
	var b []byte
	err := q.QueryRowContext(ctx, query, args...).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &v)
	}
	return v, err
}
func Many[T any](ctx context.Context, q Queryer, query string, args ...any) ([]T, error) {
	out := []T{}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var v T
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func duplicate(err error) bool {
	var m *mysql.MySQLError
	return errors.As(err, &m) && m.Number == 1062
}
func (s *Store) Job(ctx context.Context, id string) (d.Job, error) {
	return One[d.Job](ctx, s.DB, "SELECT body FROM jobs WHERE id=?", id)
}
func (s *Store) Jobs(ctx context.Context, query string) ([]d.Job, error) {
	return Many[d.Job](ctx, s.DB, "SELECT body FROM jobs WHERE LOWER(JSON_UNQUOTE(JSON_EXTRACT(body,'$.title'))) LIKE ? OR LOWER(JSON_UNQUOTE(JSON_EXTRACT(body,'$.company'))) LIKE ? ORDER BY id LIMIT 100", "%"+strings.ToLower(query)+"%", "%"+strings.ToLower(query)+"%")
}
func (s *Store) Observation(ctx context.Context, id string) (d.Observation, error) {
	return One[d.Observation](ctx, s.DB, "SELECT body FROM observations WHERE id=?", id)
}
func (s *Store) Evidence(ctx context.Context, job string) ([]d.Evidence, error) {
	return Many[d.Evidence](ctx, s.DB, "SELECT body FROM evidence WHERE job_id=? ORDER BY id", job)
}
func (s *Store) Observations(ctx context.Context, job string) ([]d.Observation, error) {
	return Many[d.Observation](ctx, s.DB, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at,id", job)
}
func (s *Store) Assessments(ctx context.Context, job string) ([]d.Assessment, error) {
	return Many[d.Assessment](ctx, s.DB, "SELECT body FROM assessments WHERE job_id=? ORDER BY assessed_at,id", job)
}
func (s *Store) Changes(ctx context.Context, job string) ([]d.Change, error) {
	return Many[d.Change](ctx, s.DB, "SELECT body FROM changes WHERE job_id=? ORDER BY id", job)
}
func (s *Store) Profile(ctx context.Context, user string) (d.Profile, error) {
	return One[d.Profile](ctx, s.DB, "SELECT body FROM profiles WHERE user_id=?", user)
}
func (s *Store) SaveProfile(ctx context.Context, user string, p d.Profile) error {
	p.UserID = user
	if (p.GraduationFrom != 0 && (p.GraduationFrom < 2000 || p.GraduationTo > 2100 || p.GraduationTo < p.GraduationFrom)) || (p.GraduationYear != 0 && (p.GraduationYear < 2000 || p.GraduationYear > 2100)) || p.ExperienceMonths < 0 || len(p.Skills) > 100 {
		return ErrValidation
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&id); err != nil {
			return err
		}
		old, err := One[d.Profile](ctx, tx, "SELECT body FROM profiles WHERE user_id=?", user)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		p.Revision = old.Revision + 1
		_, err = tx.ExecContext(ctx, "INSERT INTO profiles(id,user_id,body) VALUES(?,?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", user, user, d.JSON(p))
		return err
	})
}
func (s *Store) Owned(ctx context.Context, table, user string) ([]json.RawMessage, error) {
	switch table {
	case "applications", "interviews", "reviews", "weak_topics", "projects", "project_facts", "documents":
	default:
		return nil, ErrValidation
	}
	return Many[json.RawMessage](ctx, s.DB, "SELECT body FROM "+table+" WHERE user_id=? ORDER BY id LIMIT 500", user)
}
func (s *Store) NewUser(ctx context.Context, email, hash string) (string, error) {
	id := d.ID()
	_, err := s.DB.ExecContext(ctx, "INSERT INTO users(id,email,password_hash) VALUES(?,?,?)", id, strings.ToLower(email), hash)
	return id, err
}
func (s *Store) Credentials(ctx context.Context, email string) (string, string, error) {
	var id, hash string
	err := s.DB.QueryRowContext(ctx, "SELECT id,password_hash FROM users WHERE email=?", strings.ToLower(email)).Scan(&id, &hash)
	return id, hash, err
}
