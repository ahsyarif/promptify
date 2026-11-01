package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const userColumns = `u.id, u.email, COALESCE(u.password_hash, ''), COALESCE(u.google_sub, ''), u.member_since`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.GoogleSub, &u.MemberSince)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		INSERT INTO users AS u (email, password_hash) VALUES ($1, $2)
		RETURNING `+userColumns, email, passwordHash))
	if isUniqueViolation(err) {
		return User{}, ErrConflict
	}
	return u, err
}

func (s *Store) CreateGoogleUser(ctx context.Context, email, sub string) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		INSERT INTO users AS u (email, google_sub) VALUES ($1, $2)
		RETURNING `+userColumns, email, sub))
	if isUniqueViolation(err) {
		return User{}, ErrConflict
	}
	return u, err
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id = $1`, id))
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE lower(u.email) = lower($1)`, email))
}

func (s *Store) UserByGoogleSub(ctx context.Context, sub string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.google_sub = $1`, sub))
}

// LinkGoogle attaches a Google account to an existing, unlinked user. It
// fails with ErrConflict if the user is already linked to another account.
//
// Password signups don't verify the email address, so someone could register
// a victim's address first and wait for them to arrive through Google. The
// Google login is the proof of ownership, so linking drops the unverified
// password and every existing session, locking such a squatter out.
func (s *Store) LinkGoogle(ctx context.Context, userID int64, sub string) (User, error) {
	var u User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `
			UPDATE users u SET google_sub = $2, password_hash = NULL
			WHERE u.id = $1 AND u.google_sub IS NULL
			RETURNING `+userColumns, userID, sub))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
		return err
	})
	if errors.Is(err, ErrNotFound) || isUniqueViolation(err) {
		return User{}, ErrConflict
	}
	return u, err
}
