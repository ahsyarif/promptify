package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const orderColumns = `id, order_id, user_id, amount, status, transaction_id, qr_url, qr_string, expires_at, paid_at, created_at`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.OrderID, &o.UserID, &o.Amount, &o.Status, &o.TransactionID,
		&o.QRURL, &o.QRString, &o.ExpiresAt, &o.PaidAt, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	return o, err
}

func (s *Store) CreateOrder(ctx context.Context, o Order) (Order, error) {
	return scanOrder(s.pool.QueryRow(ctx, `
		INSERT INTO orders (order_id, user_id, amount, transaction_id, qr_url, qr_string, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+orderColumns,
		o.OrderID, o.UserID, o.Amount, o.TransactionID, o.QRURL, o.QRString, o.ExpiresAt))
}

func (s *Store) OrderByOrderID(ctx context.Context, orderID string) (Order, error) {
	return scanOrder(s.pool.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE order_id = $1`, orderID))
}

// PendingOrderForUser returns the user's latest pending order that still has
// more than a minute left, so an open QR code can be shown again.
func (s *Store) PendingOrderForUser(ctx context.Context, userID int64) (Order, error) {
	return scanOrder(s.pool.QueryRow(ctx, `
		SELECT `+orderColumns+` FROM orders
		WHERE user_id = $1 AND status = 'pending' AND expires_at > now() + interval '1 minute'
		ORDER BY created_at DESC LIMIT 1`, userID))
}

// ApplyOrderStatus records a payment outcome and is safe to call repeatedly
// (webhook retries, status polls). A paid order is final. Any other order may
// still become paid, because a settlement confirmed by Midtrans means the
// money was received even if we had already marked the order expired.
// Becoming paid grants lifetime membership to the order's user.
func (s *Store) ApplyOrderStatus(ctx context.Context, orderID, status, transactionID string) (Order, error) {
	switch status {
	case OrderPending, OrderPaid, OrderExpired, OrderFailed:
	default:
		return Order{}, fmt.Errorf("invalid order status %q", status)
	}

	var o Order
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		o, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE order_id = $1 FOR UPDATE`, orderID))
		if err != nil {
			return err
		}
		canMove := (status == OrderPaid && o.Status != OrderPaid) ||
			(o.Status == OrderPending && status != OrderPending)
		if !canMove {
			return nil
		}
		o, err = scanOrder(tx.QueryRow(ctx, `
			UPDATE orders SET
				status = $2,
				transaction_id = CASE WHEN $3 = '' THEN transaction_id ELSE $3 END,
				paid_at = CASE WHEN $2 = 'paid' THEN now() ELSE paid_at END,
				updated_at = now()
			WHERE id = $1
			RETURNING `+orderColumns, o.ID, status, transactionID))
		if err != nil {
			return err
		}
		if status == OrderPaid {
			_, err = tx.Exec(ctx, `UPDATE users SET member_since = COALESCE(member_since, now()) WHERE id = $1`, o.UserID)
		}
		return err
	})
	return o, err
}
