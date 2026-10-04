package persistence

import (
	"context"
	"database/sql"
	"net/url"
)

// OfficialJobURL exposes only an official posting visible to the current user.
// A manually supplied URL does not acquire official trust by being imported.
func (s *Store) OfficialJobURL(ctx context.Context, user, jobID string) (string, error) {
	var link string
	err := s.DB.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(p.body,'$.url'))
 FROM postings p JOIN sources src ON src.id=p.source_id JOIN jobs j ON j.id=p.job_id
 WHERE p.job_id=? AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))
 AND JSON_UNQUOTE(JSON_EXTRACT(src.body,'$.trust'))='OFFICIAL'
 AND (JSON_UNQUOTE(JSON_EXTRACT(src.body,'$.visibility'))='GLOBAL' OR
 (JSON_UNQUOTE(JSON_EXTRACT(src.body,'$.visibility'))='PRIVATE' AND JSON_UNQUOTE(JSON_EXTRACT(src.body,'$.owner_id'))=?))
 AND JSON_UNQUOTE(JSON_EXTRACT(p.body,'$.url'))<>''
 ORDER BY JSON_UNQUOTE(JSON_EXTRACT(p.body,'$.last_seen_at')) DESC,p.id LIMIT 1`, jobID, user, user).Scan(&link)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	u, err := url.Parse(link)
	if err != nil || len(link) > 2048 || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return "", nil
	}
	return link, nil
}
