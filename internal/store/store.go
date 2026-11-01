package store

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type Category struct {
	ID          int64
	Name        string
	Slug        string
	Description string
	PromptCount int
}

type Prompt struct {
	ID           int64
	Title        string
	Description  string
	Body         string
	CategoryID   *int64
	CategoryName string
	CategorySlug string
	ImagePath    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Bookmarked   bool
}

type PromptInput struct {
	Title       string
	Description string
	Body        string
	CategoryID  *int64
	ImagePath   string
}
