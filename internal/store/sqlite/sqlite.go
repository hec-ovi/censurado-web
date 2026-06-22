// Package sqlite implements the store.Repository contract on SQLite using the
// pure-Go modernc.org/sqlite driver (no cgo). It is the default source-of-truth
// adapter: a single file in WAL mode, the stable hot axes indexed, and topics
// normalized into a join table.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/store"
)

//go:embed schema.sql
var schema string

const timeLayout = time.RFC3339

// Store is a SQLite-backed Repository.
type Store struct {
	db *sql.DB
}

// querier is satisfied by both *sql.DB and *sql.Tx so helpers work in or out of
// a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface {
	Scan(dest ...any) error
}

// Open opens (creating if needed) a SQLite database at path and applies the
// schema. WAL mode, foreign keys, and a busy timeout are set via the DSN.
func Open(path string) (*Store, error) {
	q := url.Values{"_pragma": {"journal_mode(WAL)", "foreign_keys(1)", "busy_timeout(5000)"}}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	// Single-writer model: one connection serializes writes and sidesteps lock
	// churn, which matches the batch publish workload.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite schema: %w", err)
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

	res, err := tx.ExecContext(ctx,
		`INSERT INTO articles (slug,title,body,author,section,published_at,content_hash,metadata,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(content_hash) DO NOTHING`,
		a.Slug, a.Title, a.Body, a.Author, a.Section,
		// Whole-second resolution so both adapters return the identical instant
		// through the Repository interface. RFC3339 already drops sub-second
		// precision, so the Truncate is documentary here, but it keeps the
		// whole-second contract explicit and symmetric with the Postgres adapter.
		a.PublishedAt.UTC().Truncate(time.Second).Format(timeLayout), a.ContentHash, meta, a.CreatedAt.UTC().Truncate(time.Second).Format(timeLayout),
	)
	if err != nil {
		return store.UpsertResult{}, fmt.Errorf("insert article: %w", err)
	}
	affected, _ := res.RowsAffected()
	created := affected == 1

	var id int64
	if created {
		if id, err = res.LastInsertId(); err != nil {
			return store.UpsertResult{}, err
		}
		for _, topic := range a.Topics {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO article_topics (article_id,topic) VALUES (?,?)`, id, topic); err != nil {
				return store.UpsertResult{}, fmt.Errorf("insert topic: %w", err)
			}
		}
	} else {
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM articles WHERE content_hash = ?`, a.ContentHash).Scan(&id); err != nil {
			return store.UpsertResult{}, fmt.Errorf("lookup existing: %w", err)
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
	err := s.db.QueryRowContext(ctx, `SELECT id FROM articles WHERE slug = ?`, slug).Scan(&id)
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
	var args []any
	if count {
		b.WriteString("SELECT COUNT(*) FROM articles a")
	} else {
		b.WriteString("SELECT a.id,a.slug,a.title,a.body,a.author,a.section,a.published_at,a.content_hash,a.metadata,a.created_at FROM articles a")
	}
	if f.Topic != "" {
		b.WriteString(" JOIN article_topics t ON t.article_id = a.id AND t.topic = ?")
		args = append(args, f.Topic)
	}
	b.WriteString(" WHERE 1=1")
	if f.Section != "" {
		b.WriteString(" AND a.section = ?")
		args = append(args, f.Section)
	}
	if f.Author != "" {
		b.WriteString(" AND a.author = ?")
		args = append(args, f.Author)
	}
	if !f.From.IsZero() {
		b.WriteString(" AND a.published_at >= ?")
		args = append(args, f.From.UTC().Format(timeLayout))
	}
	if !f.To.IsZero() {
		b.WriteString(" AND a.published_at < ?")
		args = append(args, f.To.UTC().Format(timeLayout))
	}
	if count {
		return b.String(), args
	}
	dir := "DESC"
	if f.Order == store.OldestFirst {
		dir = "ASC"
	}
	fmt.Fprintf(&b, " ORDER BY a.published_at %s, a.id %s", dir, dir)
	if f.Limit > 0 {
		b.WriteString(" LIMIT ?")
		args = append(args, f.Limit)
	}
	if f.Offset > 0 {
		if f.Limit <= 0 {
			b.WriteString(" LIMIT -1") // SQLite requires LIMIT before OFFSET
		}
		b.WriteString(" OFFSET ?")
		args = append(args, f.Offset)
	}
	return b.String(), args
}

func getByID(ctx context.Context, q querier, id int64) (domain.Article, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id,slug,title,body,author,section,published_at,content_hash,metadata,created_at
		 FROM articles WHERE id = ?`, id)
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
		id                                                            int64
		slug, title, body, author, section, pub, hash, meta, created string
	)
	if err := sc.Scan(&id, &slug, &title, &body, &author, &section, &pub, &hash, &meta, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Article{}, 0, store.ErrNotFound
		}
		return domain.Article{}, 0, err
	}
	pubT, err := time.Parse(timeLayout, pub)
	if err != nil {
		return domain.Article{}, 0, fmt.Errorf("parse published_at: %w", err)
	}
	createdT, err := time.Parse(timeLayout, created)
	if err != nil {
		return domain.Article{}, 0, fmt.Errorf("parse created_at: %w", err)
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
		PublishedAt: pubT,
		ContentHash: hash,
		Metadata:    m,
		CreatedAt:   createdT,
		Topics:      []string{},
	}, id, nil
}

func loadTopics(ctx context.Context, q querier, ids []int64) (map[int64][]string, error) {
	out := make(map[int64][]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := q.QueryContext(ctx,
		`SELECT article_id, topic FROM article_topics WHERE article_id IN (`+strings.Join(ph, ",")+`) ORDER BY article_id, topic`,
		args...)
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
		`SELECT idempotency_key,content_hash,article_id,slug,author,scopes,created_at FROM submissions WHERE idempotency_key = ?`, key)
	var sub store.Submission
	var scopes, created string
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
	if sub.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return store.Submission{}, false, fmt.Errorf("parse submission created_at: %w", err)
	}
	return sub, true, nil
}

// RecordSubmission appends a submission record (audit log + idempotency ledger).
func (s *Store) RecordSubmission(ctx context.Context, sub store.Submission) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO submissions (idempotency_key,content_hash,article_id,slug,author,scopes,created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		sub.IdempotencyKey, sub.ContentHash, sub.ArticleID, sub.Slug, sub.Author,
		// Truncate to whole seconds, the shared second-granularity contract with
		// the Postgres adapter. RFC3339 already drops sub-second precision, so
		// this is explicit rather than load-bearing here, but it keeps both
		// adapters visibly identical and guards against a future layout change.
		strings.Join(sub.Scopes, " "), sub.CreatedAt.UTC().Truncate(time.Second).Format(timeLayout))
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

func unmarshalMeta(s string) (map[string]any, error) {
	if s == "" || s == "{}" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return m, nil
}
