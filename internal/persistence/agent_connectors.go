package persistence

import (
	"context"
	"database/sql"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/mcpclient"
)

func (s *Store) AgentConnectors(ctx context.Context, user string) ([]mcpclient.Connector, error) {
	return Many[mcpclient.Connector](ctx, s.DB, "SELECT body FROM agent_connectors WHERE user_id=? ORDER BY id LIMIT 10", user)
}
func (s *Store) AgentConnector(ctx context.Context, user, id string) (mcpclient.Connector, error) {
	return One[mcpclient.Connector](ctx, s.DB, "SELECT body FROM agent_connectors WHERE user_id=? AND id=?", user, id)
}
func (s *Store) SaveAgentConnector(ctx context.Context, user string, v mcpclient.Connector) (mcpclient.Connector, error) {
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := v.Validate(); e != nil {
			return ErrValidation
		}
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		if v.ID == "" {
			old, e := Many[mcpclient.Connector](ctx, tx, "SELECT body FROM agent_connectors WHERE user_id=? ORDER BY id LIMIT 10", user)
			if e != nil {
				return e
			}
			for _, c := range old {
				if c.Name == v.Name && c.URL == v.URL && d.JSON(c.Allowed) == d.JSON(v.Allowed) && d.JSON(c.Names) == d.JSON(v.Names) {
					v = c
					return nil
				}
			}
			var n int
			if e := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_connectors WHERE user_id=?", user).Scan(&n); e != nil {
				return e
			}
			if n >= 10 {
				return ErrValidation
			}
			v.ID = d.ID()
			v.Version = 1
		} else {
			old, e := One[mcpclient.Connector](ctx, tx, "SELECT body FROM agent_connectors WHERE id=? AND user_id=? FOR UPDATE", v.ID, user)
			if e != nil {
				return e
			}
			if old.Version != v.Version {
				return ErrConflict
			}
			v.Version++
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO agent_connectors(id,user_id,version,body) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE version=VALUES(version),body=VALUES(body)", v.ID, user, v.Version, d.JSON(v))
		return e
	})
	return v, e
}
