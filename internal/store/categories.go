package store

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const uniqueViolation = "23505"

func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteRune('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.slug, c.description, COUNT(p.id)
		FROM categories c LEFT JOIN prompts p ON p.category_id = c.id
		GROUP BY c.id
		ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Category, error) {
		var c Category
		err := r.Scan(&c.ID, &c.Name, &c.Slug, &c.Description, &c.PromptCount)
		return c, err
	})
}

func (s *Store) CreateCategory(ctx context.Context, name, description string) (Category, error) {
	c := Category{Name: strings.TrimSpace(name), Slug: Slugify(name), Description: strings.TrimSpace(description)}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO categories (name, slug, description) VALUES ($1, $2, $3) RETURNING id`,
		c.Name, c.Slug, c.Description).Scan(&c.ID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return Category{}, ErrConflict
	}
	return c, err
}

func (s *Store) CategoryExists(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM categories WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}
