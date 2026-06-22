package adminweb

import (
	"bytes"
	"embed"
	"html/template"
	"log"
	"net/http"
)

//go:embed templates/*.tmpl
var tmplFS embed.FS

//go:embed assets/*
var assetFS embed.FS

// Template sets are parsed once at init. Each page kind that defines "content"
// gets its own set paired with the shared layout, because two templates cannot
// both define "content" in one set (same reason the generator splits its sets).
// login renders its own standalone shell (no nav/logout), so it is parsed alone.
// results is parsed both inside the browse set (the full page embeds it) and on
// its own, so the HX-Request partial can render the fragment in isolation.
var (
	loginTmpl   = template.Must(template.New("login").ParseFS(tmplFS, "templates/login.tmpl"))
	browseTmpl  = template.Must(template.New("browse").ParseFS(tmplFS, "templates/layout.tmpl", "templates/browse.tmpl", "templates/results.tmpl"))
	detailTmpl  = template.Must(template.New("detail").ParseFS(tmplFS, "templates/layout.tmpl", "templates/detail.tmpl"))
	resultsTmpl = template.Must(template.New("results").ParseFS(tmplFS, "templates/results.tmpl"))
)

// layoutData is embedded by every full-page view so the shared layout can read
// the page title and the per-session CSRF token off the same dot.
type layoutData struct {
	Title     string
	CSRFToken string
}

// loginView models the standalone login page.
type loginView struct {
	Title string
	Error string // generic message; never echoes the submitted token
}

// facetOption is one selectable filter value with its article count and whether
// the current request has it selected.
type facetOption struct {
	Value    string
	Count    int
	Selected bool
}

// chip is an active filter shown as a removable tag. RemoveURL points back to
// /admin/articles with this one value dropped (and paging reset).
type chip struct {
	Axis      string // human label: "section" / "author" / "topic" / "search" / "from" / "to"
	Value     string
	RemoveURL string
}

// browseView is the full browse page: the filter controls (pre-filled from the
// request), the active-filter chips, and the embedded results region.
type browseView struct {
	layoutData
	Sections []facetOption
	Authors  []facetOption
	Topics   []facetOption
	Query    string
	From     string
	To       string
	Order    string // "newest" or "oldest"
	Chips    []chip
	Results  resultsView
}

// articleRow is one row in the results table.
type articleRow struct {
	Title     string
	Slug      string
	DetailURL string
	Author    string
	Section   string
	Topics    []string
	Published string // YYYY-MM-DD
}

// resultsView is the results table plus its count and pagination. It is rendered
// both inside the browse page and as the standalone HX fragment.
type resultsView struct {
	Rows       []articleRow
	Total      int // total matching articles (ignores paging)
	Shown      int // rows on this page
	Page       int
	PageSize   int
	TotalPages int
	FirstIndex int // 1-based index of the first row in the full result set
	LastIndex  int
	HasPrev    bool
	HasNext    bool
	PrevURL    string
	NextURL    string
}

// kv is one metadata key/value row on the detail page.
type kv struct {
	Key   string
	Value string
}

// detailView is a single article's full page.
type detailView struct {
	layoutData
	ArticleTitle string
	Author       string
	Section      string
	Published    string
	Topics       []string
	BodyHTML     template.HTML // sanitized markdown render; the ONLY trusted HTML
	Meta         []kv
	ContentHash  string
	CreatedAt    string
}

// renderTemplate executes name from t into a buffer first, so a template error
// becomes a clean 500 with a generic body rather than a half-written page. The
// detailed error is logged to stderr only.
func (h *Handler) renderTemplate(w http.ResponseWriter, status int, t *template.Template, name string, data any) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("adminweb: render %s: %v", name, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// renderPage writes a full layout-wrapped page.
func (h *Handler) renderPage(w http.ResponseWriter, t *template.Template, data any) {
	h.renderTemplate(w, http.StatusOK, t, "layout", data)
}

// renderPartial writes only the named fragment (the HX-Request results region).
func (h *Handler) renderPartial(w http.ResponseWriter, name string, data any) {
	h.renderTemplate(w, http.StatusOK, resultsTmpl, name, data)
}
