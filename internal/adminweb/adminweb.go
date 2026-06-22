// Package adminweb is the private, server-rendered operator console for the
// censurado archive. It is an observability tool, not a CMS: it is read-only over
// article content (browse with combinable filters, inspect one article) behind a
// single-operator signed-cookie session, and is meant to run bound to localhost
// off the public internet. Every page is plain Go html/template with a small
// vendored htmx for the filter/results swap; the only trusted HTML is the
// markdown body, rendered through internal/content's sanitizer.
package adminweb

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hec-ovi/censurado-web/internal/content"
	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/store"
)

// Config wires a Handler. Repo and the auth secrets are required; the rest take
// documented defaults.
type Config struct {
	Repo          store.Repository
	Now           func() time.Time // injectable clock; default time.Now
	TokenHash     string           // hex sha256 of the operator login token
	SessionKey    []byte           // HMAC signing key for session+csrf (>=32 bytes)
	SessionTTL    time.Duration    // default 12h if zero
	SecureCookies bool             // set Secure on cookies (true in prod)
	PageSize      int              // default 50

	// Log backs the operator audit view (/admin/audit). When nil, that route
	// returns a friendly "not configured" page instead of an error.
	Log store.SubmissionLog
	// Regenerate triggers a static-site rebuild from /admin/regenerate. It is an
	// injected closure so adminweb stays decoupled from internal/generate: the
	// binary supplies it. When nil, the route shows a disabled state and the POST
	// returns a friendly "not configured" response (never a 500/panic).
	Regenerate func(ctx context.Context) (RegenResult, error)
}

// RegenResult is the outcome of one Regenerate call, surfaced on the regenerate
// page. It mirrors the generator's Result counts and purge list without importing
// internal/generate, so the two packages stay decoupled.
type RegenResult struct {
	Written    int
	Unchanged  int
	Deleted    int
	ScopeCount int
	Purge      []string // every root-relative URL to invalidate; never truncated
}

// Handler serves the whole /admin surface. It is safe for concurrent use.
type Handler struct {
	repo          store.Repository
	now           func() time.Time
	tokenHash     string
	sessionKey    []byte
	sessionTTL    time.Duration
	secureCookies bool
	pageSize      int
	log           store.SubmissionLog
	regenerate    func(ctx context.Context) (RegenResult, error)
	mux           *http.ServeMux
}

const (
	defaultSessionTTL = 12 * time.Hour
	defaultPageSize   = 50
	minSessionKeyLen  = 32
)

// New validates the config, fills defaults, and builds the route mux.
func New(cfg Config) (*Handler, error) {
	if cfg.Repo == nil {
		return nil, errors.New("adminweb: Repo is required")
	}
	if cfg.TokenHash == "" {
		return nil, errors.New("adminweb: TokenHash is required")
	}
	if len(cfg.SessionKey) < minSessionKeyLen {
		return nil, fmt.Errorf("adminweb: SessionKey must be at least %d bytes, got %d", minSessionKeyLen, len(cfg.SessionKey))
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	ttl := cfg.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	pageSize := cfg.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	h := &Handler{
		repo:          cfg.Repo,
		now:           now,
		tokenHash:     cfg.TokenHash,
		sessionKey:    append([]byte(nil), cfg.SessionKey...),
		sessionTTL:    ttl,
		secureCookies: cfg.SecureCookies,
		pageSize:      pageSize,
		log:           cfg.Log,        // optional; nil disables the audit view
		regenerate:    cfg.Regenerate, // optional; nil disables the regenerate action
	}
	h.routes()
	return h, nil
}

// ServeHTTP dispatches through the route mux.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// routes registers the Go 1.22 method+pattern routes. Registering POST-only
// patterns (logout) makes a GET to the same path return 405 automatically.
func (h *Handler) routes() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/login", h.handleLoginForm)
	mux.HandleFunc("POST /admin/login", h.handleLoginSubmit)
	mux.HandleFunc("POST /admin/logout", h.requireSession(h.requireCSRF(h.handleLogout)))
	mux.HandleFunc("GET /admin/{$}", h.handleRoot)
	mux.HandleFunc("GET /admin/articles", h.requireSession(h.handleArticles))
	mux.HandleFunc("GET /admin/articles/{slug}", h.requireSession(h.handleDetail))
	mux.HandleFunc("GET /admin/audit", h.requireSession(h.handleAudit))
	mux.HandleFunc("GET /admin/regenerate", h.requireSession(h.handleRegenerateForm))
	mux.HandleFunc("POST /admin/regenerate", h.requireSession(h.requireCSRF(h.handleRegenerateSubmit)))
	mux.HandleFunc("GET /admin/assets/{file}", h.handleAsset)
	mux.HandleFunc("GET /admin/healthz", h.handleHealthz)
	h.mux = mux
}

// requireSession guards a handler with the signed-cookie session. On failure it
// answers HX-Request callers with an HX-Redirect header (htmx swaps would
// otherwise inject the login page into a fragment) and everyone else with a 303
// to the login page. On success it stashes the session expiry in the request
// context so downstream handlers can derive the CSRF token without re-parsing.
func (h *Handler) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := h.now()
		exp, ok := h.verifySession(r, now)
		if !ok {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/admin/login")
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		// Sliding renewal: once more than half the TTL has elapsed, mint a fresh
		// cookie so an active operator is not logged out mid-session. The renewed
		// cookie carries a NEW expiry, so adopt it as the context expiry too:
		// layoutFor derives the CSRF token from the context expiry, and that token
		// must match the cookie the browser now holds, or the next POST 403s.
		if time.Unix(exp, 0).Sub(now) < h.sessionTTL/2 {
			exp = h.issueSession(w, now)
		}
		ctx := context.WithValue(r.Context(), sessionExpKey, exp)
		next(w, r.WithContext(ctx))
	}
}

// requireCSRF guards a mutating POST. It must run inside requireSession so the
// session expiry (and thus the expected token) is in context. A missing or
// mismatched csrf_token is a 403.
func (h *Handler) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		exp, ok := sessionExpFromContext(r)
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !h.validCSRF(exp, r.PostFormValue("csrf_token")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/articles", http.StatusSeeOther)
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.verifySession(r, h.now()); ok {
		http.Redirect(w, r, "/admin/articles", http.StatusSeeOther)
		return
	}
	h.renderTemplate(w, http.StatusOK, loginTmpl, "login", loginView{Title: "Sign in"})
}

func (h *Handler) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	token := r.PostFormValue("token")
	// Both sides are hex sha256 (fixed 64 bytes), so the constant-time compare
	// neither leaks length nor short-circuits on the first differing byte.
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(h.tokenHash)) == 1 {
		h.issueSession(w, h.now())
		http.Redirect(w, r, "/admin/articles", http.StatusSeeOther)
		return
	}
	// Generic error; never reflect the submitted token. No session cookie is set.
	h.renderTemplate(w, http.StatusOK, loginTmpl, "login", loginView{
		Title: "Sign in",
		Error: "Invalid token.",
	})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	h.clearSession(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (h *Handler) handleArticles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sel := h.parseSelection(r)

	total, err := h.repo.Count(ctx, h.buildFilter(sel, true))
	if err != nil {
		h.serverError(w, "count", err)
		return
	}
	arts, err := h.repo.Find(ctx, h.buildFilter(sel, false))
	if err != nil {
		h.serverError(w, "find", err)
		return
	}
	results := h.buildResults(sel, arts, total)

	// HX-Request -> swap only the results region, so hx-push-url'd filtered URLs
	// stay shareable: a full reload of the same URL hits the else branch below and
	// reproduces the identical state.
	if r.Header.Get("HX-Request") == "true" {
		h.renderPartial(w, "results", results)
		return
	}

	facets, err := h.repo.Facets(ctx)
	if err != nil {
		h.serverError(w, "facets", err)
		return
	}
	view := browseView{
		layoutData: h.layoutFor(r, "Browse articles"),
		Sections:   facetOptionsFor(facets.Sections, sel.Sections),
		Authors:    facetOptionsFor(facets.Authors, sel.Authors),
		Topics:     facetOptionsFor(facets.Topics, sel.Topics),
		Query:      sel.Query,
		From:       sel.From,
		To:         sel.To,
		Order:      sel.Order,
		Chips:      h.chipsFor(sel),
		Results:    results,
	}
	h.renderPage(w, browseTmpl, view)
}

func (h *Handler) handleDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := r.PathValue("slug")
	a, err := h.repo.BySlug(ctx, slug)
	if errors.Is(err, store.ErrNotFound) {
		h.notFound(w, r)
		return
	}
	if err != nil {
		h.serverError(w, "byslug", err)
		return
	}
	bodyHTML, err := content.RenderMarkdown(a.Body)
	if err != nil {
		h.serverError(w, "render markdown", err)
		return
	}
	view := detailView{
		layoutData:   h.layoutFor(r, a.Title),
		ArticleTitle: a.Title,
		Author:       a.Author,
		Section:      a.Section,
		Published:    a.PublishedAt.UTC().Format("2006-01-02"),
		Topics:       a.Topics,
		BodyHTML:     template.HTML(bodyHTML),
		Meta:         metaRows(a),
		ContentHash:  a.ContentHash,
		CreatedAt:    a.CreatedAt.UTC().Format(time.RFC3339),
	}
	h.renderPage(w, detailTmpl, view)
}

// handleAudit renders the operator audit log: recorded submissions newest first,
// with optional author and date-range filters and pagination. When no Log is
// configured it returns a friendly 503 page rather than an error. Mirrors the
// browse dual-render: an HX-Request gets only the results fragment.
func (h *Handler) handleAudit(w http.ResponseWriter, r *http.Request) {
	if h.log == nil {
		view := auditView{
			layoutData:    h.layoutFor(r, "Audit log"),
			NotConfigured: true,
		}
		h.renderTemplate(w, http.StatusServiceUnavailable, auditTmpl, "layout", view)
		return
	}
	ctx := r.Context()
	sel := h.parseAuditSelection(r)
	f := store.ListSubmissionsFilter{
		Author: sel.Author,
		From:   sel.FromTime,
		To:     sel.ToTime,
		// Over-fetch one row past the page so we can report a next page without a
		// separate count query (SubmissionLog has no Count). buildAuditResults trims.
		Limit:  h.pageSize + 1,
		Offset: (sel.Page - 1) * h.pageSize,
	}
	subs, err := h.log.ListSubmissions(ctx, f)
	if err != nil {
		h.serverError(w, "list submissions", err)
		return
	}
	results := h.buildAuditResults(sel, subs)

	if r.Header.Get("HX-Request") == "true" {
		h.renderTemplate(w, http.StatusOK, auditResultsTmpl, "audit_results", results)
		return
	}
	view := auditView{
		layoutData: h.layoutFor(r, "Audit log"),
		Author:     sel.Author,
		From:       sel.From,
		To:         sel.To,
		Results:    results,
	}
	h.renderPage(w, auditTmpl, view)
}

// handleRegenerateForm renders the regenerate page: a POST form with the CSRF
// token and a submit button when Regenerate is configured, or a disabled note
// when it is nil. The action runs on POST only.
func (h *Handler) handleRegenerateForm(w http.ResponseWriter, r *http.Request) {
	view := regenerateView{
		layoutData: h.layoutFor(r, "Regenerate"),
		Configured: h.regenerate != nil,
	}
	h.renderPage(w, regenerateTmpl, view)
}

// handleRegenerateSubmit runs the regenerate closure (under requireSession +
// requireCSRF) and renders the summary. A nil closure yields a friendly
// "not configured" page rather than a 500/panic. On error it shows the error; on
// success it shows the counts and the FULL purge URL list (never truncated).
func (h *Handler) handleRegenerateSubmit(w http.ResponseWriter, r *http.Request) {
	view := regenerateView{
		layoutData: h.layoutFor(r, "Regenerate"),
		Configured: h.regenerate != nil,
	}
	if h.regenerate == nil {
		// Feature disabled: render the full page (with its disabled note) so the
		// operator sees a clear, friendly state. Not an error.
		h.renderPage(w, regenerateTmpl, view)
		return
	}
	view.Ran = true
	res, err := h.regenerate(r.Context())
	if err != nil {
		view.Error = err.Error()
	} else {
		view.Result = &regenResultView{
			Written:    res.Written,
			Unchanged:  res.Unchanged,
			Deleted:    res.Deleted,
			ScopeCount: res.ScopeCount,
			Purge:      res.Purge,
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		h.renderTemplate(w, http.StatusOK, regenResultTmpl, "regen_result", view)
		return
	}
	h.renderPage(w, regenerateTmpl, view)
}

// assetTypes is the allowlist of servable embedded assets and their MIME types.
// Only these exact names are served; the {file} route segment cannot contain a
// slash, so path traversal is structurally impossible, and the allowlist is a
// second gate.
var assetTypes = map[string]string{
	"htmx.min.js": "application/javascript; charset=utf-8",
	"admin.css":   "text/css; charset=utf-8",
}

func (h *Handler) handleAsset(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	ctype, ok := assetTypes[file]
	if !ok {
		h.notFound(w, r)
		return
	}
	body, err := assetFS.ReadFile("assets/" + file)
	if err != nil {
		h.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// layoutFor builds the shared layout data, deriving the CSRF token from the
// session expiry stashed by requireSession.
func (h *Handler) layoutFor(r *http.Request, title string) layoutData {
	ld := layoutData{Title: title}
	if exp, ok := sessionExpFromContext(r); ok {
		ld.CSRFToken = h.csrfToken(exp)
	}
	return ld
}

func (h *Handler) serverError(w http.ResponseWriter, stage string, err error) {
	// Detail goes to stderr only; the client gets a generic body.
	fmt.Println("adminweb:", stage, "error:", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<title>Not found</title><link rel="stylesheet" href="/admin/assets/admin.css"></head>` +
		`<body class="admin-body"><main class="admin-main"><h1 class="admin-h1">404</h1>` +
		`<p>No such article. <a href="/admin/articles">Back to browse</a>.</p></main></body></html>`))
}

// selection is the parsed, validated filter request. Raw date strings are kept
// for re-populating the form and chips; the parsed times drive the store filter.
type selection struct {
	Sections []string
	Authors  []string
	Topics   []string
	Query    string
	From     string // raw YYYY-MM-DD echoed into the input (only set when valid)
	To       string
	FromTime time.Time
	ToTime   time.Time
	Order    string // "newest" (default) or "oldest"
	Page     int    // 1-based
}

const dateLayout = "2006-01-02"

// parseSelection reads the filter query params. Invalid dates and pages are
// ignored gracefully (no error page; just no constraint / page 1).
func (h *Handler) parseSelection(r *http.Request) selection {
	q := r.URL.Query()
	sel := selection{
		Sections: nonBlankStrings(q["section"]),
		Authors:  nonBlankStrings(q["author"]),
		Topics:   nonBlankStrings(q["topic"]),
		Query:    strings.TrimSpace(q.Get("q")),
		Order:    "newest",
		Page:     1,
	}
	if q.Get("order") == "oldest" {
		sel.Order = "oldest"
	}
	if t, err := time.Parse(dateLayout, strings.TrimSpace(q.Get("from"))); err == nil {
		sel.FromTime = t.UTC()
		sel.From = t.UTC().Format(dateLayout)
	}
	if t, err := time.Parse(dateLayout, strings.TrimSpace(q.Get("to"))); err == nil {
		// 'to' is an INCLUSIVE day: the store's To is an exclusive upper bound, so
		// the whole of the named day is included by adding 24h.
		sel.ToTime = t.UTC().Add(24 * time.Hour)
		sel.To = t.UTC().Format(dateLayout)
	}
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 1 {
		sel.Page = n
	}
	return sel
}

// buildFilter maps a selection onto a store.Filter. forCount omits paging so the
// total used for pagination ignores Limit/Offset.
func (h *Handler) buildFilter(sel selection, forCount bool) store.Filter {
	f := store.Filter{
		Sections: sel.Sections,
		Authors:  sel.Authors,
		Topics:   sel.Topics,
		Query:    sel.Query,
		From:     sel.FromTime,
		To:       sel.ToTime,
		Order:    store.NewestFirst,
	}
	if sel.Order == "oldest" {
		f.Order = store.OldestFirst
	}
	if !forCount {
		f.Limit = h.pageSize
		f.Offset = (sel.Page - 1) * h.pageSize
	}
	return f
}

// values renders the selection back into url.Values (without page) for building
// shareable links.
func (sel selection) values() url.Values {
	v := url.Values{}
	for _, s := range sel.Sections {
		v.Add("section", s)
	}
	for _, a := range sel.Authors {
		v.Add("author", a)
	}
	for _, t := range sel.Topics {
		v.Add("topic", t)
	}
	if sel.Query != "" {
		v.Set("q", sel.Query)
	}
	if sel.From != "" {
		v.Set("from", sel.From)
	}
	if sel.To != "" {
		v.Set("to", sel.To)
	}
	if sel.Order == "oldest" {
		v.Set("order", "oldest")
	}
	return v
}

func articlesURL(v url.Values) string {
	if len(v) == 0 {
		return "/admin/articles"
	}
	return "/admin/articles?" + v.Encode()
}

func (h *Handler) buildResults(sel selection, arts []domain.Article, total int) resultsView {
	rows := make([]articleRow, 0, len(arts))
	for _, a := range arts {
		rows = append(rows, articleRow{
			Title:     a.Title,
			Slug:      a.Slug,
			DetailURL: "/admin/articles/" + a.Slug,
			Author:    a.Author,
			Section:   a.Section,
			Topics:    a.Topics,
			Published: a.PublishedAt.UTC().Format(dateLayout),
		})
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + h.pageSize - 1) / h.pageSize
	}
	rv := resultsView{
		Rows:       rows,
		Total:      total,
		Shown:      len(rows),
		Page:       sel.Page,
		PageSize:   h.pageSize,
		TotalPages: totalPages,
		HasPrev:    sel.Page > 1,
		HasNext:    sel.Page < totalPages,
	}
	if len(rows) > 0 {
		rv.FirstIndex = (sel.Page-1)*h.pageSize + 1
		rv.LastIndex = rv.FirstIndex + len(rows) - 1
	}
	if rv.HasPrev {
		v := sel.values()
		v.Set("page", strconv.Itoa(sel.Page-1))
		rv.PrevURL = articlesURL(v)
	}
	if rv.HasNext {
		v := sel.values()
		v.Set("page", strconv.Itoa(sel.Page+1))
		rv.NextURL = articlesURL(v)
	}
	return rv
}

// chipsFor renders one removable chip per active constraint. Each RemoveURL is
// the current selection minus that single value, with paging reset.
func (h *Handler) chipsFor(sel selection) []chip {
	var chips []chip
	add := func(axis, value string, without url.Values) {
		chips = append(chips, chip{Axis: axis, Value: value, RemoveURL: articlesURL(without)})
	}
	for _, s := range sel.Sections {
		add("section", s, removeValue(sel.values(), "section", s))
	}
	for _, a := range sel.Authors {
		add("author", a, removeValue(sel.values(), "author", a))
	}
	for _, t := range sel.Topics {
		add("topic", t, removeValue(sel.values(), "topic", t))
	}
	if sel.Query != "" {
		v := sel.values()
		v.Del("q")
		add("search", sel.Query, v)
	}
	if !sel.FromTime.IsZero() {
		v := sel.values()
		v.Del("from")
		add("from", sel.From, v)
	}
	if !sel.ToTime.IsZero() {
		v := sel.values()
		v.Del("to")
		add("to", sel.To, v)
	}
	return chips
}

// removeValue returns a copy of v with one occurrence of val removed from key.
func removeValue(v url.Values, key, val string) url.Values {
	kept := v[key][:0:0]
	removed := false
	for _, x := range v[key] {
		if !removed && x == val {
			removed = true
			continue
		}
		kept = append(kept, x)
	}
	if len(kept) == 0 {
		v.Del(key)
	} else {
		v[key] = kept
	}
	return v
}

func facetOptionsFor(facets []store.Facet, selected []string) []facetOption {
	out := make([]facetOption, 0, len(facets))
	for _, f := range facets {
		out = append(out, facetOption{
			Value:    f.Value,
			Count:    f.Count,
			Selected: containsString(selected, f.Value),
		})
	}
	return out
}

// auditSelection is the parsed, validated audit-log filter request. Raw date
// strings are kept to re-populate the form; the parsed times drive the store
// filter. The date semantics match browse: 'to' is an inclusive day (the store's
// To is the exclusive upper bound, so the whole named day is included by +24h).
type auditSelection struct {
	Author   string
	From     string // raw YYYY-MM-DD echoed into the input (only set when valid)
	To       string
	FromTime time.Time
	ToTime   time.Time
	Page     int // 1-based
}

// parseAuditSelection reads the audit filter query params. Invalid dates and
// pages are ignored gracefully (no error page; just no constraint / page 1).
func (h *Handler) parseAuditSelection(r *http.Request) auditSelection {
	q := r.URL.Query()
	sel := auditSelection{
		Author: strings.TrimSpace(q.Get("author")),
		Page:   1,
	}
	if t, err := time.Parse(dateLayout, strings.TrimSpace(q.Get("from"))); err == nil {
		sel.FromTime = t.UTC()
		sel.From = t.UTC().Format(dateLayout)
	}
	if t, err := time.Parse(dateLayout, strings.TrimSpace(q.Get("to"))); err == nil {
		sel.ToTime = t.UTC().Add(24 * time.Hour)
		sel.To = t.UTC().Format(dateLayout)
	}
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 1 {
		sel.Page = n
	}
	return sel
}

// values renders the audit selection back into url.Values (without page) for
// building shareable, filter-preserving pagination links.
func (sel auditSelection) values() url.Values {
	v := url.Values{}
	if sel.Author != "" {
		v.Set("author", sel.Author)
	}
	if sel.From != "" {
		v.Set("from", sel.From)
	}
	if sel.To != "" {
		v.Set("to", sel.To)
	}
	return v
}

func auditURL(v url.Values) string {
	if len(v) == 0 {
		return "/admin/audit"
	}
	return "/admin/audit?" + v.Encode()
}

// buildAuditResults projects the over-fetched submissions into the results view.
// The extra row past the page (if present) only signals a next page; it is
// trimmed off before rendering.
func (h *Handler) buildAuditResults(sel auditSelection, subs []store.Submission) auditResultsView {
	hasNext := len(subs) > h.pageSize
	if hasNext {
		subs = subs[:h.pageSize]
	}
	rows := make([]auditRow, 0, len(subs))
	for _, s := range subs {
		rows = append(rows, auditRow{
			CreatedAt:      s.CreatedAt.UTC().Format(time.RFC3339),
			Author:         s.Author,
			Slug:           s.Slug,
			DetailURL:      "/admin/articles/" + s.Slug,
			ContentHash:    s.ContentHash,
			Scopes:         s.Scopes,
			IdempotencyKey: s.IdempotencyKey,
		})
	}
	rv := auditResultsView{
		Rows:     rows,
		Shown:    len(rows),
		Page:     sel.Page,
		PageSize: h.pageSize,
		HasPrev:  sel.Page > 1,
		HasNext:  hasNext,
	}
	if rv.HasPrev {
		v := sel.values()
		v.Set("page", strconv.Itoa(sel.Page-1))
		rv.PrevURL = auditURL(v)
	}
	if rv.HasNext {
		v := sel.values()
		v.Set("page", strconv.Itoa(sel.Page+1))
		rv.NextURL = auditURL(v)
	}
	return rv
}

// metaRows projects an article's open-ended metadata into stable key/value rows,
// sorted by key for a deterministic table.
func metaRows(a domain.Article) []kv {
	if len(a.Metadata) == 0 {
		return nil
	}
	keys := make([]string, 0, len(a.Metadata))
	for k := range a.Metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]kv, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, kv{Key: k, Value: fmt.Sprintf("%v", a.Metadata[k])})
	}
	return rows
}

func nonBlankStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
