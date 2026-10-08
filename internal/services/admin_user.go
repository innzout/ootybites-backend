package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/innzout/ootybites/internal/password"
	"github.com/jackc/pgx/v5"
)

// AdminGuardError is a user-facing rule violation for admin-user management
// (e.g. removing the last super-admin), distinct from an internal failure.
type AdminGuardError struct{ Message string }

func (e *AdminGuardError) Error() string { return e.Message }

// validAdminRoles gates the role column (mirrors the DB check constraint).
var validAdminRoles = map[string]bool{"super_admin": true, "manager": true}

// Admins is the admin-user management service (super-admin only, enforced by
// route middleware). Passwords are hashed here; roles are validated.
type Admins struct {
	db *db.Pool
}

// NewAdmins builds the admin-user service.
func NewAdmins(pool *db.Pool) *Admins { return &Admins{db: pool} }

// AdminInput carries create/update fields. Password is optional on update
// (blank = keep the existing one).
type AdminInput struct {
	Username string
	Name     *string
	Password string
	Role     string
}

func (in AdminInput) validate(requirePassword bool) map[string]string {
	f := map[string]string{}
	if strings.TrimSpace(in.Username) == "" {
		f["username"] = "Username is required"
	} else if len(strings.TrimSpace(in.Username)) < 3 {
		f["username"] = "Username must be at least 3 characters"
	}
	if !validAdminRoles[in.Role] {
		f["role"] = "Pick a valid role"
	}
	if requirePassword || in.Password != "" {
		if len(in.Password) < 8 {
			f["password"] = "Password must be at least 8 characters"
		}
	}
	if len(f) == 0 {
		return nil
	}
	return f
}

// List returns all admin users (newest first).
func (s *Admins) List(ctx context.Context) ([]models.Admin, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, username, name, role, created_at FROM admins ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list admins: %w", err)
	}
	defer rows.Close()
	out := []models.Admin{}
	for rows.Next() {
		var m models.Admin
		if err := rows.Scan(&m.ID, &m.Username, &m.Name, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns one admin by id.
func (s *Admins) Get(ctx context.Context, id string) (*models.Admin, error) {
	var m models.Admin
	err := s.db.QueryRow(ctx,
		`SELECT id, username, name, role, created_at FROM admins WHERE id=$1`, id).
		Scan(&m.ID, &m.Username, &m.Name, &m.Role, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// Create inserts a new admin with a hashed password.
func (s *Admins) Create(ctx context.Context, in AdminInput) (*models.Admin, map[string]string, error) {
	if fields := in.validate(true); fields != nil {
		return nil, fields, nil
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, nil, err
	}
	var id string
	err = s.db.QueryRow(ctx,
		`INSERT INTO admins (username, name, password_hash, role) VALUES ($1,$2,$3,$4) RETURNING id`,
		strings.ToLower(strings.TrimSpace(in.Username)), in.Name, hash, in.Role).Scan(&id)
	if err != nil {
		return nil, nil, &AdminGuardError{Message: "Could not create admin (username may be taken)"}
	}
	m, err := s.Get(ctx, id)
	return m, nil, err
}

// Update mutates an admin; a blank password keeps the existing one. Demoting the
// last super-admin is refused so the panel can never be locked out.
func (s *Admins) Update(ctx context.Context, id string, in AdminInput) (*models.Admin, map[string]string, error) {
	if fields := in.validate(false); fields != nil {
		return nil, fields, nil
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if current.Role == "super_admin" && in.Role != "super_admin" {
		n, err := s.countSuperAdmins(ctx)
		if err != nil {
			return nil, nil, err
		}
		if n <= 1 {
			return nil, nil, &AdminGuardError{Message: "Cannot change the role of the last super-admin"}
		}
	}

	if strings.TrimSpace(in.Password) != "" {
		hash, err := password.Hash(in.Password)
		if err != nil {
			return nil, nil, err
		}
		if _, err := s.db.Exec(ctx, `UPDATE admins SET password_hash=$2 WHERE id=$1`, id, hash); err != nil {
			return nil, nil, err
		}
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE admins SET username=$2, name=$3, role=$4 WHERE id=$1`,
		id, strings.ToLower(strings.TrimSpace(in.Username)), in.Name, in.Role)
	if err != nil {
		return nil, nil, &AdminGuardError{Message: "Could not update admin (username may be taken)"}
	}
	if tag.RowsAffected() == 0 {
		return nil, nil, ErrNotFound
	}
	m, err := s.Get(ctx, id)
	return m, nil, err
}

// Delete removes an admin. It refuses to delete the caller's own account or the
// last remaining super-admin.
func (s *Admins) Delete(ctx context.Context, id, currentAdminID string) error {
	if id == currentAdminID {
		return &AdminGuardError{Message: "You cannot delete your own account"}
	}
	target, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if target.Role == "super_admin" {
		n, err := s.countSuperAdmins(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return &AdminGuardError{Message: "Cannot delete the last super-admin"}
		}
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM admins WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete admin: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Admins) countSuperAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM admins WHERE role='super_admin'`).Scan(&n)
	return n, err
}
