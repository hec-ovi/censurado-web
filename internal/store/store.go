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
//
// Axes AND together. The scalar Section/Author/Topic fields are the stable public
// surface the generator relies on. The plural Sections/Authors/Topics slices and
// the Query field are an admin-only widening (Phase 5): within a single plural
// axis the values OR (membership), and a scalar field and its plural counterpart
// are independent constraints that AND together (the plural never overrides the
// scalar). Blank or whitespace-only entries inside a slice are ignored, and a
// slice of only blanks is treated as no constraint.
type Filter struct {
	// Scalar hot axes. The public generator uses ONLY these (plus From/To/Order/
	// paging); they must keep their exact meaning.
	Section string
	Author  string
	Topic   string

	// Multi-value axes (admin only). Each is an OR-within-axis membership test:
	//   Sections -> article.section IN (Sections)
	//   Authors  -> article.author  IN (Authors)
	//   Topics   -> article has AT LEAST ONE topic IN (Topics)
	// They AND with each other and with the scalar fields above. Blank entries are
	// ignored; an all-blank (or empty) slice imposes no constraint.
	Sections []string
	Authors  []string
	Topics   []string

	// Query is an admin-only full-text filter: a case-insensitive substring match
	// over title OR body. Blank/whitespace-only imposes no constraint. The match is
	// ASCII case-insensitive only; non-ASCII (accented) letters match
	// case-sensitively. See the adapters' buildSelect for why (sqlite vs postgres
	// lower() differ on unicode folding) and the exact, parity-safe contract.
	Query string

	From   time.Time // inclusive lower bound on PublishedAt; zero = open
	To     time.Time // exclusive upper bound on PublishedAt; zero = open
	Order  Order
	Limit  int // 0 = no limit
	Offset int
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

// Submission is the append-only record of one publish attempt. It is both the
// audit log entry and the idempotency ledger entry, so one write covers both.
type Submission struct {
	IdempotencyKey string
	ContentHash    string
	ArticleID      string
	Slug           string
	Author         string
	Scopes         []string
	CreatedAt      time.Time
}

// SubmissionLog records publish attempts and looks them up by idempotency key.
// It is a separate concern from the article Repository so the article contract
// stays focused, even when one adapter implements both.
type SubmissionLog interface {
	// FindSubmission returns a prior submission for the key, or found=false.
	FindSubmission(ctx context.Context, idempotencyKey string) (Submission, bool, error)
	// RecordSubmission appends a submission record.
	RecordSubmission(ctx context.Context, s Submission) error
}
