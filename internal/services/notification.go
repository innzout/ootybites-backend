package services

import (
	"context"
	"fmt"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

// Notifications is the in-app notification service (reads + mark-read). Rows are
// created by the order flows via insertNotification.
type Notifications struct {
	db *db.Pool
}

// NewNotifications builds the service.
func NewNotifications(pool *db.Pool) *Notifications { return &Notifications{db: pool} }

// Feed bundles a notification list with the unread count.
type Feed struct {
	Notifications []models.Notification `json:"notifications"`
	Unread        int                   `json:"unread"`
}

// ForCustomer returns a customer's latest notifications + unread count.
func (s *Notifications) ForCustomer(ctx context.Context, customerID string) (*Feed, error) {
	return s.feed(ctx, `customer_id = $1`, customerID)
}

// ForAdmin returns the admin feed + unread count.
func (s *Notifications) ForAdmin(ctx context.Context) (*Feed, error) {
	return s.feed(ctx, `is_admin = true`, nil)
}

func (s *Notifications) feed(ctx context.Context, where string, arg any) (*Feed, error) {
	args := []any{}
	if arg != nil {
		args = append(args, arg)
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, title, body, order_id, is_read, created_at
		 FROM notifications WHERE `+where+` ORDER BY created_at DESC LIMIT 30`, args...)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	f := &Feed{Notifications: []models.Notification{}}
	for rows.Next() {
		var n models.Notification
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.OrderID, &n.IsRead, &n.CreatedAt); err != nil {
			return nil, err
		}
		if !n.IsRead {
			f.Unread++
		}
		f.Notifications = append(f.Notifications, n)
	}
	return f, rows.Err()
}

// MarkAllReadCustomer marks a customer's notifications read.
func (s *Notifications) MarkAllReadCustomer(ctx context.Context, customerID string) error {
	_, err := s.db.Exec(ctx, `UPDATE notifications SET is_read=true WHERE customer_id=$1 AND NOT is_read`, customerID)
	return err
}

// MarkAllReadAdmin marks the admin feed read.
func (s *Notifications) MarkAllReadAdmin(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE notifications SET is_read=true WHERE is_admin AND NOT is_read`)
	return err
}

// insertNotification writes one notification within a caller's transaction.
// Non-critical: order flows ignore its error so a notification hiccup never
// fails an order.
func insertNotification(ctx context.Context, tx pgx.Tx, customerID *string, isAdmin bool, title string, body *string, orderID *string) {
	_, _ = tx.Exec(ctx,
		`INSERT INTO notifications (customer_id, is_admin, title, body, order_id) VALUES ($1,$2,$3,$4,$5)`,
		customerID, isAdmin, title, body, orderID)
}
