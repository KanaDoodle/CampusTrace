package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"sort"
)

func (s *Store) MatchImportSnapshot(ctx context.Context, user, mask string, ids []string) (MatchSnapshot, error) {
	return s.matchSnapshot(ctx, user, matching.ChatIdentity, mask, ids, false, "", false)
}

func matchResultVersion(ctx context.Context, q Queryer, user, job string) (string, error) {
	var body []byte
	err := q.QueryRowContext(ctx, "SELECT body FROM job_match_results WHERE user_id=? AND job_id=?", user, job).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "NONE", nil
	}
	if err != nil {
		return "", err
	}
	return d.Hash(string(body)), nil
}
func (s *Store) MatchResultVersions(ctx context.Context, user string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if err := CheckJobAccess(ctx, s.DB, user, id); err != nil {
			return nil, err
		}
		v, err := matchResultVersion(ctx, s.DB, user, id)
		if err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, nil
}

// A preview confirms the validated subset. Any concurrent profile, accepted job
// or saved result change aborts all subset writes; rejected jobs remain untouched.
func (s *Store) SaveChatMatches(ctx context.Context, user, mask string, results []matching.Result, previous map[string]string) error {
	if len(results) == 0 || len(results) > 100 || len(previous) != len(results) {
		return ErrValidation
	}
	ordered := append([]matching.Result{}, results...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].JobID < ordered[j].JobID })
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		var active int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM match_runs WHERE user_id=? AND state IN ('RUNNING','PAUSING')", user).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return ErrMatchRunBusy
		}
		seen := map[string]bool{}
		for _, r := range ordered {
			if r.Source != matching.ChatSource || r.Model != matching.ChatIdentity || seen[r.JobID] || previous[r.JobID] == "" {
				return ErrValidation
			}
			seen[r.JobID] = true
			if err := CheckJobAccess(ctx, tx, user, r.JobID); err != nil {
				return err
			}
			old, err := matchResultVersion(ctx, tx, user, r.JobID)
			if err != nil {
				return err
			}
			if old != previous[r.JobID] {
				return ErrStaleInput
			}
			if err := s.saveMatchResult(ctx, tx, user, mask, r); err != nil {
				return err
			}
		}
		return nil
	})
}
