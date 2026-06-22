package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/hec-ovi/censurado-web/internal/store/sqlite"
	"github.com/hec-ovi/censurado-web/internal/store/storetest"
)

func TestSQLiteRepository(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	storetest.Run(t, repo)
}

func TestSQLiteSubmissionLog(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sub.db")
	repo, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	storetest.RunSubmissionLog(t, repo)
}
