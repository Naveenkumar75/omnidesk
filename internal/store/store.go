package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"

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

// migrationLockKey is an arbitrary constant for pg_advisory_lock, so that two
// replicas starting at the same time cannot run migrations concurrently.
const migrationLockKey = 72_610_001

// Migrate applies embedded SQL files that have not been applied yet, in
// filename order. Each file runs in its own transaction together with the
// insert into schema_migrations, so a failed migration leaves no partial
// schema and is retried on the next start. Applied files are never re-run, so
// migrations can use plain ALTER TABLE statements.
func (s *Store) Migrate(ctx context.Context) error {
	names, err := migrationNames()
	if err != nil {
		return err
	}

	conn, err := s.db.Conn(ctx) // the advisory lock is per session, so pin one connection
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey) //nolint:errcheck

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}

	applied := map[string]bool{}
	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, n := range names {
		version := strings.TrimSuffix(n, ".sql")
		if applied[version] {
			continue
		}
		b, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(b)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", n, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", n, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", n, err)
		}
	}
	return nil
}

// LatestMigration is the version the schema reaches after Migrate. Other
// services sharing the database (the AI service) wait for it before starting.
func LatestMigration() (string, error) {
	names, err := migrationNames()
	if err != nil || len(names) == 0 {
		return "", err
	}
	return strings.TrimSuffix(names[len(names)-1], ".sql"), nil
}

func migrationNames() ([]string, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
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
