package store

import (
	"context"
	"time"
)

// UserInfo is the public view of a user (no password hash).
type UserInfo struct {
	ID        int       `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateUser adds a user to an existing tenant. Emails are unique across all
// tenants because login is by email alone.
func (s *Store) CreateUser(ctx context.Context, tenantID int, email, role, passwordHash string) (UserInfo, error) {
	u := UserInfo{Email: email, Role: role}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO users (tenant_id, email, role, password_hash) VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at`,
		tenantID, email, role, passwordHash,
	).Scan(&u.ID, &u.CreatedAt)
	if err != nil {
		return UserInfo{}, mapErr(err)
	}
	return u, nil
}

func (s *Store) ListUsers(ctx context.Context, tenantID int) ([]UserInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, role, created_at FROM users WHERE tenant_id = $1 ORDER BY id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserInfo{}
	for rows.Next() {
		var u UserInfo
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
