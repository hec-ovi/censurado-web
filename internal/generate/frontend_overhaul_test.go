package generate

import (
	"strings"
	"testing"
	"time"
)

// The frontend overhaul: authored subtitle/description, always-visible author
// signatures, no image placeholder, a clickable "Recomendado" rail, an
// article-page rail + "Relacionados", and the "Nosotros" roster. These tests pin
// the new markup and the byte-stability guarantee the article rail/related rely
// on.

// A card with no image renders text-only: subtitle + signature, and crucially NO
// placeholder box. A card with an image renders the media figure.
func TestCard_SubtitleSignature_NoPlaceholder(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title: "Sin imagen", Author: "ada", Section: "politics",
		Published: date(2026, 6, 3),
		Metadata: map[string]any{
			"author_name": "Ada L.",
			"subtitle":    "Un dek breve y verificable.",
		},
	})
	seed(t, repo, seedSpec{
		Title: "Con imagen", Author: "lin", Section: "tech",
		Published: date(2026, 6, 4),
		Metadata: map[string]any{
			"author_name": "Lin X.",
			"subtitle":    "La inferencia local, con números.",
			"image":       "/media/" + strings.Repeat("a", 64) + ".png",
		},
	})
	genInto(t, repo, out, nil)
	listing := string(readArtifact(t, out, "latest/index.html"))

	if strings.Contains(listing, "card-media-fallback") {
		t.Errorf("listing still renders the removed placeholder box")
	}
	if !strings.Contains(listing, `<p class="card-subtitle">Un dek breve y verificable.</p>`) {
		t.Errorf("card subtitle not rendered")
	}
	// Author signature is visible (not display:none in markup) with the byline link.
	if !strings.Contains(listing, `<div class="card-meta">`) ||
		!strings.Contains(listing, `class="author-link" href="/author/ada/" data-author>Ada L.</a>`) {
		t.Errorf("card author signature missing")
	}
	// The signature signs Name then portrait: the byline precedes the author-avatar.
	if b, av := strings.Index(listing, `class="byline"`), strings.Index(listing, `class="author-avatar"`); b < 0 || av < 0 || b > av {
		t.Errorf("card signature should read name then avatar: bylineIdx=%d avatarIdx=%d", b, av)
	}
	// Text-only vs media-bearing cards are marked, and only the media one has a figure.
	if !strings.Contains(listing, "card card-textonly") {
		t.Errorf("text-only card not marked card-textonly")
	}
	if !strings.Contains(listing, "card card-has-media") || !strings.Contains(listing, `<figure class="card-media">`) {
		t.Errorf("image card missing card-has-media figure")
	}
}

// The card carries the AR-local date stamp (long Spanish date, then AM/PM time) fed
// by CreatedAt, in its own .card-date line above the author signature.
func TestCard_SignatureStamp(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title: "Con fecha", Author: "ada", Section: "politics",
		Published: time.Date(2026, 5, 23, 20, 23, 0, 0, time.UTC),
		Created:   time.Date(2026, 5, 23, 20, 23, 0, 0, time.UTC), // 17:23 AR -> the displayed stamp
		Metadata:  map[string]any{"author_name": "Ada L."},
	})
	genInto(t, repo, out, nil)
	listing := string(readArtifact(t, out, "latest/index.html"))

	want := `<time class="card-date" datetime="2026-05-23T20:23:00Z">23 de mayo de 2026, 05:23PM</time>`
	if !strings.Contains(listing, want) {
		t.Errorf("card date stamp missing or wrong; want %q", want)
	}
	// The date renders above the signature (before the byline) within the card.
	if d, b := strings.Index(listing, `card-date`), strings.Index(listing, `class="byline"`); d < 0 || b < 0 || d > b {
		t.Errorf("date should precede the byline: dateIdx=%d bylineIdx=%d", d, b)
	}
}

// The article view's date carries the AR clock time too (long Spanish date, then
// the AM/PM time), so the article page shows the time like the cards do.
func TestArticlePage_DateHasTime(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{
		Title: "Con hora", Author: "ada", Section: "politics", Body: "Cuerpo del artículo.",
		Published: time.Date(2026, 5, 23, 20, 23, 0, 0, time.UTC),
		Created:   time.Date(2026, 5, 23, 20, 23, 0, 0, time.UTC), // 17:23 AR -> shown in the footer
		Metadata:  map[string]any{"author_name": "Ada L."},
	})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, articlePath(a)))

	if !strings.Contains(page, `class="article-date"`) {
		t.Fatalf("article page missing the article-date element")
	}
	if !strings.Contains(page, "05:23PM") {
		t.Errorf("article date should include the AR clock time 05:23PM")
	}
	// The date now sits under the subtitle in the header, above the signature footer.
	if d, s := strings.Index(page, `class="article-date"`), strings.Index(page, `class="article-sign"`); d < 0 || s < 0 || d > s {
		t.Errorf("article date should precede the signature footer: dateIdx=%d signIdx=%d", d, s)
	}
	if strings.Contains(page, `class="article-sign">`) && strings.Contains(page[strings.Index(page, `class="article-sign"`):], `class="article-date"`) {
		t.Errorf("article date should not be inside the signature footer anymore")
	}
}

// The article header (title) comes BEFORE the hero image: the piece leads with its
// title, then the illustration, not the other way round.
func TestArticlePage_TitleBeforeHero(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{
		Title: "Con hero", Author: "ada", Section: "tech", Body: "Cuerpo.",
		Published: date(2026, 6, 5),
		Metadata: map[string]any{
			"author_name": "Ada L.",
			"image":       "/media/" + strings.Repeat("c", 64) + ".png",
		},
	})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, articlePath(a)))

	title, hero := strings.Index(page, `class="article-title"`), strings.Index(page, `class="hero"`)
	if title < 0 || hero < 0 {
		t.Fatalf("article missing title (%d) or hero (%d)", title, hero)
	}
	if title > hero {
		t.Errorf("article title should precede the hero image: titleIdx=%d heroIdx=%d", title, hero)
	}
}

// The first article of each new day renders as that day's full-width lead (same as
// the portada lead), so a scrolled-back day opens like the front page. The day
// separator carries the thick rule and the day-first card spans the row.
func TestPerDayLead_FirstOfDayIsLead(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Jun 5 = today's lead; Jun 4 has a lead (first of that day) + a regular card.
	seed(t, repo, seedSpec{Title: "Hoy", Author: "ada", Section: "tech", Published: date(2026, 6, 5),
		Metadata: map[string]any{"author_name": "Ada L.", "subtitle": "Dek hoy."}})
	seed(t, repo, seedSpec{Title: "Ayer Lead", Author: "ada", Section: "tech", Published: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC),
		Metadata: map[string]any{"author_name": "Ada L.", "subtitle": "Dek ayer uno."}})
	seed(t, repo, seedSpec{Title: "Ayer Dos", Author: "lin", Section: "tech", Published: time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC),
		Metadata: map[string]any{"author_name": "Lin X.", "subtitle": "Dek ayer dos."}})
	genInto(t, repo, out, nil)

	listing := string(readArtifact(t, out, "latest/index.html"))
	css := string(readArtifact(t, out, "assets/style.css"))

	// Two lead cards: the global lead (Hoy) and the day-lead (Ayer Lead).
	if n := strings.Count(listing, "card lead-card"); n != 2 {
		t.Errorf("want 2 lead cards (global + day lead), got %d", n)
	}
	// The second card of the day stays a regular grid card.
	if !strings.Contains(listing, `<article class="card card-textonly">`) {
		t.Errorf("the day's non-lead card should be a regular article_card")
	}
	// The day separator carries the thick opening rule, and the day-first card spans
	// the full row on the grid.
	if !strings.Contains(css, "border-bottom: 4px solid var(--color-ink)") {
		t.Errorf("day separator missing the thick opening rule")
	}
	if !strings.Contains(css, ".day-separator + .article-item") {
		t.Errorf("CSS missing the per-day-lead full-width rule")
	}
	// Media cards reorder to date -> image -> title -> subtitle via CSS order.
	if !strings.Contains(css, ".card:not(.lead-card).card-has-media .card-date") {
		t.Errorf("CSS missing the media-card date-first reorder")
	}
}

// The article page renders the authored subtitle + standfirst and the signed
// footer (circled portrait + name).
func TestArticlePage_SubtitleStandfirstSignature(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{
		Title: "Pieza", Author: "ada", Section: "politics", Body: "Cuerpo del artículo.",
		Published: date(2026, 6, 5),
		Metadata: map[string]any{
			"author_name": "Ada L.",
			"subtitle":    "El dek de la pieza.",
			"description": "El standfirst que resume la pieza en una o dos frases.",
		},
	})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, articlePath(a)))
	css := string(readArtifact(t, out, "assets/style.css"))

	for _, want := range []string{
		`<p class="article-subtitle">El dek de la pieza.</p>`,
		`<p class="article-standfirst">El standfirst que resume la pieza en una o dos frases.</p>`,
		`<footer class="article-sign">`,
		`class="author-link article-sign-name" href="/author/ada/" rel="author" data-author>Ada L.</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("article page missing %q", want)
		}
	}
	// The footer signs Name then portrait: the name link precedes the author-avatar.
	if n, av := strings.Index(page, `article-sign-name`), strings.Index(page, `class="author-avatar"`); n < 0 || av < 0 || n > av {
		t.Errorf("article signature should read name then avatar: nameIdx=%d avatarIdx=%d", n, av)
	}
	for _, want := range []string{
		`.article-standfirst {`,
		`max-width: 100%;`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("article standfirst CSS missing %q", want)
		}
	}
}

// The listing "Recomendado" rail is clickable: each item is an <a> to a permalink.
func TestListingRail_Clickable(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Seed more than one page so the landing's "Recomendado" rail has below-the-fold
	// articles to show (the rail is now drawn from articles NOT on the page's grid).
	seed(t, repo, seedSpec{Title: "Uno", Author: "ada", Section: "tech", Published: date(2026, 6, 1)})
	seed(t, repo, seedSpec{Title: "Dos", Author: "ada", Section: "tech", Published: date(2026, 6, 2)})
	seed(t, repo, seedSpec{Title: "Tres", Author: "ada", Section: "tech", Published: date(2026, 6, 3)})
	seed(t, repo, seedSpec{Title: "Cuatro", Author: "ada", Section: "tech", Published: date(2026, 6, 4)})
	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 })
	listing := string(readArtifact(t, out, "latest/index.html"))

	if strings.Contains(listing, `<div class="ranked-link">`) {
		t.Errorf("rail still uses a non-clickable <div>")
	}
	if !strings.Contains(listing, `<a class="ranked-link" href="/a/`) {
		t.Errorf("rail items are not links to permalinks")
	}
	// The rail's Spanish heading renders when the rail is populated.
	if !strings.Contains(listing, "Recomendado") {
		t.Errorf("rail heading 'Recomendado' missing")
	}
}

// A permalink's "Más de este autor" rail and Relacionados are built only from
// articles NOT newer than itself, so publishing a NEWER article never changes an
// older permalink's bytes (append-only immutability), while the newer article
// links back to the author's older piece in the rail and to the shared-topic
// older piece in the related block.
func TestArticleAuthorMoreRelated_UpToSelfStable(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{Title: "Primero", Author: "ada", Section: "tech", Topics: []string{"go"}, Published: date(2026, 6, 1)})
	genInto(t, repo, out, nil)
	aBefore := string(readArtifact(t, out, articlePath(a)))

	// The "Más de este autor" bar always renders (even empty); the oldest article
	// has no static items (its up-to-self set is empty) and no related.
	if !strings.Contains(aBefore, `class="article-rail author-more"`) || !strings.Contains(aBefore, `data-author-more="ada"`) {
		t.Errorf("the 'Más de este autor' bar should always render with the author slug")
	}
	if strings.Contains(aBefore, `class="author-more-link"`) || strings.Contains(aBefore, `class="related-item"`) {
		t.Errorf("oldest article should have no static author-more items / related")
	}

	b := seed(t, repo, seedSpec{Title: "Segundo", Author: "ada", Section: "tech", Topics: []string{"go"}, Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)

	if string(readArtifact(t, out, articlePath(a))) != aBefore {
		t.Errorf("older permalink bytes changed after a newer article was published")
	}
	bPage := string(readArtifact(t, out, articlePath(b)))
	if !strings.Contains(bPage, `class="article-rail author-more"`) ||
		!strings.Contains(bPage, "Más de este autor") ||
		!strings.Contains(bPage, `<a class="author-more-link" href="`+articleURL(a)) {
		t.Errorf("newer article 'Más de este autor' rail does not link to the author's older piece")
	}
	if strings.Contains(bPage, "Recomendado") {
		t.Errorf("article page should not show the listing 'Recomendado' rail")
	}
	if !strings.Contains(bPage, `class="related-item"`) || !strings.Contains(bPage, articleURL(a)) {
		t.Errorf("newer article Relacionados does not include the shared-topic older one")
	}
}

// Topic labels render normalized (lowercase, no accents) regardless of how the
// topic was stored, so "Análisis Político", "política" and "politica" all read
// consistently.
func TestTopicLabels_Normalized(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{Title: "Pieza", Author: "ada", Section: "world", Topics: []string{"Análisis Político"}, Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, articlePath(a)))

	if !strings.Contains(page, `>analisis-politico</a>`) {
		t.Errorf("topic label not normalized to slug form")
	}
	if strings.Contains(page, "Análisis Político") {
		t.Errorf("raw accented/capitalized topic label leaked into output")
	}
}

// The nav menu does not repeat a label: a section and a topic that read the same
// (the section "Política" vs the topic "política") collapse to one entry.
func TestNav_NoDuplicateLabel(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Reforma", Author: "ada", Section: "politics", Topics: []string{"política"}, Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)
	listing := string(readArtifact(t, out, "latest/index.html"))

	if c := strings.Count(listing, `<a class="site-nav-link" href="/section/politics/">Política</a>`); c != 1 {
		t.Errorf("section 'Política' nav link count = %d, want 1", c)
	}
	if strings.Contains(listing, `class="site-nav-link" href="/topic/politica/"`) {
		t.Errorf("a topic that reads like the 'Política' section leaked into the nav")
	}
}

// The About page is titled "Nosotros" and lists authors by the persona-agnostic
// editorial order: most-published first, tie-broken by the author's earliest
// inserted article, then slug ascending. So a more-prolific author precedes a
// less-prolific one even when the latter's slug sorts earlier.
func TestAutores_RenameAndOrder(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// "ada" sorts before "cy" and is inserted first, so neither alphabetical nor
	// insertion order would put "cy" first. Give "cy" two articles and "ada" one
	// so only the most-published rule explains "cy" leading.
	seed(t, repo, seedSpec{Title: "Reforma", Author: "ada", Section: "politics", Published: date(2026, 6, 1)})
	seed(t, repo, seedSpec{Title: "Mercados", Author: "cy", Section: "economics", Published: date(2026, 6, 2)})
	seed(t, repo, seedSpec{Title: "Bonos", Author: "cy", Section: "economics", Published: date(2026, 6, 3)})
	genInto(t, repo, out, nil)
	about := string(readArtifact(t, out, "about/index.html"))

	if !strings.Contains(about, `<h1 class="listing-heading">Nosotros</h1>`) {
		t.Errorf("about page is not titled 'Nosotros'")
	}
	cy := strings.Index(about, `href="/author/cy/"`)
	ada := strings.Index(about, `href="/author/ada/"`)
	if cy < 0 || ada < 0 {
		t.Fatalf("about page missing an author card (cy=%d ada=%d)", cy, ada)
	}
	if cy > ada {
		t.Errorf("ordering broken: more-prolific 'cy' should precede 'ada' even though 'ada' < 'cy'")
	}
}

// Each Nosotros card carries its author's beat: data-section on the <li> (for the
// theme color) and the Spanish beat label beside the name. The portrait is the
// rectangular treatment (no circular author-card-text wrapper), and the bio sits
// in its own full-width block. Desktop cards alternate portrait/rule alignment.
func TestAutores_ThemedCardCarriesBeat(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title: "Reforma", Author: "ada", Section: "politics",
		Published: date(2026, 6, 2),
		Metadata: map[string]any{
			"author_name":   "Ada L.",
			"author_bio":    "Ada cubre política y tecnología y persigue al poder por donde no quiere ser visto.",
			"author_avatar": "/media/ada.png",
		},
	})
	seed(t, repo, seedSpec{
		Title: "Paper", Author: "lin", Section: "tech",
		Published: date(2026, 6, 1),
		Metadata:  map[string]any{"author_name": "Lin X.", "author_avatar": "/media/lin.png"},
	})
	// Bob publishes under the "literatura" section, which maps to the Spanish
	// label "Cultura y literatura" rather than the raw lowercase slug.
	seed(t, repo, seedSpec{
		Title: "Cuento", Author: "bob", Section: "literatura",
		Published: date(2026, 5, 31),
		Metadata:  map[string]any{"author_name": "Bob Y.", "author_avatar": "/media/bob.png"},
	})
	// Cy publishes under "economics", which maps to "Misterio y conspiración"
	// (the section's reader-facing label is remapped, the URL slug stays English).
	seed(t, repo, seedSpec{
		Title: "Pacto", Author: "cy", Section: "economics",
		Published: date(2026, 5, 30),
		Metadata:  map[string]any{"author_name": "Cy Z.", "author_avatar": "/media/cy.png"},
	})
	genInto(t, repo, out, nil)
	about := string(readArtifact(t, out, "about/index.html"))
	css := string(readArtifact(t, out, "assets/style.css"))

	// data-section themes the card by beat.
	if !strings.Contains(about, `<li class="author-card" data-section="politics">`) {
		t.Errorf("politics author card missing data-section=politics:\n%s", about)
	}
	if !strings.Contains(about, `<li class="author-card" data-section="tech">`) {
		t.Errorf("tech author card missing data-section=tech")
	}
	if !strings.Contains(about, `<li class="author-card" data-section="literatura">`) {
		t.Errorf("literatura author card missing data-section=literatura")
	}
	if !strings.Contains(about, `<li class="author-card" data-section="economics">`) {
		t.Errorf("economics author card missing data-section=economics")
	}
	// The Spanish beat label renders beside the name.
	if !strings.Contains(about, `<p class="author-card-beat">Política</p>`) {
		t.Errorf("politics card missing Spanish beat label 'Política'")
	}
	if !strings.Contains(about, `<p class="author-card-beat">Tecnología</p>`) {
		t.Errorf("tech card missing Spanish beat label 'Tecnología'")
	}
	if !strings.Contains(about, `<p class="author-card-beat">Cultura y literatura</p>`) {
		t.Errorf("literatura card missing remapped Spanish beat label 'Cultura y literatura'")
	}
	if !strings.Contains(about, `<p class="author-card-beat">Misterio y conspiración</p>`) {
		t.Errorf("economics card missing remapped Spanish beat label 'Misterio y conspiración'")
	}
	// The redesigned card replaces the old author-card-text wrapper with a head
	// block; the bio is its own full-width sibling.
	if strings.Contains(about, `class="author-card-text"`) {
		t.Errorf("old author-card-text wrapper should be gone after the redesign")
	}
	if !strings.Contains(about, `<div class="author-card-head">`) {
		t.Errorf("redesigned card missing author-card-head block")
	}
	if !strings.Contains(about, `<p class="author-card-bio">Ada cubre política`) {
		t.Errorf("politics card missing the first-person bio block")
	}
	for _, want := range []string{
		`.about-manifesto {`,
		`border-left: 5px solid var(--color-red);`,
		`.author-card:nth-child(even)`,
		`border-right: 4px solid var(--author-accent);`,
		`border-left: 0;`,
		`.author-card-bio {`,
		`width: 100%;`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("author card CSS missing %q", want)
		}
	}
}
