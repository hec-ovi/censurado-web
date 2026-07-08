package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hec-ovi/censurado-web-backend/store"
	"github.com/hec-ovi/censurado-web-backend/store/sqlite"
	"github.com/hec-ovi/censurado-web/internal/generate"
)

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func TestSeedFrontendText(t *testing.T) {
	ctx := context.Background()
	repo := openStore(t)
	catalog := generate.FrontendTextSeed()
	wantRows := 2 * len(catalog) // en + es per key

	// First seed: every row is written, nothing skipped.
	written, skipped, err := seedFrontendText(ctx, repo, false)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if written != wantRows || skipped != 0 {
		t.Fatalf("first seed wrote %d skipped %d, want %d/0", written, skipped, wantRows)
	}

	// Both language maps carry every key with the catalog value.
	en, err := repo.Text(ctx, store.ScopeFrontend, "en")
	if err != nil {
		t.Fatalf("Text(en): %v", err)
	}
	es, err := repo.Text(ctx, store.ScopeFrontend, "es")
	if err != nil {
		t.Fatalf("Text(es): %v", err)
	}
	for _, e := range catalog {
		if en[e.Key] != e.En {
			t.Errorf("en[%s] = %q, want %q", e.Key, en[e.Key], e.En)
		}
		if es[e.Key] != e.Es {
			t.Errorf("es[%s] = %q, want %q", e.Key, es[e.Key], e.Es)
		}
	}

	// Re-seed without force is idempotent: every row already exists, so all are skipped.
	written, skipped, err = seedFrontendText(ctx, repo, false)
	if err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if written != 0 || skipped != wantRows {
		t.Fatalf("re-seed wrote %d skipped %d, want 0/%d", written, skipped, wantRows)
	}

	// An operator edit survives a non-force re-seed.
	const key = "footer.disclosure_heading"
	if _, err := repo.UpsertText(ctx, store.ScopeFrontend, store.TextEntry{Key: key, Lang: "es", Value: "Aviso legal"}); err != nil {
		t.Fatalf("operator edit: %v", err)
	}
	if _, _, err := seedFrontendText(ctx, repo, false); err != nil {
		t.Fatalf("re-seed after edit: %v", err)
	}
	es, _ = repo.Text(ctx, store.ScopeFrontend, "es")
	if es[key] != "Aviso legal" {
		t.Errorf("non-force re-seed clobbered an operator edit: %q", es[key])
	}

	// -force resets every base row back to the shipped catalog.
	written, skipped, err = seedFrontendText(ctx, repo, true)
	if err != nil {
		t.Fatalf("force seed: %v", err)
	}
	if written != wantRows || skipped != 0 {
		t.Fatalf("force seed wrote %d skipped %d, want %d/0", written, skipped, wantRows)
	}
	es, _ = repo.Text(ctx, store.ScopeFrontend, "es")
	if es[key] != "Aviso editorial" {
		t.Errorf("force seed did not reset the edited row: %q", es[key])
	}
}
