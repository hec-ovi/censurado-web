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
// avatar) on both the single-author page and the Autores roster, while an author with
// NO table row keeps falling back to its metadata. This is the prefer-table-else-
// metadata contract.
func TestRegistryOverlay_AuthorTablePrefersOverMetadata(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	// lara has BOTH a metadata carrier and a registry row; the registry must win.
	seed(t, repo, seedSpec{
		Title:     "La Ley de IA",
		Author:    "lara-arianna",
		Section:   "politics",
		Published: date(2026, 6, 5),
		Metadata: map[string]any{
			"author_name":   "Lara (metadata)",
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
		Handle: "lara-arianna",
		Name:   "Lara Arianna del Registro",
		Bio:    "Bio nueva del registro de autores.",
		Avatar: "/media/registro.png",
	})

	genInto(t, repo, out, nil)

	page := string(readArtifact(t, out, "author/lara-arianna/index.html"))
	// Heading and bio block come from the registry (these use the index maps the
	// overlay rewrites). The positive heading assertion proves the name is the
	// registry value, not the metadata one.
	if !strings.Contains(page, `<h1 class="listing-heading">Lara Arianna del Registro</h1>`) {
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
	if !strings.Contains(about, "Lara Arianna del Registro") || !strings.Contains(about, "Bio nueva del registro de autores.") {
		t.Errorf("about roster does not show the registry name/bio for lara")
	}
	if strings.Contains(about, "Lara (metadata)") {
		t.Errorf("about roster leaked the metadata name for a registry-backed author")
	}
	// bob has no registry row, so its metadata still renders.
	if !strings.Contains(about, "Bob (metadata)") || !strings.Contains(about, "Bio de Bob.") {
		t.Errorf("about roster dropped the metadata fallback for a registry-less author")
	}
}

// TestRegistryOverlay_TopicLabelFromTable proves the managed topics table supplies the
// topic page heading, overriding the slug-derived label.
func TestRegistryOverlay_TopicLabelFromTable(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title:     "Modelos locales",
		Author:    "vector-omni",
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
		Author:    "lara-arianna",
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
