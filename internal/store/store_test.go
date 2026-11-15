package store

import (
	"context"
	"os"
	"sync"
	"testing"
)

// openTestStore connects to TEST_DATABASE_URL and migrates it. Integration
// tests are skipped when the variable is not set.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	st, err := Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// Several replicas starting at once must not apply a migration twice or fail
// on each other: the advisory lock serialises them and applied versions are skipped.
func TestMigrateConcurrentAndIdempotent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.Migrate(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}

	names, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	var applied int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != len(names) {
		t.Fatalf("schema_migrations has %d rows, want %d", applied, len(names))
	}

	latest, err := LatestMigration()
	if err != nil || latest != names[len(names)-1][:len(names[len(names)-1])-len(".sql")] {
		t.Fatalf("LatestMigration = %q, %v", latest, err)
	}
}
