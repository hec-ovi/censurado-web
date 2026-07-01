package generate

import (
	"context"
	"strings"
	"testing"

	"github.com/hec-ovi/censurado-web-backend/store"
)

// upsertAuthor type-asserts the test repo to the AuthorStore registry (the concrete
// *sqlite.Store implements it) and creates/updates one author row.
func upsertAuthor(t *testing.T, repo store.Repository, a store.Author) {
	t.Helper()
	as, ok := repo.(store.AuthorStore)
	if !ok {
		t.Fatalf("repo does not implement store.AuthorStore")
	}
	if _, err := as.UpsertAuthor(context.Background(), a); err != nil {
		t.Fatalf("UpsertAuthor(%q): %v", a.Handle, err)
	}
}

// upsertTopic type-asserts the test repo to the TopicStore registry and creates/updates
// one topic row.
func upsertTopic(t *testing.T, repo store.Repository, tp store.Topic) {
	t.Helper()
	ts, ok := repo.(store.TopicStore)
	if !ok {
		t.Fatalf("repo does not implement store.TopicStore")
	}
	if _, err := ts.UpsertTopic(context.Background(), tp); err != nil {
		t.Fatalf("UpsertTopic(%q): %v", tp.Slug, err)
	}
}

// TestRegistryOverlay_AuthorTablePrefersOverMetadata proves the operator-managed
// authors table wins over the per-article metadata for the public profile (name, bio,
// avatar) on both the single-author page and the Nosotros roster, while an author with
// NO table row keeps falling back to its metadata. This is the prefer-table-else-
// metadata contract.
func TestRegistryOverlay_AuthorTablePrefersOverMetadata(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	// ada has BOTH a metadata carrier and a registry row; the registry must win.
	seed(t, repo, seedSpec{
		Title:     "La Ley de IA",
		Author:    "ada",
		Section:   "politics",
		Published: date(2026, 6, 5),
		Metadata: map[string]any{
			"author_name":   "Ada L. (metadata)",
			"author_bio":    "Bio vieja de metadata.",
			"author_avatar": "/media/meta.png",
		},
	})
	// bob has only metadata, no registry row -> falls back to metadata.
	seed(t, repo, seedSpec{
		Title:     "Mundo Hoy",
		Author:    "bob",
		Section:   "world",
		Published: date(2026, 6, 6),
		Metadata: map[string]any{
			"author_name": "Bob (metadata)",
			"author_bio":  "Bio de Bob.",
		},
	})
	upsertAuthor(t, repo, store.Author{
		Handle: "ada",
		Name:   "Ada L. del Registro",
		Bio:    "Bio nueva del registro de autores.",
		Avatar: "/media/registro.png",
	})

	genInto(t, repo, out, nil)

	page := string(readArtifact(t, out, "author/ada/index.html"))
	// Heading and bio block come from the registry (these use the index maps the
	// overlay rewrites). The positive heading assertion proves the name is the
	// registry value, not the metadata one.
	if !strings.Contains(page, `<h1 class="author-profile-name">Ada L. del Registro</h1>`) {
		t.Errorf("author page heading is not the registry name")
	}
	if !strings.Contains(page, "Bio nueva del registro de autores.") {
		t.Errorf("author page missing the registry bio")
	}
	// The bio renders only in the (overlaid) bio block, so the metadata bio must be
	// gone. The per-article CARD byline still reflects the metadata stamped at publish
	// (a separate, immutable concern), so the metadata NAME may still appear on cards.
	if strings.Contains(page, "Bio vieja de metadata.") {
		t.Errorf("metadata author bio leaked despite a registry row")
	}

	about := string(readArtifact(t, out, "about/index.html"))
	if !strings.Contains(about, "Ada L. del Registro") || !strings.Contains(about, "Bio nueva del registro de autores.") {
		t.Errorf("about roster does not show the registry name/bio for ada")
	}
	if strings.Contains(about, "Ada L. (metadata)") {
		t.Errorf("about roster leaked the metadata name for a registry-backed author")
	}
	// bob has no registry row, so its metadata still renders.
	if !strings.Contains(about, "Bob (metadata)") || !strings.Contains(about, "Bio de Bob.") {
		t.Errorf("about roster dropped the metadata fallback for a registry-less author")
	}
}

// TestAuthorProfileTopics_CuratedPrefersAndCapsFallback proves the author-profile topic
// chips: a curated profile_topics list (carried in the author registry's Metadata blob)
// REPLACES the auto-computed union and is filtered to topics that actually have a page,
// while an author with NO curated list falls back to the union CAPPED at maxAuthorTopics.
func TestAuthorProfileTopics_CuratedPrefersAndCapsFallback(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	// ada writes politica + sociedad (so the UNION would be those two). Her curated list
	// is ["economia", "inexistente"]: economia is a live topic (lin tagged it below) and
	// must show; inexistente has no article anywhere and must be dropped.
	seed(t, repo, seedSpec{Title: "Ley ada", Author: "ada", Section: "politics",
		Topics: []string{"politica", "sociedad"}, Published: date(2026, 6, 5)})
	seed(t, repo, seedSpec{Title: "Economia lin", Author: "lin", Section: "world",
		Topics: []string{"economia"}, Published: date(2026, 6, 6)})
	upsertAuthor(t, repo, store.Author{
		Handle:   "ada",
		Name:     "Ada L.",
		Metadata: map[string]any{"profile_topics": []string{"economia", "inexistente"}},
	})

	// cap writes ten distinct single-tag topics and has NO curated list, so the union
	// fallback applies and must be capped at maxAuthorTopics (8). Counts all tie at 1, so
	// the order is slug-ascending: tema0..tema7 survive, tema8/tema9 are cut.
	for i := 0; i < 10; i++ {
		seed(t, repo, seedSpec{
			Title:     "Cap " + string(rune('a'+i)),
			Author:    "cap",
			Section:   "tech",
			Topics:    []string{"tema" + string(rune('0'+i))},
			Published: date(2026, 6, 7),
		})
	}

	genInto(t, repo, out, nil)

	adaTopics := topicsNav(t, string(readArtifact(t, out, "author/ada/index.html")))
	if !strings.Contains(adaTopics, `href="/topic/economia/"`) {
		t.Errorf("curated topic economia missing from ada's profile topics")
	}
	for _, dead := range []string{"/topic/politica/", "/topic/sociedad/", "/topic/inexistente/"} {
		if strings.Contains(adaTopics, dead) {
			t.Errorf("ada profile topics leaked %q (curated list must suppress the union and drop dead topics)", dead)
		}
	}

	capTopics := topicsNav(t, string(readArtifact(t, out, "author/cap/index.html")))
	if !strings.Contains(capTopics, `href="/topic/tema7/"`) {
		t.Errorf("capped fallback dropped tema7 (should keep the first 8)")
	}
	for _, cut := range []string{"/topic/tema8/", "/topic/tema9/"} {
		if strings.Contains(capTopics, cut) {
			t.Errorf("capped fallback kept %q past maxAuthorTopics=%d", cut, maxAuthorTopics)
		}
	}
}

// topicsNav returns just the <nav class="author-profile-topics"> ... </nav> slice of an
// author page, so a topic assertion cannot accidentally match a link elsewhere on the
// page (a card byline, a related block). Fails the test if the nav is absent.
func topicsNav(t *testing.T, page string) string {
	t.Helper()
	const open = `<nav class="author-profile-topics"`
	i := strings.Index(page, open)
	if i < 0 {
		t.Fatalf("author page has no author-profile-topics nav")
	}
	rest := page[i:]
	j := strings.Index(rest, "</nav>")
	if j < 0 {
		t.Fatalf("author-profile-topics nav is not closed")
	}
	return rest[:j]
}

// TestRegistryOverlay_TopicLabelFromTable proves the managed topics table supplies the
// topic page heading, overriding the slug-derived label.
func TestRegistryOverlay_TopicLabelFromTable(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title:     "Modelos locales",
		Author:    "lin",
		Section:   "tech",
		Topics:    []string{"ia"},
		Published: date(2026, 6, 5),
	})
	upsertTopic(t, repo, store.Topic{Slug: "ia", Label: "Inteligencia Artificial"})

	genInto(t, repo, out, nil)

	page := string(readArtifact(t, out, "topic/ia/index.html"))
	if !strings.Contains(page, `<h1 class="listing-heading">Inteligencia Artificial</h1>`) {
		t.Errorf("topic page heading is not the registry label 'Inteligencia Artificial'")
	}
}

// TestRegistryOverlay_IgnoresEntriesWithNoArticles proves the overlay only touches
// authors/topics that already have published articles: a registry row for an author or
// topic with no articles must not manufacture a roster card, an author page, or a topic
// page.
func TestRegistryOverlay_IgnoresEntriesWithNoArticles(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title:     "Pieza real",
		Author:    "ada",
		Section:   "politics",
		Topics:    []string{"ia"},
		Published: date(2026, 6, 5),
	})
	// Registry rows for an author and a topic that have NO articles.
	upsertAuthor(t, repo, store.Author{Handle: "fantasma", Name: "Autor Fantasma", Bio: "No escribió nada."})
	upsertTopic(t, repo, store.Topic{Slug: "vacio", Label: "Tema Vacío"})

	genInto(t, repo, out, nil)

	if exists(out, "author/fantasma/index.html") {
		t.Errorf("overlay manufactured an author page for a registry author with no articles")
	}
	if exists(out, "topic/vacio/index.html") {
		t.Errorf("overlay manufactured a topic page for a registry topic with no articles")
	}
	about := string(readArtifact(t, out, "about/index.html"))
	if strings.Contains(about, "Autor Fantasma") {
		t.Errorf("about roster lists a registry author with no articles")
	}
}
