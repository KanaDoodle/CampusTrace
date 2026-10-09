package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

func (s *Store) KnowledgeIdentity(ctx context.Context, user string) (string, error) {
	var revision uint64
	err := s.DB.QueryRowContext(ctx, "SELECT revision FROM knowledge_state WHERE user_id=?", user).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return strconv.FormatUint(revision, 10), err
}
