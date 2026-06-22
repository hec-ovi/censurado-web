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

	// Appended last so it never perturbs the seed-based count assertions above.
	// Production feeds time.Now() (sub-second) for both published_at and
	// created_at, where the engines would naturally diverge: SQLite's RFC3339
	// drops sub-second, Postgres TIMESTAMPTZ keeps microseconds. Both adapters
	// resolve this by truncating to whole seconds on write, so the instant
	// returned through the Repository interface is engine-independent. This case
	// fails if either adapter stops truncating (Postgres returning microseconds
	// -> Nanosecond() != 0).
	t.Run("Sub-second timestamps persist at whole-second resolution (engine-independent)", func(t *testing.T) {
		subSec := time.Date(2026, 6, 5, 10, 20, 30, 123456789, time.UTC)
		pub := time.Date(2026, 6, 4, 8, 15, 30, 987654321, time.UTC)
		art := mustArticle(t, domain.PublishInput{
			Title:       "Timestamp precision check",
			Body:        "sub-second",
			Author:      "chronos",
			Section:     "meta",
			Topics:      []string{"precision"},
			PublishedAt: &pub,
		}, subSec)

		res, err := repo.Upsert(ctx, art)
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if !res.Created {
			t.Fatalf("Created = false, want true (distinct content hash)")
		}
		assertWholeSecond(t, "Upsert", res.Article, pub, subSec)

		got, err := repo.BySlug(ctx, art.Slug)
		if err != nil {
			t.Fatalf("BySlug: %v", err)
		}
		assertWholeSecond(t, "BySlug", got, pub, subSec)
	})
}

// assertWholeSecond verifies the article's PublishedAt and CreatedAt carry no
// sub-second component and equal the truncated inputs, regardless of which
// engine stored them.
func assertWholeSecond(t *testing.T, stage string, a domain.Article, pub, created time.Time) {
	t.Helper()
	if ns := a.PublishedAt.Nanosecond(); ns != 0 {
		t.Errorf("%s: PublishedAt sub-second = %dns, want 0 (both adapters must truncate)", stage, ns)
	}
	if ns := a.CreatedAt.Nanosecond(); ns != 0 {
		t.Errorf("%s: CreatedAt sub-second = %dns, want 0 (both adapters must truncate)", stage, ns)
	}
	if !a.PublishedAt.UTC().Equal(pub.Truncate(time.Second)) {
		t.Errorf("%s: PublishedAt = %v, want %v (truncated to whole second)", stage, a.PublishedAt.UTC(), pub.Truncate(time.Second))
	}
	if !a.CreatedAt.UTC().Equal(created.Truncate(time.Second)) {
		t.Errorf("%s: CreatedAt = %v, want %v (truncated to whole second)", stage, a.CreatedAt.UTC(), created.Truncate(time.Second))
	}
}

// RunSubmissionLog executes the SubmissionLog conformance suite against log,
// which must back an empty submissions table. Running the identical suite
// against both SQLite and Postgres is what proves the two adapters encode and
// roundtrip submissions identically. The production caller writes
// time.Now().UTC() (sub-second precision), where the engines would naturally
// diverge: SQLite's RFC3339 drops sub-second, Postgres TIMESTAMPTZ keeps
// microseconds. Both adapters resolve this by truncating CreatedAt to whole
// seconds on write, so the roundtrip is engine-independent for any input. The
// sub-second case below pins that contract: it fails if either adapter leaks a
// sub-second component. It does not call t.Parallel so subtests share state.
func RunSubmissionLog(t *testing.T, log store.SubmissionLog) {
	ctx := context.Background()

	t.Run("FindSubmission on missing key reports not found", func(t *testing.T) {
		got, found, err := log.FindSubmission(ctx, "no-such-key")
		if err != nil {
			t.Fatalf("FindSubmission: %v", err)
		}
		if found {
			t.Errorf("found = true for missing key, want false")
		}
		if got.IdempotencyKey != "" || got.ContentHash != "" || got.ArticleID != "" ||
			got.Slug != "" || got.Author != "" || len(got.Scopes) != 0 || !got.CreatedAt.IsZero() {
			t.Errorf("got = %+v for missing key, want zero Submission", got)
		}
	})

	first := store.Submission{
		IdempotencyKey: "idem-1",
		ContentHash:    "hash-1",
		ArticleID:      "42",
		Slug:           "go-1-26-ships",
		Author:         "ada",
		Scopes:         []string{"section:tech", "author:ada", "topic:go"},
		CreatedAt:      time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}

	t.Run("RecordSubmission then FindSubmission roundtrips every field", func(t *testing.T) {
		if err := log.RecordSubmission(ctx, first); err != nil {
			t.Fatalf("RecordSubmission: %v", err)
		}
		got, found, err := log.FindSubmission(ctx, first.IdempotencyKey)
		if err != nil {
			t.Fatalf("FindSubmission: %v", err)
		}
		if !found {
			t.Fatalf("found = false after RecordSubmission, want true")
		}
		assertSubmissionEqual(t, got, first)
	})

	second := store.Submission{
		IdempotencyKey: "idem-2",
		ContentHash:    "hash-2",
		ArticleID:      "7",
		Slug:           "markets-dip",
		Author:         "bo",
		Scopes:         []string{"section:economics", "topic:markets"},
		CreatedAt:      time.Date(2026, 6, 2, 9, 30, 0, 0, time.UTC),
	}

	t.Run("A distinct key roundtrips independently", func(t *testing.T) {
		if err := log.RecordSubmission(ctx, second); err != nil {
			t.Fatalf("RecordSubmission: %v", err)
		}
		got, found, err := log.FindSubmission(ctx, second.IdempotencyKey)
		if err != nil {
			t.Fatalf("FindSubmission: %v", err)
		}
		if !found {
			t.Fatalf("found = false for second key, want true")
		}
		assertSubmissionEqual(t, got, second)

		// The first record is untouched by the second write.
		gotFirst, found, err := log.FindSubmission(ctx, first.IdempotencyKey)
		if err != nil {
			t.Fatalf("FindSubmission(first): %v", err)
		}
		if !found {
			t.Fatalf("found = false for first key after second write, want true")
		}
		assertSubmissionEqual(t, gotFirst, first)
	})

	t.Run("Empty scopes roundtrips as no scopes", func(t *testing.T) {
		empty := store.Submission{
			IdempotencyKey: "idem-empty",
			ContentHash:    "hash-empty",
			ArticleID:      "0",
			Slug:           "no-scopes",
			Author:         "cy",
			Scopes:         nil,
			CreatedAt:      time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC),
		}
		if err := log.RecordSubmission(ctx, empty); err != nil {
			t.Fatalf("RecordSubmission: %v", err)
		}
		got, found, err := log.FindSubmission(ctx, empty.IdempotencyKey)
		if err != nil {
			t.Fatalf("FindSubmission: %v", err)
		}
		if !found {
			t.Fatalf("found = false for empty-scopes key, want true")
		}
		if len(got.Scopes) != 0 {
			t.Errorf("scopes = %v, want empty", got.Scopes)
		}
		assertSubmissionEqual(t, got, empty)
	})

	t.Run("Sub-second CreatedAt truncates to whole seconds identically", func(t *testing.T) {
		// Production writes h.now().UTC() with sub-second precision (publish.go).
		// Both adapters must store it the same way; they agree by truncating to
		// whole seconds, independent of the engine's native time resolution
		// (SQLite RFC3339 vs Postgres microsecond TIMESTAMPTZ). This case fails
		// if either adapter keeps a sub-second component.
		raw := time.Date(2026, 6, 4, 8, 15, 30, 123456789, time.UTC)
		sub := store.Submission{
			IdempotencyKey: "idem-subsecond",
			ContentHash:    "hash-subsecond",
			ArticleID:      "99",
			Slug:           "sub-second",
			Author:         "di",
			Scopes:         []string{"section:tech"},
			CreatedAt:      raw,
		}
		if err := log.RecordSubmission(ctx, sub); err != nil {
			t.Fatalf("RecordSubmission: %v", err)
		}
		got, found, err := log.FindSubmission(ctx, sub.IdempotencyKey)
		if err != nil {
			t.Fatalf("FindSubmission: %v", err)
		}
		if !found {
			t.Fatalf("found = false after RecordSubmission, want true")
		}
		want := raw.Truncate(time.Second) // 2026-06-04T08:15:30Z, no sub-second
		if !got.CreatedAt.UTC().Equal(want) {
			t.Errorf("CreatedAt = %v, want %v (truncated to whole second; engines must agree)", got.CreatedAt.UTC(), want)
		}
		if ns := got.CreatedAt.UTC().Nanosecond(); ns != 0 {
			t.Errorf("CreatedAt sub-second = %dns, want 0 (both adapters must truncate identically)", ns)
		}
	})

	t.Run("RecordSubmission on a duplicate key errors", func(t *testing.T) {
		if err := log.RecordSubmission(ctx, first); err == nil {
			t.Errorf("RecordSubmission(duplicate key) err = nil, want non-nil (primary-key violation)")
		}
	})
}

func assertSubmissionEqual(t *testing.T, got, want store.Submission) {
	t.Helper()
	if got.IdempotencyKey != want.IdempotencyKey {
		t.Errorf("IdempotencyKey = %q, want %q", got.IdempotencyKey, want.IdempotencyKey)
	}
	if got.ContentHash != want.ContentHash {
		t.Errorf("ContentHash = %q, want %q", got.ContentHash, want.ContentHash)
	}
	if got.ArticleID != want.ArticleID {
		t.Errorf("ArticleID = %q, want %q", got.ArticleID, want.ArticleID)
	}
	if got.Slug != want.Slug {
		t.Errorf("Slug = %q, want %q", got.Slug, want.Slug)
	}
	if got.Author != want.Author {
		t.Errorf("Author = %q, want %q", got.Author, want.Author)
	}
	if !setEqual(got.Scopes, want.Scopes) {
		t.Errorf("Scopes = %v, want %v", got.Scopes, want.Scopes)
	}
	if !got.CreatedAt.UTC().Equal(want.CreatedAt.UTC()) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt.UTC(), want.CreatedAt.UTC())
	}
}
