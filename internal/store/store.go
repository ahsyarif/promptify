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
	IsPremium    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Bookmarked   bool

	// Locked is set by the server when the viewer may not see a premium
	// body, which has then been cleared. It is not stored.
	Locked bool
}

type PromptInput struct {
	Title       string
	Description string
	Body        string
	CategoryID  *int64
	ImagePath   string
	IsPremium   bool
}

type User struct {
	ID           int64
	Email        string
	PasswordHash string
	GoogleSub    string
	MemberSince  *time.Time
}

func (u *User) IsMember() bool { return u != nil && u.MemberSince != nil }

func (u *User) HasPassword() bool { return u.PasswordHash != "" }

const (
	OrderPending = "pending"
	OrderPaid    = "paid"
	OrderExpired = "expired"
	OrderFailed  = "failed"
)

type Order struct {
	ID            int64
	OrderID       string
	UserID        int64
	Amount        int64
	Status        string
	TransactionID string
	QRURL         string
	QRString      string
	ExpiresAt     time.Time
	PaidAt        *time.Time
	CreatedAt     time.Time
}
