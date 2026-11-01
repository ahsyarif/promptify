package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

type ListParams struct {
	Query          string
	CategorySlug   string
	VisitorID      string
	BookmarkedOnly bool
	Limit          int
	Offset         int
}

const promptColumns = `p.id, p.title, p.description, p.body, p.category_id,
	COALESCE(c.name, ''), COALESCE(c.slug, ''), p.image_path, p.created_at, p.updated_at,
	EXISTS (SELECT 1 FROM bookmarks b WHERE b.prompt_id = p.id AND b.visitor_id = $1)`

func scanPrompt(row pgx.Row) (Prompt, error) {
	var p Prompt
	err := row.Scan(&p.ID, &p.Title, &p.Description, &p.Body, &p.CategoryID,
		&p.CategoryName, &p.CategorySlug, &p.ImagePath, &p.CreatedAt, &p.UpdatedAt, &p.Bookmarked)
	return p, err
}

// PrefixTSQuery turns free-form user input into a to_tsquery expression where
// every word is prefix-matched, so results update while the user is typing.
// Only letters and digits survive, which keeps the expression syntactically safe.
func PrefixTSQuery(input string) string {
	words := strings.FieldsFunc(input, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for i, w := range words {
		words[i] = strings.ToLower(w) + ":*"
	}
	return strings.Join(words, " & ")
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ListPrompts returns one page of prompts and whether more pages exist.
func (s *Store) ListPrompts(ctx context.Context, p ListParams) ([]Prompt, bool, error) {
	args := []any{p.VisitorID}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	var (
		joins []string
		where []string
		order = "p.created_at DESC, p.id DESC"
	)

	if p.BookmarkedOnly {
		joins = append(joins, "JOIN bookmarks bm ON bm.prompt_id = p.id AND bm.visitor_id = $1")
		order = "bm.created_at DESC, p.id DESC"
	}
	if p.CategorySlug != "" {
		where = append(where, "c.slug = "+arg(p.CategorySlug))
	}
	if q := strings.TrimSpace(p.Query); q != "" {
		like := arg("%" + likeEscaper.Replace(q) + "%")
		cond := fmt.Sprintf("p.title ILIKE %s OR p.description ILIKE %s", like, like)
		if tsq := PrefixTSQuery(q); tsq != "" {
			tsArg := arg(tsq)
			cond = fmt.Sprintf("p.search @@ to_tsquery('english', %s) OR %s", tsArg, cond)
			order = fmt.Sprintf("ts_rank(p.search, to_tsquery('english', %s)) DESC, %s", tsArg, order)
		}
		where = append(where, "("+cond+")")
	}

	sql := "SELECT " + promptColumns + " FROM prompts p LEFT JOIN categories c ON c.id = p.category_id "
	sql += strings.Join(joins, " ")
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += fmt.Sprintf(" ORDER BY %s LIMIT %s OFFSET %s", order, arg(p.Limit+1), arg(p.Offset))

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, fmt.Errorf("list prompts: %w", err)
	}
	prompts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Prompt, error) { return scanPrompt(r) })
	if err != nil {
		return nil, false, fmt.Errorf("list prompts: %w", err)
	}
	hasMore := len(prompts) > p.Limit
	if hasMore {
		prompts = prompts[:p.Limit]
	}
	return prompts, hasMore, nil
}

func (s *Store) GetPrompt(ctx context.Context, id int64, visitorID string) (Prompt, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+promptColumns+`
		FROM prompts p LEFT JOIN categories c ON c.id = p.category_id
		WHERE p.id = $2`, visitorID, id)
	p, err := scanPrompt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Prompt{}, ErrNotFound
	}
	return p, err
}

func (s *Store) CreatePrompt(ctx context.Context, in PromptInput) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO prompts (title, description, body, category_id, image_path)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		in.Title, in.Description, in.Body, in.CategoryID, in.ImagePath).Scan(&id)
	return id, err
}

func (s *Store) UpdatePrompt(ctx context.Context, id int64, in PromptInput) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE prompts
		SET title = $2, description = $3, body = $4, category_id = $5, image_path = $6, updated_at = now()
		WHERE id = $1`,
		id, in.Title, in.Description, in.Body, in.CategoryID, in.ImagePath)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePrompt removes a prompt and returns its image path so the caller can
// clean up the file.
func (s *Store) DeletePrompt(ctx context.Context, id int64) (string, error) {
	var imagePath string
	err := s.pool.QueryRow(ctx, `DELETE FROM prompts WHERE id = $1 RETURNING image_path`, id).Scan(&imagePath)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return imagePath, err
}
