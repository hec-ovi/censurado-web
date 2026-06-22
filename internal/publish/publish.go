// Package publish implements the authenticated write path: the only way articles
// enter the system. It authenticates the caller, binds the article to that
// author, validates and safety-checks untrusted input, stores it (deduplicated
// and idempotent), and appends an audit/idempotency record per submission.
package publish

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hec-ovi/censurado-web/internal/content"
	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/store"
)

// ScopeWrite is the scope a key must hold to publish.
const ScopeWrite = "articles:write"

// maxBody caps the request entity as a transport safeguard against abuse. It is
// generous and is not a content limit on the article body.
const maxBody = 8 << 20 // 8 MiB

// Handler serves POST /articles.
type Handler struct {
	repo store.Repository
	log  store.SubmissionLog
	auth Authenticator
	now  func() time.Time
}

// NewHandler wires the publish handler. now may be nil (defaults to time.Now).
func NewHandler(repo store.Repository, log store.SubmissionLog, auth Authenticator, now func() time.Time) *Handler {
	if now == nil {
		now = time.Now
	}
	return &Handler{repo: repo, log: log, auth: auth, now: now}
}

type problem struct {
	Status int               `json:"status"`
	Code   string            `json:"code"`
	Detail string            `json:"detail,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

type createResponse struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeProblem(w, problem{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed"})
		return
	}

	id, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeProblem(w, problem{Status: http.StatusBadRequest, Code: "missing_idempotency_key", Detail: "Idempotency-Key header is required"})
		return
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields() // additionalProperties:false from the contract
	var in domain.PublishInput
	if err := dec.Decode(&in); err != nil {
		writeProblem(w, problem{Status: http.StatusBadRequest, Code: "invalid_json", Detail: err.Error()})
		return
	}

	// The key determines the author: an article may only be published as the
	// authenticated persona.
	if strings.TrimSpace(in.Author) != id.Author {
		writeProblem(w, problem{Status: http.StatusForbidden, Code: "author_mismatch", Detail: "author must match the authenticated key"})
		return
	}

	article, err := domain.NewArticle(in, h.now())
	if err != nil {
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			writeProblem(w, problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Fields: ve.Fields})
			return
		}
		writeProblem(w, problem{Status: http.StatusUnprocessableEntity, Code: "invalid_article", Detail: err.Error()})
		return
	}

	// Safety gate: the body must render to sanitized HTML without error.
	if _, err := content.RenderMarkdown(article.Body); err != nil {
		writeProblem(w, problem{Status: http.StatusUnprocessableEntity, Code: "unrenderable_body", Detail: err.Error()})
		return
	}

	ctx := r.Context()
	if prev, found, err := h.log.FindSubmission(ctx, key); err != nil {
		writeProblem(w, problem{Status: http.StatusInternalServerError, Code: "internal"})
		return
	} else if found {
		if prev.ContentHash != article.ContentHash {
			writeProblem(w, problem{Status: http.StatusUnprocessableEntity, Code: "idempotency_key_reused", Detail: "this key was used for a different article"})
			return
		}
		writeJSON(w, http.StatusOK, createResponse{ID: prev.ArticleID, Slug: prev.Slug})
		return
	}

	res, err := h.repo.Upsert(ctx, article)
	if err != nil {
		writeProblem(w, problem{Status: http.StatusInternalServerError, Code: "store_error"})
		return
	}

	if err := h.log.RecordSubmission(ctx, store.Submission{
		IdempotencyKey: key,
		ContentHash:    res.Article.ContentHash,
		ArticleID:      res.Article.ID,
		Slug:           res.Article.Slug,
		Author:         id.Author,
		Scopes:         id.Scopes,
		CreatedAt:      h.now().UTC(),
	}); err != nil {
		writeProblem(w, problem{Status: http.StatusInternalServerError, Code: "audit_error"})
		return
	}

	status := http.StatusOK // article already existed (deduplicated)
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, createResponse{ID: res.Article.ID, Slug: res.Article.Slug})
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		writeProblem(w, problem{Status: http.StatusUnauthorized, Code: "missing_token"})
		return Identity{}, false
	}
	id, err := h.auth.Authenticate(token)
	if err != nil {
		writeProblem(w, problem{Status: http.StatusUnauthorized, Code: "invalid_token"})
		return Identity{}, false
	}
	if !id.HasScope(ScopeWrite) {
		writeProblem(w, problem{Status: http.StatusForbidden, Code: "insufficient_scope", Detail: "requires " + ScopeWrite})
		return Identity{}, false
	}
	return id, true
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, p problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
