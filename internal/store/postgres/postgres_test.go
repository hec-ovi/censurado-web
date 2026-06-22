package postgres_test

import (
	"os"
	"testing"

	"github.com/hec-ovi/censurado-web/internal/store/postgres"
	"github.com/hec-ovi/censurado-web/internal/store/storetest"
)

// TestPostgresRepository runs the shared conformance suite against a real
// Postgres when CENSURADO_TEST_POSTGRES_DSN is set (CI and the local docker
// harness provide it), and skips otherwise so the default `go test ./...` stays
// dependency-free. A green run here is the proof that the store is swappable.
func TestPostgresRepository(t *testing.T) {
	dsn := os.Getenv("CENSURADO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set CENSURADO_TEST_POSTGRES_DSN to run the Postgres conformance suite")
	}
	repo, err := postgres.Open(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	storetest.Run(t, repo)
}
