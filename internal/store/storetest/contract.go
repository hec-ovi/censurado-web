// Package storetest provides a reusable conformance suite that every
// store.Repository implementation must pass. Running the identical suite against
// both SQLite and Postgres is what proves the store is swappable rather than
// merely claimed to be.
package storetest

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/store"
)

func mustArticle(t *testing.T, in domain.PublishInput, at time.Time) domain.Article {
	t.Helper()
	a, err := domain.NewArticle(in, at)
	if err != nil {
		t.Fatalf("build article: %v", err)
	}
	return a
}

func setEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func slugsOf(arts []domain.Article) []string {
	out := make([]string, len(arts))
	for i, a := range arts {
		out[i] = a.Slug
	}
	return out
}

func equalOrdered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Run executes the full Repository conformance suite against repo, which must be
// empty. Subtests run sequentially and share the seeded state.
func Run(t *testing.T, repo store.Repository) {
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	seed := []domain.Article{
		mustArticle(t, domain.PublishInput{Title: "Go 1.26 ships", Body: "b1", Author: "ada", Section: "tech", Topics: []string{"go", "release"}}, base),
		mustArticle(t, domain.PublishInput{Title: "Election results", Body: "b2", Author: "bo", Section: "politics", Topics: []string{"election"}}, base.Add(24*time.Hour)),
		mustArticle(t, domain.PublishInput{Title: "Markets dip", Body: "b3", Author: "ada", Section: "economics", Topics: []string{"markets", "go"}}, base.Add(48*time.Hour)),
	}
	for _, a := range seed {
		res, err := repo.Upsert(ctx, a)
		if err != nil {
			t.Fatalf("seed upsert %q: %v", a.Slug, err)
		}
		if !res.Created {
			t.Fatalf("seed upsert %q: Created=false, want true", a.Slug)
		}
		if res.Article.ID == "" {
			t.Fatalf("seed upsert %q: empty ID, want store-assigned", a.Slug)
		}
	}

	t.Run("Upsert dedups on content hash (idempotent publish)", func(t *testing.T) {
		res, err := repo.Upsert(ctx, seed[0])
		if err != nil {
			t.Fatalf("re-upsert: %v", err)
		}
		if res.Created {
			t.Errorf("Created=true on duplicate content hash, want false")
		}
		n, err := repo.Count(ctx, store.Filter{})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != len(seed) {
			t.Errorf("count = %d after duplicate upsert, want %d", n, len(seed))
		}
	})

	t.Run("BySlug returns the article, with topics, or ErrNotFound", func(t *testing.T) {
		got, err := repo.BySlug(ctx, seed[0].Slug)
		if err != nil {
			t.Fatalf("BySlug: %v", err)
		}
		if got.Title != seed[0].Title || got.ContentHash != seed[0].ContentHash {
			t.Errorf("BySlug returned wrong article: %+v", got)
		}
		if !setEqual(got.Topics, seed[0].Topics) {
			t.Errorf("topics roundtrip = %v, want %v", got.Topics, seed[0].Topics)
		}
		if _, err := repo.BySlug(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("BySlug(missing) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("Find by section", func(t *testing.T) {
		got, err := repo.Find(ctx, store.Filter{Section: "tech"})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if !equalOrdered(slugsOf(got), []string{seed[0].Slug}) {
			t.Errorf("section=tech -> %v, want [%s]", slugsOf(got), seed[0].Slug)
		}
	})

	t.Run("Find by author", func(t *testing.T) {
		got, err := repo.Find(ctx, store.Filter{Author: "ada"})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if !setEqual(slugsOf(got), []string{seed[0].Slug, seed[2].Slug}) {
			t.Errorf("author=ada -> %v, want {%s,%s}", slugsOf(got), seed[0].Slug, seed[2].Slug)
		}
	})

	t.Run("Find by topic (normalized join)", func(t *testing.T) {
		got, err := repo.Find(ctx, store.Filter{Topic: "go"})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if !setEqual(slugsOf(got), []string{seed[0].Slug, seed[2].Slug}) {
			t.Errorf("topic=go -> %v, want {%s,%s}", slugsOf(got), seed[0].Slug, seed[2].Slug)
		}
	})

	t.Run("Find by date range (inclusive From, exclusive To)", func(t *testing.T) {
		got, err := repo.Find(ctx, store.Filter{From: base.Add(24 * time.Hour), To: base.Add(48 * time.Hour)})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if !equalOrdered(slugsOf(got), []string{seed[1].Slug}) {
			t.Errorf("date range -> %v, want [%s]", slugsOf(got), seed[1].Slug)
		}
	})

	t.Run("Ordering and paging", func(t *testing.T) {
		newest, err := repo.Find(ctx, store.Filter{Order: store.NewestFirst})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		wantNewest := []string{seed[2].Slug, seed[1].Slug, seed[0].Slug}
		if !equalOrdered(slugsOf(newest), wantNewest) {
			t.Errorf("newest-first -> %v, want %v", slugsOf(newest), wantNewest)
		}
		oldest, err := repo.Find(ctx, store.Filter{Order: store.OldestFirst})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		wantOldest := []string{seed[0].Slug, seed[1].Slug, seed[2].Slug}
		if !equalOrdered(slugsOf(oldest), wantOldest) {
			t.Errorf("oldest-first -> %v, want %v", slugsOf(oldest), wantOldest)
		}
		page, err := repo.Find(ctx, store.Filter{Order: store.NewestFirst, Limit: 1, Offset: 1})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if !equalOrdered(slugsOf(page), []string{seed[1].Slug}) {
			t.Errorf("page(limit1,offset1) -> %v, want [%s]", slugsOf(page), seed[1].Slug)
		}
	})

	t.Run("Count respects filter and ignores paging", func(t *testing.T) {
		n, err := repo.Count(ctx, store.Filter{Author: "ada", Limit: 1, Offset: 1})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n != 2 {
			t.Errorf("count(author=ada) = %d, want 2 (paging ignored)", n)
		}
	})
}
