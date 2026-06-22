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
		a.PublishedAt.UTC(), a.ContentHash, meta, a.CreatedAt.UTC(),
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
		id                                            int64
		slug, title, body, author, section, hash      string
		pub, created                                  time.Time
		meta                                          []byte
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
