package store

import (
	"context"
	"time"
)

func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash []byte, expiresAt time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND expires_at <= now()`, userID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt)
	return err
}

// UserBySession returns the user owning an unexpired session, or ErrNotFound.
func (s *Store) UserBySession(ctx context.Context, tokenHash []byte) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		SELECT `+userColumns+` FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash))
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}
