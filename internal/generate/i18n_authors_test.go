package generate

import (
	"context"
	"strings"
	"testing"
)

// TestSpanishUI_PresentationLayer asserts the public chrome renders in Spanish:
// the document language, the skip link, the byline preposition, the nav, and the
// About link. The brand name "El Censurado Web" stays as-is.
func TestSpanishUI_PresentationLayer(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{Title: "Hola Mundo", Author: "ada", Section: "tech", Published: date(2026, 6, 3)})
	genInto(t, repo, out, nil)

	listing := string(readArtifact(t, out, "latest/index.html"))
	article := string(readArtifact(t, out, articlePath(a)))

	// Document language and skip link.
	if !strings.Contains(listing, `<html lang="es">`) {
		t.Errorf("listing missing <html lang=\"es\">")
	}
	if !strings.Contains(listing, "Saltar al contenido") {
		t.Errorf("listing missing translated skip link 'Saltar al contenido'")
	}

	// Listing heading for /latest/ is "Lo último".
	if !strings.Contains(listing, "Lo último") {
		t.Errorf("listing missing 'Lo último' (Latest heading/nav)")
	}

	// Chrome strings. ("Recomendado" is no longer always-on chrome: the rail only
	// renders on a landing that has below-the-fold articles, so its Spanish heading
	// is asserted in TestListingRail_Clickable instead, where the rail is populated.)
	for _, want := range []string{"Portada", "Menú", "Sistema", "Claro", "Oscuro", "Aviso editorial", "Nosotros"} {
		if !strings.Contains(listing, want) {
			t.Errorf("listing missing translated chrome string %q", want)
		}
	}
	// The brand name is untranslated.
	if !strings.Contains(listing, "El Censurado Web") {
		t.Errorf("listing dropped the brand name 'El Censurado Web'")
	}
	// No leftover English chrome.
	for _, gone := range []string{`<html lang="en">`, "Skip to content", "Editorial Disclosure", "Most Read", ">Menu<"} {
		if strings.Contains(listing, gone) {
			t.Errorf("listing still contains untranslated %q", gone)
		}
	}

	// Article byline preposition: "Por ... en ...".
	if !strings.Contains(article, "Por ") {
		t.Errorf("article byline missing 'Por '")
	}
	if strings.Contains(article, ">By <") || strings.Contains(article, "byline\">By ") {
		t.Errorf("article byline still uses English 'By'")
	}
	// The "Acerca de" nav link points at /about/.
	if !strings.Contains(listing, `href="/about/"`) {
		t.Errorf("listing nav missing the /about/ link")
	}
}

// TestSectionDisplayName_SpanishLabelEnglishSlug proves a tech-section article's
// heading/label renders "Tecnología" while the URL slug stays /section/tech/.
func TestSectionDisplayName_SpanishLabelEnglishSlug(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Chip Wars", Author: "ada", Section: "tech", Published: date(2026, 6, 3)})
	genInto(t, repo, out, nil)

	// The English slug page exists; no /section/tecnologia/ page is created.
	if !exists(out, "section/tech/index.html") {
		t.Fatalf("missing /section/tech/ page (URL slug must stay English)")
	}
	if exists(out, "section/tecnologia/index.html") {
		t.Errorf("a /section/tecnologia/ page was emitted; the slug must stay 'tech'")
	}

	section := string(readArtifact(t, out, "section/tech/index.html"))
	// The heading renders the Spanish display name.
	if !strings.Contains(section, `<h1 class="listing-heading">Tecnología</h1>`) {
		t.Errorf("section page heading is not 'Tecnología'")
	}
	// The scope description uses the Spanish label too.
	if !strings.Contains(section, "sección Tecnología") {
		t.Errorf("section description missing 'sección Tecnología'")
	}
	// The canonical URL slug stays English.
	if !strings.Contains(section, `<link rel="canonical" href="https://news.example/section/tech/">`) {
		t.Errorf("section canonical is not /section/tech/")
	}

	// A section facet link on the latest listing keeps the English href but
	// renders the Spanish label.
	listing := string(readArtifact(t, out, "latest/index.html"))
	if !strings.Contains(listing, `href="/section/tech/" data-section>Tecnología</a>`) {
		t.Errorf("section card link does not pair /section/tech/ href with Tecnología label")
	}
}

// TestAuthorPage_NameAndBioFromMetadata proves a single-author page renders the
// metadata.author_name display label and the author_bio text, while the URL slug
// is derived from the raw author string.
func TestAuthorPage_NameAndBioFromMetadata(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	const bio = "Ada cubre política y tecnología."
	seed(t, repo, seedSpec{
		Title:     "La Ley de IA",
		Author:    "ada",
		Section:   "politics",
		Published: date(2026, 6, 5),
		Metadata: map[string]any{
			"author_name": "Ada L.",
			"author_bio":  bio,
		},
	})
	genInto(t, repo, out, nil)

	// URL slug is derived from the raw author string.
	if !exists(out, "author/ada/index.html") {
		t.Fatalf("missing /author/ada/ page")
	}
	page := string(readArtifact(t, out, "author/ada/index.html"))

	// The profile heading is the display name, not the raw slug.
	if !strings.Contains(page, `<h1 class="author-profile-name">Ada L.</h1>`) {
		t.Errorf("author profile heading is not the display name 'Ada L.'")
	}
	// The profile block renders the name, bio text, and avoids the old duplicate
	// listing hero.
	if !strings.Contains(page, `data-author-profile`) {
		t.Errorf("author page missing the profile block")
	}
	if !strings.Contains(page, "Ada cubre política y tecnología.") {
		t.Errorf("author page missing the bio text")
	}
	if strings.Contains(page, `<p class="eyebrow">Portada</p>`) {
		t.Errorf("author page should not repeat the normal listing hero")
	}
	// Canonical URL keeps the English-derived slug.
	if !strings.Contains(page, `href="https://news.example/author/ada/"`) {
		t.Errorf("author canonical is not /author/ada/")
	}

	// The byline on the article permalink uses the display name, the author link
	// still points at the slug URL.
	plan, err := BuildPlan(context.Background(), repo, 20)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	art := plan.Index.All[0]
	article := string(readArtifact(t, out, articlePath(art)))
	if !strings.Contains(article, ">Ada L.</a>") {
		t.Errorf("article byline does not show display name 'Ada L.'")
	}
	if !strings.Contains(article, `href="/author/ada/"`) {
		t.Errorf("article author link does not point at /author/ada/")
	}
}

func TestAuthorPage_ProfileContentLatestPageSize(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	articles := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		title := "Ada pieza " + string(rune('A'+i))
		a := seed(t, repo, seedSpec{
			Title:     title,
			Author:    "ada",
			Section:   "politics",
			Topics:    []string{"poder", "ia"},
			Published: date(2026, 6, i+1),
			Metadata: map[string]any{
				"author_name":   "Ada L.",
				"author_bio":    "Persigo al poder donde no quiere ser visto.",
				"author_avatar": "/media/ada.png",
			},
		})
		articles = append(articles, articleURL(a))
	}
	// The author landing shows the latest PageSize pieces (then infinite-scrolls the
	// rest), so the initial batch matches every other scope. PageSize 6 exercises
	// Feature 7 on author profiles too.
	genInto(t, repo, out, func(o *Options) { o.PageSize = 6 })
	page := string(readArtifact(t, out, "author/ada/index.html"))

	for _, want := range []string{
		`<section class="author-profile" data-author-profile>`,
		`<div class="author-profile-photo">`,
		`<nav class="author-profile-topics" aria-label="Temas que cubre">`,
		`<p class="eyebrow">Contenido generado</p>`,
		`<ol class="article-list author-grid" data-articles>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("author profile page missing %q", want)
		}
	}
	if strings.Contains(page, `class="ranked-rail"`) {
		t.Errorf("author profile should not render the generic ranked rail")
	}
	listed := permalinksIn([]byte(page))
	if len(listed) != 6 {
		t.Fatalf("author profile listed %d articles, want latest 6 (PageSize)", len(listed))
	}
	wantLatest := articles[6:] // the six newest, June 7..12
	for i := range wantLatest {
		want := wantLatest[len(wantLatest)-1-i] // newest first
		if listed[i] != want {
			t.Fatalf("listed[%d] = %q, want %q", i, listed[i], want)
		}
	}
	if strings.Contains(page, articles[0]) || strings.Contains(page, articles[5]) {
		t.Errorf("author profile leaked articles older than the latest 6")
	}
}

// TestAboutPage_ListsAuthors proves /about/ is emitted and lists each author's
// display name and bio with a link to the single-author page.
func TestAboutPage_ListsAuthors(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title:     "Crypto Crash",
		Author:    "ada",
		Section:   "crypto",
		Published: date(2026, 6, 5),
		Metadata: map[string]any{
			"author_name": "Ada L.",
			"author_bio":  "Reportera de mercados.",
		},
	})
	seed(t, repo, seedSpec{
		Title:     "World Today",
		Author:    "Bob",
		Section:   "world",
		Published: date(2026, 6, 6),
	})
	genInto(t, repo, out, nil)

	if !exists(out, "about/index.html") {
		t.Fatalf("missing about/index.html")
	}
	about := string(readArtifact(t, out, "about/index.html"))

	if !strings.Contains(about, `<h1 class="listing-heading">Nosotros</h1>`) {
		t.Errorf("about page title is not 'Nosotros'")
	}
	if !strings.Contains(about, `<title>Nosotros</title>`) {
		t.Errorf("about document title does not exactly match 'Nosotros'")
	}
	for _, want := range []string{
		`<section class="about-manifesto" aria-label="Manifiesto">`,
		"primer portal de noticias plenamente sintético",
		"Cuidamos la atención del lector",
		"fuentes independientes",
		"dos rondas de autorrevisión",
	} {
		if !strings.Contains(about, want) {
			t.Errorf("about manifesto missing %q", want)
		}
	}
	// Author with metadata: display name, bio, and link to the author page.
	if !strings.Contains(about, "Ada L.") {
		t.Errorf("about page missing author name 'Ada L.'")
	}
	if !strings.Contains(about, "Reportera de mercados.") {
		t.Errorf("about page missing the author bio")
	}
	if !strings.Contains(about, `href="/author/ada/"`) {
		t.Errorf("about page missing the /author/ada/ link")
	}
	// Author without metadata falls back to the stored label and still appears.
	if !strings.Contains(about, "Bob") || !strings.Contains(about, `href="/author/bob/"`) {
		t.Errorf("about page missing the no-metadata author 'Bob' / /author/bob/ link")
	}
	// It reuses base.tmpl (Spanish chrome present).
	if !strings.Contains(about, `<html lang="es">`) {
		t.Errorf("about page does not reuse the Spanish base layout")
	}
}
