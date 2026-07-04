package generate

import (
	"context"
	"strings"
	"testing"

	"github.com/hec-ovi/censurado-web-backend/store"
)

// deleteAuthor type-asserts the test repo to the AuthorStore registry and soft-deletes
// one author row (setting its tombstone). The author's articles are untouched.
func deleteAuthor(t *testing.T, repo store.Repository, handle string) {
	t.Helper()
	as, ok := repo.(store.AuthorStore)
	if !ok {
		t.Fatalf("repo does not implement store.AuthorStore")
	}
	if err := as.DeleteAuthor(context.Background(), handle); err != nil {
		t.Fatalf("DeleteAuthor(%q): %v", handle, err)
	}
}

// TestDeletedAuthor_HiddenFromRosterKeepsArticlesAndByline pins the locked
// deleted-author contract: a soft-deleted author is dropped from the Nosotros roster,
// but their articles stay published and their byline keeps a working link (the
// /author/<slug>/ page is kept, so nothing dangles). A non-deleted author is a control
// that must still appear in the roster.
func TestDeletedAuthor_HiddenFromRosterKeepsArticlesAndByline(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	adaArt := seed(t, repo, seedSpec{
		Title:     "La Ley de IA",
		Author:    "ada",
		Section:   "politics",
		Published: date(2026, 6, 5),
		Metadata:  map[string]any{"author_name": "Ada Metadata"},
	})
	seed(t, repo, seedSpec{
		Title:     "Mundo Hoy",
		Author:    "bob",
		Section:   "world",
		Published: date(2026, 6, 6),
		Metadata:  map[string]any{"author_name": "Bob Metadata"},
	})
	upsertAuthor(t, repo, store.Author{Handle: "ada", Name: "Ada Registro", Bio: "Perfil de Ada."})
	upsertAuthor(t, repo, store.Author{Handle: "bob", Name: "Bob Vivo", Bio: "Perfil de Bob."})

	deleteAuthor(t, repo, "ada")
	genInto(t, repo, out, nil)

	// Roster (Nosotros) drops the tombstoned author entirely: neither her registry
	// name nor her metadata name, and no roster card link to her page.
	about := string(readArtifact(t, out, "about/index.html"))
	if strings.Contains(about, `href="/author/ada/"`) {
		t.Errorf("about roster still links to the deleted author's page")
	}
	if strings.Contains(about, "Ada Registro") || strings.Contains(about, "Ada Metadata") {
		t.Errorf("about roster still shows the deleted author's name")
	}
	// The survivor is still listed.
	if !strings.Contains(about, `href="/author/bob/"`) || !strings.Contains(about, "Bob Vivo") {
		t.Errorf("about roster dropped the surviving author bob")
	}

	// Articles stay published and the byline keeps a working link (page kept).
	if !exists(out, articlePath(adaArt)) {
		t.Errorf("deleted author's article was dropped: %s", articlePath(adaArt))
	}
	articleHTML := string(readArtifact(t, out, articlePath(adaArt)))
	if !strings.Contains(articleHTML, `href="/author/ada/"`) {
		t.Errorf("article byline lost its author link for a deleted author")
	}
	if !exists(out, "author/ada/index.html") {
		t.Errorf("deleted author's /author/ page was dropped, so every byline link now dangles")
	}
	// The listing card byline also keeps the link.
	if latest := string(readArtifact(t, out, "latest/index.html")); !strings.Contains(latest, `href="/author/ada/"`) {
		t.Errorf("listing card byline lost its author link for a deleted author")
	}
}

// TestDeletedAuthor_NoTombstoneUnchangedAndIdempotent guards the ListAuthors(false)->
// (true) switch: with no tombstones the roster is unchanged (the registered author is
// listed) and a re-run over the same store is idempotent, proving the switch adds no
// output churn until an author is actually deleted.
func TestDeletedAuthor_NoTombstoneUnchangedAndIdempotent(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	seed(t, repo, seedSpec{Title: "La Ley de IA", Author: "ada", Section: "politics", Published: date(2026, 6, 5)})
	upsertAuthor(t, repo, store.Author{Handle: "ada", Name: "Ada Registro", Bio: "Perfil de Ada."})

	genInto(t, repo, out, nil)

	about := string(readArtifact(t, out, "about/index.html"))
	if !strings.Contains(about, `href="/author/ada/"`) || !strings.Contains(about, "Ada Registro") {
		t.Errorf("a non-deleted registered author is missing from the roster")
	}

	res2 := genInto(t, repo, out, nil)
	if res2.Written != 0 || res2.Deleted != 0 || len(res2.Purge) != 0 {
		t.Errorf("second run not idempotent: written=%d deleted=%d purge=%d", res2.Written, res2.Deleted, len(res2.Purge))
	}
}

// TestDeletedAuthor_ArticlelessRegisteredAuthorNoPage proves the deleted branch skips
// the article-less-author inject: a registered author with no published articles who is
// then deleted gets no /author/ page and no roster card (the continue fires before the
// inject branch that would otherwise manufacture them).
func TestDeletedAuthor_ArticlelessRegisteredAuthorNoPage(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	// One real article so the site (and the roster) has content.
	seed(t, repo, seedSpec{Title: "Mundo Hoy", Author: "bob", Section: "world", Published: date(2026, 6, 6)})
	upsertAuthor(t, repo, store.Author{Handle: "bob", Name: "Bob Vivo"})
	// A registered author with NO articles, then deleted.
	upsertAuthor(t, repo, store.Author{Handle: "fantasma", Name: "Fantasma Registrado"})
	deleteAuthor(t, repo, "fantasma")

	genInto(t, repo, out, nil)

	if exists(out, "author/fantasma/index.html") {
		t.Errorf("deleted article-less author still got an /author/ page")
	}
	about := string(readArtifact(t, out, "about/index.html"))
	if strings.Contains(about, "Fantasma Registrado") {
		t.Errorf("deleted article-less author still appears in the roster")
	}
}
