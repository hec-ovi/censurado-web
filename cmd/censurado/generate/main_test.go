package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/store/sqlite"
)

func seedDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "seed.db")
	repo, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer repo.Close()
	pub := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	a, err := domain.NewArticle(domain.PublishInput{
		Title: "Hello", Body: "world", Author: "ada", Section: "tech", PublishedAt: &pub,
	}, time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewArticle: %v", err)
	}
	if _, err := repo.Upsert(context.Background(), a); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return dbPath
}

// G8
func TestCmd_Run(t *testing.T) {
	t.Setenv("CENSURADO_DB", "")
	t.Setenv("CENSURADO_BASE_URL", "")

	t.Run("writes site and prints summary", func(t *testing.T) {
		db := seedDB(t)
		out := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := run([]string{"-db", db, "-out", out, "-base-url", "https://news.example"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("code = %d, want 0 (%s)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "written=") || !strings.Contains(stdout.String(), "scopes=") {
			t.Errorf("summary missing: %q", stdout.String())
		}
		if _, err := os.Stat(filepath.Join(out, "latest", "index.html")); err != nil {
			t.Errorf("site not written: %v", err)
		}
	})

	t.Run("missing -db is usage error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-base-url", "https://news.example"}, &stdout, &stderr); code != 2 {
			t.Errorf("code = %d, want 2", code)
		}
	})

	t.Run("missing -base-url is usage error", func(t *testing.T) {
		db := seedDB(t)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-db", db}, &stdout, &stderr); code != 2 {
			t.Errorf("code = %d, want 2", code)
		}
	})

	t.Run("bad db path is runtime error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		bad := filepath.Join(t.TempDir(), "no-such-dir", "x.db")
		if code := run([]string{"-db", bad, "-base-url", "https://news.example", "-out", t.TempDir()}, &stdout, &stderr); code != 1 {
			t.Errorf("code = %d, want 1", code)
		}
	})
}
