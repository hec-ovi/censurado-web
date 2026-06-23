package adminweb_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web/internal/adminweb"
	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/store/sqlite"
)

// publishStub is a recording fake for the injected Publish closure: it captures
// the input the handler built and returns a programmed result/error, so a test
// can assert both the parsing (what reached the write boundary) and the rendered
// outcome without standing up a real publish service.
type publishStub struct {
	calls  int
	lastIn adminweb.CreateArticleInput
	result adminweb.CreateArticleResult
	err    error
}

func (p *publishStub) fn(_ context.Context, in adminweb.CreateArticleInput) (adminweb.CreateArticleResult, error) {
	p.calls++
	p.lastIn = in
	return p.result, p.err
}

// newCreateHandler builds an admin handler with a configurable Publish closure
// (nil disables the action) over a small seed corpus, so the datalist suggestions
// and the create flow can be exercised end to end. Passing nil for pub leaves the
// route in its disabled state.
func newCreateHandler(t *testing.T, pub func(context.Context, adminweb.CreateArticleInput) (adminweb.CreateArticleResult, error)) *adminweb.Handler {
	t.Helper()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "create.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	ctx := context.Background()
	for _, s := range []struct {
		title, author, section string
		topics                 []string
	}{
		{"Seed One", "ada", "tech", []string{"go"}},
		{"Seed Two", "bo", "economics", []string{"markets"}},
	} {
		art, err := domain.NewArticle(domain.PublishInput{
			Title: s.title, Body: "body text", Author: s.author, Section: s.section, Topics: s.topics,
		}, fixedNow())
		if err != nil {
			t.Fatalf("NewArticle %q: %v", s.title, err)
		}
		if _, err := repo.Upsert(ctx, art); err != nil {
			t.Fatalf("Upsert %q: %v", s.title, err)
		}
	}

	h, err := adminweb.New(adminweb.Config{
		Repo:          repo,
		Now:           fixedNow,
		TokenHash:     hashTok(fixtureToken),
		SessionKey:    testSessionKey,
		SecureCookies: false,
		PageSize:      pageSize,
		Publish:       pub,
	})
	if err != nil {
		t.Fatalf("adminweb.New: %v", err)
	}
	return h
}

// getCreate fetches the create page and returns the body plus its CSRF token.
func getCreate(t *testing.T, h http.Handler, c *http.Cookie) (body, csrf string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/create", nil)
	req.AddCookie(c)
	rec := do(h, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/create status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	return rec.Body.String(), metaCSRF(t, rec.Body.String())
}

// postCreate submits the create form with the given fields, attaching the cookie
// and (unless csrf is the sentinel "<omit>") a csrf_token field.
func postCreate(h http.Handler, c *http.Cookie, csrf string, fields url.Values) *httptest.ResponseRecorder {
	if fields == nil {
		fields = url.Values{}
	}
	if csrf != "<omit>" {
		fields.Set("csrf_token", csrf)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/create", strings.NewReader(fields.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	return do(h, req)
}

// validCreateForm is a complete, valid submission (sans csrf_token).
func validCreateForm() url.Values {
	return url.Values{
		"title":        {"  Operator Desk Note  "},
		"section":      {"world"},
		"author":       {"zoe"},
		"topics":       {"go, ai , go"}, // duplicate + spacing exercised end to end
		"published_at": {"2026-06-20T08:30"},
		"body":         {"# Heading\n\nUNIQUEBODYMARKER body text."},
		"metadata":     {`{"image":"https://example/y.jpg"}`},
	}
}

func TestCreateRequiresSession(t *testing.T) {
	h := newCreateHandler(t, (&publishStub{}).fn)

	rec := do(h, httptest.NewRequest(http.MethodGet, "/admin/create", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("no-cookie GET status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("Location = %q, want /admin/login", loc)
	}
}

func TestCreateFormConfigured(t *testing.T) {
	h := newCreateHandler(t, (&publishStub{}).fn)
	c := loginCookie(t, h)
	body, csrf := getCreate(t, h, c)

	if csrf == "" {
		t.Errorf("create form missing csrf token")
	}
	if !strings.Contains(body, `action="/admin/create"`) {
		t.Errorf("create POST form not rendered")
	}
	for _, name := range []string{
		`name="title"`, `name="section"`, `name="author"`, `name="topics"`,
		`name="published_at"`, `name="body"`, `name="metadata"`, `name="csrf_token"`,
	} {
		if !strings.Contains(body, name) {
			t.Errorf("create form missing input %s", name)
		}
	}
	// Datalist typeahead is seeded from existing facet values.
	if !strings.Contains(body, `value="ada"`) || !strings.Contains(body, `value="tech"`) {
		t.Errorf("create form missing datalist suggestions from facets")
	}
	// The masthead exposes the New article link on an authed page.
	if !strings.Contains(body, `href="/admin/create"`) {
		t.Errorf("masthead missing the New article link")
	}
}

func TestCreateNotConfigured(t *testing.T) {
	h := newCreateHandler(t, nil) // Publish disabled
	c := loginCookie(t, h)
	body, csrf := getCreate(t, h, c)

	if !strings.Contains(body, "not configured") {
		t.Errorf("disabled create page missing the not-configured note")
	}
	if strings.Contains(body, `action="/admin/create"`) {
		t.Errorf("a create form was rendered while the action is disabled")
	}
	// A POST is answered friendly (no panic), not a 500.
	rec := postCreate(h, c, csrf, validCreateForm())
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled POST status = %d, want 200 (friendly)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not configured") {
		t.Errorf("disabled POST missing the not-configured note")
	}
}

func TestCreateSubmitSuccess(t *testing.T) {
	stub := &publishStub{result: adminweb.CreateArticleResult{ID: "a1", Slug: "operator-desk-note", Created: true}}
	h := newCreateHandler(t, stub.fn)
	c := loginCookie(t, h)
	_, csrf := getCreate(t, h, c)

	rec := postCreate(h, c, csrf, validCreateForm())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if stub.calls != 1 {
		t.Fatalf("Publish closure called %d times, want 1", stub.calls)
	}

	// The handler parsed the raw form into a clean CreateArticleInput.
	in := stub.lastIn
	if in.Title != "Operator Desk Note" {
		t.Errorf("Title = %q, want trimmed %q", in.Title, "Operator Desk Note")
	}
	if in.Author != "zoe" || in.Section != "world" {
		t.Errorf("Author/Section = %q/%q, want zoe/world", in.Author, in.Section)
	}
	if got := strings.Join(in.Topics, ","); got != "go,ai,go" {
		t.Errorf("Topics = %v, want trimmed split [go ai go] (server dedups)", in.Topics)
	}
	if in.PublishedAt == nil || !in.PublishedAt.Equal(time.Date(2026, 6, 20, 8, 30, 0, 0, time.UTC)) {
		t.Errorf("PublishedAt = %v, want 2026-06-20T08:30:00Z", in.PublishedAt)
	}
	if in.Metadata["image"] != "https://example/y.jpg" {
		t.Errorf("Metadata image = %v, want the typed URL", in.Metadata["image"])
	}

	// The success summary links to the article and the form is cleared.
	body := rec.Body.String()
	if !strings.Contains(body, "Article published") {
		t.Errorf("success summary missing")
	}
	if !strings.Contains(body, `href="/admin/articles/operator-desk-note"`) {
		t.Errorf("success summary missing the article detail link")
	}
	if strings.Contains(body, "UNIQUEBODYMARKER") {
		t.Errorf("form was not cleared after a successful publish")
	}
}

func TestCreateIdempotentReplayShown(t *testing.T) {
	stub := &publishStub{result: adminweb.CreateArticleResult{ID: "a1", Slug: "dup", Created: false}}
	h := newCreateHandler(t, stub.fn)
	c := loginCookie(t, h)
	_, csrf := getCreate(t, h, c)

	rec := postCreate(h, c, csrf, validCreateForm())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Already published") {
		t.Errorf("idempotent/dedup outcome (Created=false) not surfaced distinctly")
	}
}

func TestCreateRequiredFieldInline(t *testing.T) {
	stub := &publishStub{}
	h := newCreateHandler(t, stub.fn)
	c := loginCookie(t, h)
	_, csrf := getCreate(t, h, c)

	f := validCreateForm()
	f.Set("title", "   ") // blank after trim
	rec := postCreate(h, c, csrf, f)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if stub.calls != 0 {
		t.Errorf("Publish was called despite an admin-side validation failure")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "required") {
		t.Errorf("missing-title page does not show a required-field error")
	}
	// Other typed values are preserved so nothing is retyped.
	if !strings.Contains(body, `value="zoe"`) {
		t.Errorf("author value not preserved on a validation error")
	}
	if !strings.Contains(body, "UNIQUEBODYMARKER") {
		t.Errorf("body text not preserved on a validation error")
	}
}

func TestCreateMetadataInvalid(t *testing.T) {
	// Every non-object shape is rejected admin-side with a clear field error and no
	// call to the write API: not-JSON, a JSON array, a scalar, JSON null (a nil map),
	// and trailing junk after the object.
	for _, md := range []string{
		"not json at all",
		"[1,2,3]",
		"42",
		"null",
		`{"a":1} trailing`,
	} {
		t.Run(md, func(t *testing.T) {
			stub := &publishStub{}
			h := newCreateHandler(t, stub.fn)
			c := loginCookie(t, h)
			_, csrf := getCreate(t, h, c)

			f := validCreateForm()
			f.Set("metadata", md)
			rec := postCreate(h, c, csrf, f)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", rec.Code)
			}
			if stub.calls != 0 {
				t.Errorf("Publish was called despite invalid metadata %q", md)
			}
			if !strings.Contains(rec.Body.String(), "JSON object") {
				t.Errorf("metadata %q did not produce a clear field error", md)
			}
		})
	}
}

func TestCreateServerValidationErrorNoFields(t *testing.T) {
	// A 422 carrying no field map (e.g. unrenderable_body) still needs a visible
	// top-level message, not a silent 422.
	stub := &publishStub{err: &adminweb.CreateValidationError{Message: "unrenderable_body"}}
	h := newCreateHandler(t, stub.fn)
	c := loginCookie(t, h)
	_, csrf := getCreate(t, h, c)

	rec := postCreate(h, c, csrf, validCreateForm())
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if stub.calls != 1 {
		t.Errorf("Publish closure calls = %d, want 1", stub.calls)
	}
	if !strings.Contains(rec.Body.String(), "unrenderable_body") {
		t.Errorf("field-less validation error message not surfaced to the operator")
	}
}

func TestCreatePublishedAtInvalid(t *testing.T) {
	stub := &publishStub{}
	h := newCreateHandler(t, stub.fn)
	c := loginCookie(t, h)
	_, csrf := getCreate(t, h, c)

	f := validCreateForm()
	f.Set("published_at", "not-a-time")
	rec := postCreate(h, c, csrf, f)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if stub.calls != 0 {
		t.Errorf("Publish was called despite an unparseable published_at")
	}
}

func TestCreateServerValidationError(t *testing.T) {
	stub := &publishStub{err: &adminweb.CreateValidationError{
		Message: "validation_failed",
		Fields:  map[string]string{"body": "too short for the desk"},
	}}
	h := newCreateHandler(t, stub.fn)
	c := loginCookie(t, h)
	_, csrf := getCreate(t, h, c)

	rec := postCreate(h, c, csrf, validCreateForm())
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (server validation)", rec.Code)
	}
	if stub.calls != 1 {
		t.Errorf("Publish closure calls = %d, want 1", stub.calls)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "too short for the desk") {
		t.Errorf("server-side field error not rendered inline")
	}
	// Values are preserved so the operator can fix and resubmit.
	if !strings.Contains(body, "UNIQUEBODYMARKER") {
		t.Errorf("body not preserved after a server validation error")
	}
}

func TestCreateServerError(t *testing.T) {
	stub := &publishStub{err: errors.New("contact publish API: connection refused")}
	h := newCreateHandler(t, stub.fn)
	c := loginCookie(t, h)
	_, csrf := getCreate(t, h, c)

	rec := postCreate(h, c, csrf, validCreateForm())
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (server problem)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "connection refused") {
		t.Errorf("server error detail not surfaced to the operator")
	}
	// A server-side failure keeps the typed values so a retry is one click away.
	if !strings.Contains(body, "UNIQUEBODYMARKER") {
		t.Errorf("body not preserved after a server error")
	}
}

func TestCreateCSRFAndSession(t *testing.T) {
	stub := &publishStub{result: adminweb.CreateArticleResult{Slug: "x"}}
	h := newCreateHandler(t, stub.fn)
	c := loginCookie(t, h)
	_, csrf := getCreate(t, h, c)

	t.Run("no csrf -> 403", func(t *testing.T) {
		if rec := postCreate(h, c, "<omit>", validCreateForm()); rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})
	t.Run("wrong csrf -> 403", func(t *testing.T) {
		if rec := postCreate(h, c, "wrong-token", validCreateForm()); rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})
	t.Run("no session -> 303", func(t *testing.T) {
		if rec := postCreate(h, nil, csrf, validCreateForm()); rec.Code != http.StatusSeeOther {
			t.Errorf("status = %d, want 303", rec.Code)
		}
	})
	if stub.calls != 0 {
		t.Errorf("Publish was reached despite csrf/session rejection (calls=%d)", stub.calls)
	}
}
