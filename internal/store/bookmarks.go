package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const foreignKeyViolation = "23503"

// ToggleBookmark saves the prompt for the visitor, or removes it if it was
// already saved. It reports whether the prompt is bookmarked afterwards.
func (s *Store) ToggleBookmark(ctx context.Context, visitorID string, promptID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM bookmarks WHERE visitor_id = $1 AND prompt_id = $2`, visitorID, promptID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 {
		return false, nil
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO bookmarks (visitor_id, prompt_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, visitorID, promptID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return false, ErrNotFound
	}
	return err == nil, err
}

func (s *Store) CountBookmarks(ctx context.Context, visitorID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookmarks WHERE visitor_id = $1`, visitorID).Scan(&n)
	return n, err
}
