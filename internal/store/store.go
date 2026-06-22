// Package store defines the source-of-truth contract for articles. The interface
// is expressed purely in domain terms; no SQL, dialect, or storage detail leaks
// through it, which is what keeps the store swappable. SQLite is the default
// adapter; a Postgres adapter satisfies the same interface and is exercised by
// the same conformance suite (see storetest) so the swap is proven, not assumed.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
)

// ErrNotFound is returned when a lookup matches no article.
var ErrNotFound = errors.New("store: article not found")

// Order controls result ordering by publish time.
type Order int

const (
	// NewestFirst orders by descending publish time (the default).
	NewestFirst Order = iota
	// OldestFirst orders by ascending publish time.
	OldestFirst
)

// Filter selects articles by the stable hot axes. Zero-valued fields are
// ignored, so the empty Filter matches everything.
type Filter struct {
	Section string
	Author  string
	Topic   string
	From    time.Time // inclusive lower bound on PublishedAt; zero = open
	To      time.Time // exclusive upper bound on PublishedAt; zero = open
	Order   Order
	Limit   int // 0 = no limit
	Offset  int
}

// UpsertResult reports the stored article and whether it was newly created.
// Created is false when an article with the same content hash already existed,
// which is what makes publishing both idempotent and deduplicated.
type UpsertResult struct {
	Article domain.Article
	Created bool
}

// Repository is the article source of truth. Implementations must be safe for a
// single writer with concurrent readers.
type Repository interface {
	// Upsert stores an article, deduplicating on content hash: a later call with
	// the same hash returns the existing row with Created=false.
	Upsert(ctx context.Context, a domain.Article) (UpsertResult, error)
	// BySlug returns the article with the given slug, or ErrNotFound.
	BySlug(ctx context.Context, slug string) (domain.Article, error)
	// Find returns articles matching the filter, ordered and paged.
	Find(ctx context.Context, f Filter) ([]domain.Article, error)
	// Count returns how many articles match the filter, ignoring Limit/Offset.
	Count(ctx context.Context, f Filter) (int, error)
	// Close releases resources held by the store.
	Close() error
}
