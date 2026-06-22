// Package postgres implements the store.Repository contract on PostgreSQL via
// the pgx stdlib driver. It is not the default store; it exists so the same
// conformance suite (storetest) runs against both SQLite and Postgres, proving
// the source of truth is swappable behind the repository interface rather than
// the swap being merely documented.
package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/store"
)

//go:embed schema.sql
var schema string

// Store is a Postgres-backed Repository.
type Store struct {
	db *sql.DB
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface {
	Scan(dest ...any) error
}

// argBuilder assigns positional placeholders ($1, $2, ...) as args are added.
type argBuilder struct{ args []any }

func (a *argBuilder) next(v any) string {
	a.args = append(a.args, v)
	return "$" + strconv.Itoa(len(a.args))
}

// Open connects to Postgres using a pgx DSN (postgres://...), applies the
// schema, and verifies connectivity with a short retry to tolerate a database
// that is still starting up.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	var pingErr error
	for i := 0; i < 20; i++ {
		if pingErr = db.PingContext(context.Background()); pingErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if pingErr != nil {
		db.Close()
		return nil, fmt.Errorf("postgres ping: %w", pingErr)
	}
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Upsert inserts the article, or returns the existing one when its content hash
// already exists (idempotent, deduplicated). Topics are written only on create.
func (s *Store) Upsert(ctx context.Context, a domain.Article) (store.UpsertResult, error) {
	meta, err := marshalMeta(a.Metadata)
	if err != nil {
		return store.UpsertResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.UpsertResult{}, err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO articles (slug,title,body,author,section,published_at,content_hash,metadata,created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)
		 ON CONFLICT (content_hash) DO NOTHING
		 RETURNING id`,
		a.Slug, a.Title, a.Body, a.Author, a.Section,
		// Whole-second resolution so both adapters return the identical instant
		// through the Repository interface. TIMESTAMPTZ would otherwise keep
		// microseconds while SQLite's RFC3339 drops them, diverging on the
		// sub-second inputs production feeds (domain.NewArticle uses time.Now()).
		a.PublishedAt.UTC().Truncate(time.Second), a.ContentHash, meta, a.CreatedAt.UTC().Truncate(time.Second),
	).Scan(&id)

	created := true
	if errors.Is(err, sql.ErrNoRows) {
		created = false
		if err = tx.QueryRowContext(ctx,
			`SELECT id FROM articles WHERE content_hash = $1`, a.ContentHash).Scan(&id); err != nil {
			return store.UpsertResult{}, fmt.Errorf("lookup existing: %w", err)
		}
	} else if err != nil {
		return store.UpsertResult{}, fmt.Errorf("insert article: %w", err)
	}

	if created {
		for _, topic := range a.Topics {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO article_topics (article_id,topic) VALUES ($1,$2)`, id, topic); err != nil {
				return store.UpsertResult{}, fmt.Errorf("insert topic: %w", err)
			}
		}
	}

	stored, err := getByID(ctx, tx, id)
	if err != nil {
		return store.UpsertResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.UpsertResult{}, err
	}
	return store.UpsertResult{Article: stored, Created: created}, nil
}

// BySlug returns the article with the given slug, or store.ErrNotFound.
func (s *Store) BySlug(ctx context.Context, slug string) (domain.Article, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM articles WHERE slug = $1`, slug).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Article{}, store.ErrNotFound
	}
	if err != nil {
		return domain.Article{}, err
	}
	return getByID(ctx, s.db, id)
}

// Find returns articles matching the filter, ordered and paged.
func (s *Store) Find(ctx context.Context, f store.Filter) ([]domain.Article, error) {
	query, args := buildSelect(f, false)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var arts []domain.Article
	var ids []int64
	for rows.Next() {
		a, id, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		arts = append(arts, a)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	topics, err := loadTopics(ctx, s.db, ids)
	if err != nil {
		return nil, err
	}
	for i := range arts {
		if tp := topics[ids[i]]; tp != nil {
			arts[i].Topics = tp
		}
	}
	return arts, nil
}

// Count returns how many articles match the filter, ignoring paging.
func (s *Store) Count(ctx context.Context, f store.Filter) (int, error) {
	query, args := buildSelect(f, true)
	var n int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func buildSelect(f store.Filter, count bool) (string, []any) {
	var b strings.Builder
	ab := &argBuilder{}
	if count {
		b.WriteString("SELECT COUNT(*) FROM articles a")
	} else {
		b.WriteString("SELECT a.id,a.slug,a.title,a.body,a.author,a.section,a.published_at,a.content_hash,a.metadata,a.created_at FROM articles a")
	}
	if f.Topic != "" {
		fmt.Fprintf(&b, " JOIN article_topics t ON t.article_id = a.id AND t.topic = %s", ab.next(f.Topic))
	}
	b.WriteString(" WHERE 1=1")
	if f.Section != "" {
		fmt.Fprintf(&b, " AND a.section = %s", ab.next(f.Section))
	}
	if f.Author != "" {
		fmt.Fprintf(&b, " AND a.author = %s", ab.next(f.Author))
	}
	if !f.From.IsZero() {
		fmt.Fprintf(&b, " AND a.published_at >= %s", ab.next(f.From.UTC()))
	}
	if !f.To.IsZero() {
		fmt.Fprintf(&b, " AND a.published_at < %s", ab.next(f.To.UTC()))
	}
	// Multi-value axes (admin only). Each ANDs with the scalar fields above and
	// ORs within itself via IN/EXISTS. Blank entries are dropped first; an
	// all-blank slice imposes no constraint. All values are bound via $n.
	if vals := nonBlank(f.Sections); len(vals) > 0 {
		fmt.Fprintf(&b, " AND a.section IN (%s)", inList(ab, vals))
	}
	if vals := nonBlank(f.Authors); len(vals) > 0 {
		fmt.Fprintf(&b, " AND a.author IN (%s)", inList(ab, vals))
	}
	if vals := nonBlank(f.Topics); len(vals) > 0 {
		// EXISTS (not a JOIN) so an article matching several of the values is
		// returned once; coexists with the scalar Topic JOIN above (both AND).
		fmt.Fprintf(&b, " AND EXISTS (SELECT 1 FROM article_topics att WHERE att.article_id = a.id AND att.topic IN (%s))", inList(ab, vals))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		// Full-text substring over title OR body. LIKE wildcards in the user value
		// are escaped in Go (likeEscape) and the backslash is declared as the
		// ESCAPE char, so '%'/'_' are matched literally, never as wildcards. We use
		// lower()+LIKE (NOT ILIKE) so the case-folding is symmetric with SQLite.
		// standard_conforming_strings is on by default (pg17), so the '\' literal
		// in ESCAPE '\' is a single backslash, matching the bound pattern.
		//
		// ACCEPTED BOUNDARY: this is ASCII-case-insensitive only. Postgres's lower()
		// is unicode-aware while SQLite's folds ASCII only, so a non-ASCII case fold
		// (e.g. 'É' vs 'é') would DIVERGE between the engines. The contract is
		// therefore: case-insensitive for ASCII; non-ASCII (accented) letters match
		// case-sensitively. An exact non-ASCII substring in the same case DOES match
		// and is safe to rely on across both engines.
		pat := "%" + likeEscape(q) + "%"
		fmt.Fprintf(&b, " AND (lower(a.title) LIKE lower(%s) ESCAPE '\\' OR lower(a.body) LIKE lower(%s) ESCAPE '\\')", ab.next(pat), ab.next(pat))
	}
	if count {
		return b.String(), ab.args
	}
	dir := "DESC"
	if f.Order == store.OldestFirst {
		dir = "ASC"
	}
	fmt.Fprintf(&b, " ORDER BY a.published_at %s, a.id %s", dir, dir)
	if f.Limit > 0 {
		fmt.Fprintf(&b, " LIMIT %s", ab.next(f.Limit))
	}
	if f.Offset > 0 {
		fmt.Fprintf(&b, " OFFSET %s", ab.next(f.Offset))
	}
	return b.String(), ab.args
}

// nonBlank returns the entries of in that are not empty or whitespace-only,
// preserving their original (untrimmed) value so membership matches the exact
// stored column value, consistent with the scalar equality predicates.
func nonBlank(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// inList binds each value through the argBuilder and returns the comma-joined
// "$n,$m,..." placeholder list for an IN clause.
func inList(ab *argBuilder, vals []string) string {
	ph := make([]string, len(vals))
	for i, v := range vals {
		ph[i] = ab.next(v)
	}
	return strings.Join(ph, ",")
}

// likeEscape escapes the LIKE wildcards in a user value so it is matched as a
// literal substring under "ESCAPE '\'". The escape char is escaped first, then
// the '%' and '_' metacharacters. Identical to the SQLite adapter's escaping so
// the two engines bind byte-identical patterns.
func likeEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

func getByID(ctx context.Context, q querier, id int64) (domain.Article, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id,slug,title,body,author,section,published_at,content_hash,metadata,created_at
		 FROM articles WHERE id = $1`, id)
	a, _, err := scanArticle(row)
	if err != nil {
		return domain.Article{}, err
	}
	topics, err := loadTopics(ctx, q, []int64{id})
	if err != nil {
		return domain.Article{}, err
	}
	if tp := topics[id]; tp != nil {
		a.Topics = tp
	}
	return a, nil
}

func scanArticle(sc scanner) (domain.Article, int64, error) {
	var (
		id                                       int64
		slug, title, body, author, section, hash string
		pub, created                             time.Time
		meta                                     []byte
	)
	if err := sc.Scan(&id, &slug, &title, &body, &author, &section, &pub, &hash, &meta, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Article{}, 0, store.ErrNotFound
		}
		return domain.Article{}, 0, err
	}
	m, err := unmarshalMeta(meta)
	if err != nil {
		return domain.Article{}, 0, err
	}
	return domain.Article{
		ID:          strconv.FormatInt(id, 10),
		Slug:        slug,
		Title:       title,
		Body:        body,
		Author:      author,
		Section:     section,
		PublishedAt: pub.UTC(),
		ContentHash: hash,
		Metadata:    m,
		CreatedAt:   created.UTC(),
		Topics:      []string{},
	}, id, nil
}

func loadTopics(ctx context.Context, q querier, ids []int64) (map[int64][]string, error) {
	out := make(map[int64][]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ab := &argBuilder{}
	ph := make([]string, len(ids))
	for i, id := range ids {
		ph[i] = ab.next(id)
	}
	rows, err := q.QueryContext(ctx,
		`SELECT article_id, topic FROM article_topics WHERE article_id IN (`+strings.Join(ph, ",")+`) ORDER BY article_id, topic`,
		ab.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var aid int64
		var topic string
		if err := rows.Scan(&aid, &topic); err != nil {
			return nil, err
		}
		out[aid] = append(out[aid], topic)
	}
	return out, rows.Err()
}

// FindSubmission returns a prior submission for the idempotency key, or found=false.
func (s *Store) FindSubmission(ctx context.Context, key string) (store.Submission, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT idempotency_key,content_hash,article_id,slug,author,scopes,created_at FROM submissions WHERE idempotency_key = $1`, key)
	var sub store.Submission
	var scopes string
	var created time.Time
	err := row.Scan(&sub.IdempotencyKey, &sub.ContentHash, &sub.ArticleID, &sub.Slug, &sub.Author, &scopes, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Submission{}, false, nil
	}
	if err != nil {
		return store.Submission{}, false, err
	}
	if scopes != "" {
		sub.Scopes = strings.Split(scopes, " ")
	}
	sub.CreatedAt = created.UTC()
	return sub, true, nil
}

// RecordSubmission appends a submission record (audit log + idempotency ledger).
func (s *Store) RecordSubmission(ctx context.Context, sub store.Submission) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO submissions (idempotency_key,content_hash,article_id,slug,author,scopes,created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		sub.IdempotencyKey, sub.ContentHash, sub.ArticleID, sub.Slug, sub.Author,
		// Truncate to whole seconds so both adapters return the identical instant
		// through the SubmissionLog interface. The caller writes time.Now().UTC()
		// (sub-second), and TIMESTAMPTZ would otherwise keep microseconds while
		// SQLite's RFC3339 drops them, diverging on the exact inputs production feeds.
		strings.Join(sub.Scopes, " "), sub.CreatedAt.UTC().Truncate(time.Second))
	if err != nil {
		return fmt.Errorf("record submission: %w", err)
	}
	return nil
}

func marshalMeta(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}
	return string(b), nil
}

func unmarshalMeta(b []byte) (map[string]any, error) {
	if len(b) == 0 || string(b) == "{}" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return m, nil
}
