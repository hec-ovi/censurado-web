package publish_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web/internal/publish"
	"github.com/hec-ovi/censurado-web/internal/store"
	"github.com/hec-ovi/censurado-web/internal/store/sqlite"
)

const (
	adaSecret = "ada-secret-value-1234567890"
	roSecret  = "readonly-secret-1234567890"
	validBody = `{"title":"Go 1.26 ships","body":"# Hello\n\nbody text","author":"ada","section":"tech","topics":["go","release"]}`
)

func newHandler(t *testing.T) (*publish.Handler, store.Repository) {
	t.Helper()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "pub.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	auth := publish.NewStaticKeyAuth()
	auth.Add("ak_ada", publish.HashSecret(adaSecret), "ada", publish.ScopeWrite)
	auth.Add("ak_ro", publish.HashSecret(roSecret), "ro", "articles:read")

	now := func() time.Time { return time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC) }
	// *sqlite.Store satisfies both the article Repository and the SubmissionLog,
	// so the same value serves as both arguments.
	return publish.NewHandler(repo, repo, auth, now), repo
}

func post(t *testing.T, h http.Handler, token, idemKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/articles", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeCreate(t *testing.T, rec *httptest.ResponseRecorder) (id, slug string) {
	t.Helper()
	var got struct{ ID, Slug string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	return got.ID, got.Slug
}

func TestPublish_RejectsBadRequests(t *testing.T) {
	h, _ := newHandler(t)
	adaToken := "ak_ada." + adaSecret

	t.Run("no auth -> 401", func(t *testing.T) {
		if rec := post(t, h, "", "k1", validBody); rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("bad token -> 401", func(t *testing.T) {
		if rec := post(t, h, "ak_ada.wrong", "k1", validBody); rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("read-only scope -> 403", func(t *testing.T) {
		if rec := post(t, h, "ak_ro."+roSecret, "k1", validBody); rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})
	t.Run("missing Idempotency-Key -> 400", func(t *testing.T) {
		if rec := post(t, h, adaToken, "", validBody); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("unknown field -> 400", func(t *testing.T) {
		body := `{"title":"x","body":"y","author":"ada","section":"tech","bogus":1}`
		if rec := post(t, h, adaToken, "k1", body); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("missing required field -> 422 with fields", func(t *testing.T) {
		body := `{"body":"y","author":"ada","section":"tech"}`
		rec := post(t, h, adaToken, "k1", body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "title") {
			t.Errorf("expected a field error for title, got %s", rec.Body.String())
		}
	})
	t.Run("author mismatch -> 403", func(t *testing.T) {
		body := `{"title":"x","body":"y","author":"bo","section":"tech"}`
		if rec := post(t, h, adaToken, "k1", body); rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})
}

func TestPublish_CreateIdempotencyAndDedup(t *testing.T) {
	h, repo := newHandler(t)
	adaToken := "ak_ada." + adaSecret
	ctx := context.Background()

	// First create.
	rec := post(t, h, adaToken, "key-1", validBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	id1, slug1 := decodeCreate(t, rec)
	if id1 == "" || slug1 == "" {
		t.Fatalf("create returned empty id/slug: %s", rec.Body.String())
	}
	if got, err := repo.BySlug(ctx, slug1); err != nil {
		t.Fatalf("article not stored: %v", err)
	} else if got.Author != "ada" || got.Section != "tech" {
		t.Errorf("stored article wrong: %+v", got)
	}

	// Same key + same payload -> idempotent replay, 200, same identity, no new row.
	rec = post(t, h, adaToken, "key-1", validBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", rec.Code)
	}
	id2, slug2 := decodeCreate(t, rec)
	if id2 != id1 || slug2 != slug1 {
		t.Errorf("replay returned %s/%s, want %s/%s", id2, slug2, id1, slug1)
	}
	assertCount(t, repo, 1)

	// Different key + same payload -> content-hash dedup, 200, same article, still one row.
	rec = post(t, h, adaToken, "key-2", validBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("dedup status = %d, want 200", rec.Code)
	}
	if _, slug := decodeCreate(t, rec); slug != slug1 {
		t.Errorf("dedup slug = %s, want %s", slug, slug1)
	}
	assertCount(t, repo, 1)

	// Same key + different payload -> idempotency conflict.
	other := `{"title":"Different headline entirely","body":"other","author":"ada","section":"tech"}`
	rec = post(t, h, adaToken, "key-1", other)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("key reuse status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "idempotency_key_reused") {
		t.Errorf("expected idempotency_key_reused, got %s", rec.Body.String())
	}
	assertCount(t, repo, 1)

	// A genuinely new article -> 201, count grows.
	rec = post(t, h, adaToken, "key-3", `{"title":"Markets dip","body":"b","author":"ada","section":"economics"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("second create status = %d, want 201", rec.Code)
	}
	assertCount(t, repo, 2)
}

func assertCount(t *testing.T, repo store.Repository, want int) {
	t.Helper()
	n, err := repo.Count(context.Background(), store.Filter{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != want {
		t.Errorf("article count = %d, want %d", n, want)
	}
}
