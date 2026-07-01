package generate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web-backend/store"
)

// upsertPortada type-asserts the test repo to the PortadaStore registry (the concrete
// *sqlite.Store implements it) and writes one per-day plan.
func upsertPortada(t *testing.T, repo store.Repository, p store.PortadaDay) {
	t.Helper()
	ps, ok := repo.(store.PortadaStore)
	if !ok {
		t.Fatalf("repo does not implement store.PortadaStore")
	}
	if _, err := ps.UpsertPortada(context.Background(), p); err != nil {
		t.Fatalf("UpsertPortada(%q): %v", p.Date, err)
	}
}

// dayAt is a UTC publish instant on the given day at hour h, so same-day articles get a
// deterministic newest-first default order.
func dayAt(y, m, d, h int) time.Time {
	return time.Date(y, time.Month(m), d, h, 0, 0, 0, time.UTC)
}

// liFor returns the <li ...> ... </li> slice of an article-list page that wraps the
// given permalink, so a per-card class assertion cannot match a different card.
func liFor(t *testing.T, page, permalink string) string {
	t.Helper()
	at := strings.Index(page, permalink)
	if at < 0 {
		t.Fatalf("permalink %q not on page", permalink)
	}
	start := strings.LastIndex(page[:at], "<li ")
	if start < 0 {
		t.Fatalf("no <li> wrapping %q", permalink)
	}
	end := strings.Index(page[at:], "</li>")
	if end < 0 {
		t.Fatalf("<li> wrapping %q is not closed", permalink)
	}
	return page[start : at+end]
}

// rankedRail returns the <aside class="ranked-rail"> ... </aside> slice of a landing
// page (the "Recomendado" block), or fails if absent.
func rankedRail(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `<aside class="ranked-rail"`)
	if i < 0 {
		t.Fatalf("page has no ranked-rail")
	}
	rest := page[i:]
	j := strings.Index(rest, "</aside>")
	if j < 0 {
		t.Fatalf("ranked-rail is not closed")
	}
	return rest[:j]
}

// TestPortada_CuratedOrderRolesAndShard proves a per-day plan reorders a day on the
// front page (server HTML and the shard both), promotes the plan's first entry to the
// day's lead, and marks a role:"important" entry as a full-width card.
func TestPortada_CuratedOrderRolesAndShard(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	// Three articles on 2026-06-10 (default newest-first order would be C, B, A) plus
	// one on the previous day.
	a := seed(t, repo, seedSpec{Title: "Alfa", Author: "ada", Section: "politics", Published: dayAt(2026, 6, 10, 9)})
	b := seed(t, repo, seedSpec{Title: "Beta", Author: "bob", Section: "world", Published: dayAt(2026, 6, 10, 10)})
	c := seed(t, repo, seedSpec{Title: "Gamma", Author: "lin", Section: "tech", Published: dayAt(2026, 6, 10, 11)})
	z := seed(t, repo, seedSpec{Title: "Zeta", Author: "ada", Section: "politics", Published: dayAt(2026, 6, 9, 9)})

	// Curate 2026-06-10 as A (lead), C (important), B.
	upsertPortada(t, repo, store.PortadaDay{
		Date: "2026-06-10",
		Entries: []store.PortadaEntry{
			{Slug: a.Slug, Role: "main"},
			{Slug: c.Slug, Role: "important"},
			{Slug: b.Slug, Role: ""},
		},
	})

	genInto(t, repo, out, nil)

	page := string(readArtifact(t, out, "latest/index.html"))
	got := permalinksIn([]byte(page))
	wantOrder := []string{articleURL(a), articleURL(c), articleURL(b), articleURL(z)}
	if strings.Join(got, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("front-page order = %v, want curated %v", got, wantOrder)
	}

	// A is the day's lead (first of the day) -> lead_card, never is-important.
	if lead := liFor(t, page, articleURL(a)); strings.Contains(lead, "is-important") {
		t.Errorf("curated lead A must not be is-important")
	}
	if !strings.Contains(page, `class="card lead-card`) {
		t.Errorf("front page has no lead card")
	}
	// C is role:important and NOT the lead -> its <li> spans the row (is-important).
	if li := liFor(t, page, articleURL(c)); !strings.Contains(li, "is-important") {
		t.Errorf("important card C is missing the is-important class: %s", li)
	}
	// B is a normal card.
	if li := liFor(t, page, articleURL(b)); strings.Contains(li, "is-important") {
		t.Errorf("normal card B must not be is-important")
	}

	// The shard reflects the curated order and carries ord/role.
	var es []ShardEntry
	if err := json.Unmarshal(readArtifact(t, out, "shards/latest/2026/06.json"), &es); err != nil {
		t.Fatalf("decode latest shard: %v", err)
	}
	bySlug := map[string]ShardEntry{}
	for _, e := range es {
		bySlug[e.Slug] = e
	}
	if bySlug[a.Slug].Ord != 0 || bySlug[c.Slug].Ord != 1 || bySlug[b.Slug].Ord != 2 {
		t.Errorf("shard ord = A:%d C:%d B:%d, want 0/1/2",
			bySlug[a.Slug].Ord, bySlug[c.Slug].Ord, bySlug[b.Slug].Ord)
	}
	if bySlug[c.Slug].Role != "important" {
		t.Errorf("shard role for C = %q, want important", bySlug[c.Slug].Role)
	}
	if bySlug[a.Slug].Role != "" || bySlug[b.Slug].Role != "" {
		t.Errorf("shard role for A/B = %q/%q, want empty", bySlug[a.Slug].Role, bySlug[b.Slug].Role)
	}
	// The day's articles appear in curated order within the shard month file.
	order := []string{}
	for _, e := range es {
		if strings.HasPrefix(e.PublishedAt, "2026-06-10") {
			order = append(order, e.Slug)
		}
	}
	if strings.Join(order, ",") != strings.Join([]string{a.Slug, c.Slug, b.Slug}, ",") {
		t.Errorf("shard within-day order = %v, want [A C B]", order)
	}
}

// TestPortada_DefaultOrderUnchanged proves that with NO plan the front page keeps the
// natural newest-published-first order and no card is marked important, so uncurated
// output is byte-identical to before this feature.
func TestPortada_DefaultOrderUnchanged(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{Title: "Alfa", Author: "ada", Section: "politics", Published: dayAt(2026, 6, 10, 9)})
	b := seed(t, repo, seedSpec{Title: "Beta", Author: "bob", Section: "world", Published: dayAt(2026, 6, 10, 10)})
	c := seed(t, repo, seedSpec{Title: "Gamma", Author: "lin", Section: "tech", Published: dayAt(2026, 6, 10, 11)})

	genInto(t, repo, out, nil)

	page := string(readArtifact(t, out, "latest/index.html"))
	got := permalinksIn([]byte(page))
	want := []string{articleURL(c), articleURL(b), articleURL(a)} // newest first
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("default order = %v, want %v", got, want)
	}
	if strings.Contains(page, "is-important") {
		t.Errorf("no plan, yet a card is marked is-important")
	}
}

// TestPortada_RecomendadoOverridesRail proves the plan's recomendado list becomes the
// front page's "Recomendado" rail, in the operator's stored order, dropping any slug
// with no matching article.
func TestPortada_RecomendadoOverridesRail(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{Title: "Alfa", Author: "ada", Section: "politics", Published: dayAt(2026, 6, 10, 9)})
	b := seed(t, repo, seedSpec{Title: "Beta", Author: "bob", Section: "world", Published: dayAt(2026, 6, 10, 10)})
	c := seed(t, repo, seedSpec{Title: "Gamma", Author: "lin", Section: "tech", Published: dayAt(2026, 6, 10, 11)})

	upsertPortada(t, repo, store.PortadaDay{
		Date:        "2026-06-10",
		Recomendado: []string{b.Slug, a.Slug, "no-such-slug"},
	})

	genInto(t, repo, out, nil)

	page := string(readArtifact(t, out, "latest/index.html"))
	rail := rankedRail(t, page)
	bi := strings.Index(rail, articleURL(b))
	ai := strings.Index(rail, articleURL(a))
	if bi < 0 || ai < 0 {
		t.Fatalf("recomendado rail missing B or A: %s", rail)
	}
	if bi > ai {
		t.Errorf("recomendado rail order wrong: B should precede A")
	}
	if strings.Contains(rail, articleURL(c)) {
		t.Errorf("recomendado rail contains C, which was not recommended")
	}
}
