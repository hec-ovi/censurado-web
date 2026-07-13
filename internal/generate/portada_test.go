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

// setRecomendado writes the site's single GLOBAL editor's-pick list via the
// RecomendadoStore (the concrete *sqlite.Store implements it).
func setRecomendado(t *testing.T, repo store.Repository, slugs []string) {
	t.Helper()
	rs, ok := repo.(store.RecomendadoStore)
	if !ok {
		t.Fatalf("repo does not implement store.RecomendadoStore")
	}
	if err := rs.SetRecomendado(context.Background(), slugs); err != nil {
		t.Fatalf("SetRecomendado: %v", err)
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

func TestPortada_NewestCuratedDayStaysOnLandingAcrossPageBoundary(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	older := seed(t, repo, seedSpec{Title: "Zeta", Author: "ada", Section: "politics", Published: dayAt(2026, 6, 9, 9)})
	a := seed(t, repo, seedSpec{Title: "Alfa", Author: "ada", Section: "politics", Published: dayAt(2026, 6, 10, 9)})
	b := seed(t, repo, seedSpec{Title: "Beta", Author: "bob", Section: "world", Published: dayAt(2026, 6, 10, 10)})
	c := seed(t, repo, seedSpec{Title: "Gamma", Author: "lin", Section: "tech", Published: dayAt(2026, 6, 10, 11)})

	upsertPortada(t, repo, store.PortadaDay{
		Date:    "2026-06-10",
		Entries: []store.PortadaEntry{{Slug: a.Slug}, {Slug: c.Slug}, {Slug: b.Slug}},
	})

	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 })

	got := permalinksIn(readArtifact(t, out, "latest/index.html"))
	want := []string{articleURL(a), articleURL(c), articleURL(b)}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("front-page order = %v, want whole curated day on landing %v (older=%s)", got, want, articleURL(older))
	}
}

// A new, still-uncurated day must not make the previous curated day's portada
// straddle the landing boundary. The old fixed-size cut left only positions 2
// and 4 on /latest/, while positions 0, 1, and 3 moved to the older page; the
// browser then appended those missing earlier positions after the later ones.
func TestPortada_PreviousCuratedDayStaysWholeWhenNewDayArrives(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	a := seed(t, repo, seedSpec{Title: "Prev A", Author: "ada", Section: "world", Published: dayAt(2026, 6, 10, 9)})
	b := seed(t, repo, seedSpec{Title: "Prev B", Author: "bob", Section: "world", Published: dayAt(2026, 6, 10, 10)})
	c := seed(t, repo, seedSpec{Title: "Prev C", Author: "lin", Section: "world", Published: dayAt(2026, 6, 10, 11)})
	d := seed(t, repo, seedSpec{Title: "Prev D", Author: "ada", Section: "world", Published: dayAt(2026, 6, 10, 12)})
	e := seed(t, repo, seedSpec{Title: "Prev E", Author: "bob", Section: "world", Published: dayAt(2026, 6, 10, 13)})
	upsertPortada(t, repo, store.PortadaDay{
		Date: "2026-06-10",
		Entries: []store.PortadaEntry{
			{Slug: a.Slug, Role: "important"},
			{Slug: c.Slug},
			{Slug: e.Slug},
			{Slug: b.Slug},
			{Slug: d.Slug},
		},
	})

	n1 := seed(t, repo, seedSpec{Title: "New One", Author: "ada", Section: "politics", Published: dayAt(2026, 6, 11, 9)})
	n2 := seed(t, repo, seedSpec{Title: "New Two", Author: "bob", Section: "politics", Published: dayAt(2026, 6, 11, 10)})
	n3 := seed(t, repo, seedSpec{Title: "New Three", Author: "lin", Section: "politics", Published: dayAt(2026, 6, 11, 11)})

	genInto(t, repo, out, func(o *Options) { o.PageSize = 6 })

	page := string(readArtifact(t, out, "latest/index.html"))
	got := permalinksIn([]byte(page))
	want := []string{
		articleURL(n3), articleURL(n2), articleURL(n1),
		articleURL(a), articleURL(c), articleURL(e), articleURL(b), articleURL(d),
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("front-page order = %v, want canonical whole-day prefix %v", got, want)
	}
	if n := strings.Count(page, `class="card lead-card`); n != 2 {
		t.Errorf("lead-card count = %d, want one lead for each complete day", n)
	}
}

// Regression for the n%%P==0 path left behind by the first curated-day fix. It
// used the modified full-page count with the old exact-boundary mirror branch
// and could replace the landing with the oldest page.
func TestPortada_CuratedLandingAtExactPageBoundary(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	seed(t, repo, seedSpec{Title: "Old One", Author: "ada", Section: "world", Published: dayAt(2026, 6, 9, 9)})
	seed(t, repo, seedSpec{Title: "Old Two", Author: "bob", Section: "world", Published: dayAt(2026, 6, 9, 10)})
	seed(t, repo, seedSpec{Title: "Old Three", Author: "lin", Section: "world", Published: dayAt(2026, 6, 9, 11)})
	a := seed(t, repo, seedSpec{Title: "New A", Author: "ada", Section: "world", Published: dayAt(2026, 6, 10, 9)})
	b := seed(t, repo, seedSpec{Title: "New B", Author: "bob", Section: "world", Published: dayAt(2026, 6, 10, 10)})
	c := seed(t, repo, seedSpec{Title: "New C", Author: "lin", Section: "world", Published: dayAt(2026, 6, 10, 11)})
	upsertPortada(t, repo, store.PortadaDay{
		Date:    "2026-06-10",
		Entries: []store.PortadaEntry{{Slug: a.Slug}, {Slug: c.Slug}, {Slug: b.Slug}},
	})

	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 })

	got := permalinksIn(readArtifact(t, out, "latest/index.html"))
	want := []string{articleURL(a), articleURL(c), articleURL(b)}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("exact-boundary landing = %v, want newest curated day %v", got, want)
	}
}

// TestRecomendado_GlobalListIsFrontPageRail proves the site's single GLOBAL
// editor's-pick list becomes the front page's "Recomendado" rail, in the operator's
// stored order, dropping any slug with no matching article (and never the auto
// fallback). The list is day-independent, so it is not attached to any portada.
func TestRecomendado_GlobalListIsFrontPageRail(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{Title: "Alfa", Author: "ada", Section: "politics", Published: dayAt(2026, 6, 10, 9)})
	b := seed(t, repo, seedSpec{Title: "Beta", Author: "bob", Section: "world", Published: dayAt(2026, 6, 10, 10)})
	c := seed(t, repo, seedSpec{Title: "Gamma", Author: "lin", Section: "tech", Published: dayAt(2026, 6, 10, 11)})

	setRecomendado(t, repo, []string{b.Slug, a.Slug, "no-such-slug"})

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
		t.Errorf("recomendado rail contains C, which was not in the curated list")
	}
}

// TestRecomendado_EmptyListStillRendersWidget proves the front-page "Recomendado"
// widget renders even when no global list is set (empty, no auto fallback), so the
// two-column layout holds. The <aside> is present with an empty ranked list.
func TestRecomendado_EmptyListStillRendersWidget(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Alfa", Author: "ada", Section: "politics", Published: dayAt(2026, 6, 10, 9)})
	seed(t, repo, seedSpec{Title: "Beta", Author: "bob", Section: "world", Published: dayAt(2026, 6, 9, 10)})

	genInto(t, repo, out, nil) // no recomendado set

	page := string(readArtifact(t, out, "latest/index.html"))
	rail := rankedRail(t, page) // fails if the <aside> is absent
	if len(railPermalinks([]byte(rail))) != 0 {
		t.Errorf("empty global list should render no rail items (no auto fallback), got: %s", rail)
	}
}
