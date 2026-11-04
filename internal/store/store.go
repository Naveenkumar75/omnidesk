package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"

	"github.com/lib/pq"
)

//go:embed migrations
var migrationsFS embed.FS

var ErrConflict = errors.New("tenant domain or user email already exists")
var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

func Open(ctx context.Context, url string) (*Store, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Migrate applies the embedded SQL files in filename order. Statements are
// idempotent (IF NOT EXISTS), so it is safe to run on every start.
func (s *Store) Migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, string(b)); err != nil {
			return fmt.Errorf("migration %s: %w", n, err)
		}
	}
	return nil
}

type User struct {
	ID           int
	TenantID     int
	Email        string
	Role         string
	PasswordHash string
}

// CreateTenantWithAdmin creates a tenant and its first admin user atomically,
// so a failure can never leave an orphaned tenant that nobody can log into.
func (s *Store) CreateTenantWithAdmin(ctx context.Context, name, domain, email, passwordHash string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback() // no-op after a successful Commit

	var tenantID int
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO tenants (name, domain) VALUES ($1, $2) RETURNING id`, name, domain,
	).Scan(&tenantID); err != nil {
		return User{}, mapErr(err)
	}

	u := User{TenantID: tenantID, Email: email, Role: "admin", PasswordHash: passwordHash}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO users (tenant_id, email, role, password_hash) VALUES ($1, $2, $3, $4) RETURNING id`,
		tenantID, email, u.Role, passwordHash,
	).Scan(&u.ID); err != nil {
		return User{}, mapErr(err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, email, role, password_hash FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Role, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func mapErr(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return ErrConflict
	}
	return err
}
