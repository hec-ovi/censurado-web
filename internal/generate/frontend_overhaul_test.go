package generate

import (
	"strings"
	"testing"
)

// The frontend overhaul: authored subtitle/description, always-visible author
// signatures, no image placeholder, a clickable "Lo más leído" rail, an
// article-page rail + "Relacionados", and the "Autores" roster. These tests pin
// the new markup and the byte-stability guarantee the article rail/related rely
// on.

// A card with no image renders text-only: subtitle + signature, and crucially NO
// placeholder box. A card with an image renders the media figure.
func TestCard_SubtitleSignature_NoPlaceholder(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title: "Sin imagen", Author: "lara-arianna", Section: "politics",
		Published: date(2026, 6, 3),
		Metadata: map[string]any{
			"author_name": "Lara Arianna",
			"subtitle":    "Un dek breve y verificable.",
		},
	})
	seed(t, repo, seedSpec{
		Title: "Con imagen", Author: "vector-omni", Section: "tech",
		Published: date(2026, 6, 4),
		Metadata: map[string]any{
			"author_name": "Vector Omni",
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
		!strings.Contains(listing, `class="author-link" href="/author/lara-arianna/" data-author>Lara Arianna</a>`) {
		t.Errorf("card author signature missing")
	}
	// Text-only vs media-bearing cards are marked, and only the media one has a figure.
	if !strings.Contains(listing, "card card-textonly") {
		t.Errorf("text-only card not marked card-textonly")
	}
	if !strings.Contains(listing, "card card-has-media") || !strings.Contains(listing, `<figure class="card-media">`) {
		t.Errorf("image card missing card-has-media figure")
	}
}

// The article page renders the authored subtitle + standfirst and the signed
// footer (circled portrait + name).
func TestArticlePage_SubtitleStandfirstSignature(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{
		Title: "Pieza", Author: "lara-arianna", Section: "politics", Body: "Cuerpo del artículo.",
		Published: date(2026, 6, 5),
		Metadata: map[string]any{
			"author_name": "Lara Arianna",
			"subtitle":    "El dek de la pieza.",
			"description": "El standfirst que resume la pieza en una o dos frases.",
		},
	})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, articlePath(a)))

	for _, want := range []string{
		`<p class="article-subtitle">El dek de la pieza.</p>`,
		`<p class="article-standfirst">El standfirst que resume la pieza en una o dos frases.</p>`,
		`<footer class="article-sign">`,
		`class="author-link article-sign-name" href="/author/lara-arianna/" rel="author" data-author>Lara Arianna</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("article page missing %q", want)
		}
	}
}

// The listing "Lo más leído" rail is clickable: each item is an <a> to a permalink.
func TestListingRail_Clickable(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Uno", Author: "ada", Section: "tech", Published: date(2026, 6, 1)})
	seed(t, repo, seedSpec{Title: "Dos", Author: "ada", Section: "tech", Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)
	listing := string(readArtifact(t, out, "latest/index.html"))

	if strings.Contains(listing, `<div class="ranked-link">`) {
		t.Errorf("rail still uses a non-clickable <div>")
	}
	if !strings.Contains(listing, `<a class="ranked-link" href="/a/`) {
		t.Errorf("rail items are not links to permalinks")
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
	if strings.Contains(bPage, "Lo más leído") {
		t.Errorf("article page should not show the listing 'Lo más leído' rail")
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
	a := seed(t, repo, seedSpec{Title: "Pieza", Author: "lara", Section: "world", Topics: []string{"Análisis Político"}, Published: date(2026, 6, 2)})
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
	seed(t, repo, seedSpec{Title: "Reforma", Author: "lara", Section: "politics", Topics: []string{"política"}, Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)
	listing := string(readArtifact(t, out, "latest/index.html"))

	if c := strings.Count(listing, `<a class="site-nav-link" href="/section/politics/">Política</a>`); c != 1 {
		t.Errorf("section 'Política' nav link count = %d, want 1", c)
	}
	if strings.Contains(listing, `class="site-nav-link" href="/topic/politica/"`) {
		t.Errorf("a topic that reads like the 'Política' section leaked into the nav")
	}
}

// The About page is titled "Autores" and lists authors in the curated editorial
// order (lara-arianna ahead of borge-luis-jorges), not alphabetically.
func TestAutores_RenameAndOrder(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Insert borge first so neither insertion nor alphabetical order would put
	// lara first by accident; only the curated order does.
	seed(t, repo, seedSpec{Title: "Mercados", Author: "borge-luis-jorges", Section: "economics", Published: date(2026, 6, 1)})
	seed(t, repo, seedSpec{Title: "Reforma", Author: "lara-arianna", Section: "politics", Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)
	about := string(readArtifact(t, out, "about/index.html"))

	if !strings.Contains(about, `<h1 class="listing-heading">Autores</h1>`) {
		t.Errorf("about page is not titled 'Autores'")
	}
	lara := strings.Index(about, `href="/author/lara-arianna/"`)
	borge := strings.Index(about, `href="/author/borge-luis-jorges/"`)
	if lara < 0 || borge < 0 {
		t.Fatalf("about page missing an author card (lara=%d borge=%d)", lara, borge)
	}
	if lara > borge {
		t.Errorf("curated order broken: lara-arianna should precede borge-luis-jorges")
	}
}

// Each Autores card carries its author's beat: data-section on the <li> (for the
// theme color) and the Spanish beat label beside the name. The portrait is the
// rectangular treatment (no circular author-card-text wrapper), and the bio sits
// in its own full-width block.
func TestAutores_ThemedCardCarriesBeat(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title: "Reforma", Author: "lara-arianna", Section: "politics",
		Published: date(2026, 6, 2),
		Metadata: map[string]any{
			"author_name":   "Lara Arianna",
			"author_bio":    "Soy Lara Arianna. Persigo al poder por donde no quiere ser visto.",
			"author_avatar": "/media/lara.png",
		},
	})
	seed(t, repo, seedSpec{
		Title: "Paper", Author: "vector-omni", Section: "tech",
		Published: date(2026, 6, 1),
		Metadata:  map[string]any{"author_name": "Vector Omni", "author_avatar": "/media/vector.png"},
	})
	// Glorieta publishes under the "literatura" section, which must map to the
	// Spanish label "Literatura" (capitalized) rather than the raw lowercase slug.
	seed(t, repo, seedSpec{
		Title: "Cuento", Author: "glorieta-sadeta", Section: "literatura",
		Published: date(2026, 5, 31),
		Metadata:  map[string]any{"author_name": "Glorieta Sadeta", "author_avatar": "/media/glorieta.png"},
	})
	genInto(t, repo, out, nil)
	about := string(readArtifact(t, out, "about/index.html"))

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
	// The Spanish beat label renders beside the name.
	if !strings.Contains(about, `<p class="author-card-beat">Política</p>`) {
		t.Errorf("politics card missing Spanish beat label 'Política'")
	}
	if !strings.Contains(about, `<p class="author-card-beat">Tecnología</p>`) {
		t.Errorf("tech card missing Spanish beat label 'Tecnología'")
	}
	if !strings.Contains(about, `<p class="author-card-beat">Literatura</p>`) {
		t.Errorf("literatura card missing capitalized Spanish beat label 'Literatura'")
	}
	// The redesigned card replaces the old author-card-text wrapper with a head
	// block; the bio is its own full-width sibling.
	if strings.Contains(about, `class="author-card-text"`) {
		t.Errorf("old author-card-text wrapper should be gone after the redesign")
	}
	if !strings.Contains(about, `<div class="author-card-head">`) {
		t.Errorf("redesigned card missing author-card-head block")
	}
	if !strings.Contains(about, `<p class="author-card-bio">Soy Lara Arianna.`) {
		t.Errorf("politics card missing the first-person bio block")
	}
}
