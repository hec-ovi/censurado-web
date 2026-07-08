package generate

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/hec-ovi/censurado-web-backend/store"
)

// The catalog is internally consistent: keys are unique, every entry has a
// non-empty English base and Spanish value, and defaultText (the render fallback)
// carries every key. A key that resolved to "" or was dropped would render blank.
func TestCatalog_SelfConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range frontendSeed {
		if seen[e.Key] {
			t.Errorf("duplicate catalog key %q", e.Key)
		}
		seen[e.Key] = true
		if e.En == "" || e.Es == "" {
			t.Errorf("catalog key %q has an empty value (en=%q es=%q)", e.Key, e.En, e.Es)
		}
		if defaultText[e.Key] != e.Es {
			t.Errorf("defaultText[%q] = %q, want the Es value %q", e.Key, defaultText[e.Key], e.Es)
		}
	}
	if len(defaultText) != len(seen) {
		t.Errorf("defaultText has %d keys, catalog has %d", len(defaultText), len(seen))
	}
}

// Every {{t "key"}} reference in the embedded templates resolves to a real catalog
// key. A typo'd key would silently render the raw key string (env.T's last-resort
// fallback); this fails the build instead. Keys are lowercase dotted identifiers.
func TestTemplates_ReferenceOnlyKnownKeys(t *testing.T) {
	ref := regexp.MustCompile(`\bt "([a-z0-9_.]+)"`)
	err := fs.WalkDir(templateFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tmpl") {
			return err
		}
		b, rerr := templateFS.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range ref.FindAllStringSubmatch(string(b), -1) {
			if _, ok := defaultText[m[1]]; !ok {
				t.Errorf("%s references unknown catalog key %q", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// seedFrontendText upserts key->value rows for one language into frontend_text.
func seedFrontendText(t *testing.T, repo store.Repository, lang string, kv map[string]string) {
	t.Helper()
	ts, ok := repo.(store.TextStore)
	if !ok {
		t.Fatal("store does not implement store.TextStore")
	}
	for k, v := range kv {
		if _, err := ts.UpsertText(context.Background(), store.ScopeFrontend, store.TextEntry{Key: k, Lang: lang, Value: v}); err != nil {
			t.Fatalf("UpsertText(%s/%s): %v", lang, k, err)
		}
	}
}

// seedFullCatalog upserts the whole compiled catalog (both en and es) into
// frontend_text, the same data the seedtext command writes.
func seedFullCatalog(t *testing.T, repo store.Repository) {
	t.Helper()
	ts := repo.(store.TextStore)
	for _, e := range frontendSeed {
		for _, lv := range []struct{ lang, val string }{{"en", e.En}, {"es", e.Es}} {
			if _, err := ts.UpsertText(context.Background(), store.ScopeFrontend, store.TextEntry{Key: e.Key, Lang: lv.lang, Value: lv.val}); err != nil {
				t.Fatalf("seed %s/%s: %v", lv.lang, e.Key, err)
			}
		}
	}
}

// A DB row overlays the compiled default: an unseeded build shows the compiled
// Spanish footer heading; after upserting an es row for that key the build shows
// the DB value instead, and the compiled default no longer appears.
func TestFrontendText_DBOverridesCompiledDefault(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Una nota", Author: "ada", Section: "tech", Published: date(2026, 6, 3)})

	genInto(t, repo, out, nil)
	base := string(readArtifact(t, out, "latest/index.html"))
	if !strings.Contains(base, ">Aviso editorial<") {
		t.Fatalf("baseline footer heading (compiled default) missing")
	}

	seedFrontendText(t, repo, "es", map[string]string{"footer.disclosure_heading": "Aviso legal"})
	genInto(t, repo, out, nil)
	got := string(readArtifact(t, out, "latest/index.html"))
	if !strings.Contains(got, ">Aviso legal<") {
		t.Errorf("DB override not applied to the footer heading")
	}
	if strings.Contains(got, ">Aviso editorial<") {
		t.Errorf("compiled default leaked past the DB override")
	}
}

// The seeded es catalog must reproduce the compiled-default (unseeded) bytes
// exactly: every catalog Es value equals the string the templates/Go used to
// hardcode, so seeding es changes nothing a reader sees. This guards catalog.go
// against a drifted Spanish value.
func TestFrontendText_SeededEsMatchesCompiledDefault(t *testing.T) {
	seedArticles := func(repo store.Repository) {
		seed(t, repo, seedSpec{Title: "Primera", Author: "ada", Section: "tech", Topics: []string{"go"}, Published: date(2026, 6, 3),
			Body: "Cuerpo.\n\n{{tweet:9}}\n\nFin.",
			Metadata: map[string]any{
				"author_name": "Ada L.",
				"tweets": []any{map[string]any{
					"id": "9", "name": "X", "handle": "x", "text": "hola",
					"url": "https://x.com/x/status/9", "views": float64(1200),
					"replies": float64(3), "erased": true,
				}},
			},
		})
		seed(t, repo, seedSpec{Title: "Segunda", Author: "bob", Section: "world", Published: date(2026, 6, 4)})
	}

	repoA := newStore(t)
	outA := t.TempDir()
	seedArticles(repoA)
	genInto(t, repoA, outA, nil) // unseeded: compiled defaults

	repoB := newStore(t)
	outB := t.TempDir()
	seedArticles(repoB)
	seedFullCatalog(t, repoB) // seeded es (+ en) with the same values
	genInto(t, repoB, outB, nil)

	for _, rel := range []string{
		"latest/index.html", "about/index.html", "404.html", "section/world/index.html",
		"feed.xml", "atom.xml", "feed.json",
	} {
		a := readArtifact(t, outA, rel)
		b := readArtifact(t, outB, rel)
		if string(a) != string(b) {
			t.Errorf("%s differs between unseeded and seeded-es builds (a catalog Es value drifted from the hardcoded string)", rel)
		}
	}
}

// Rendering with -lang en emits the English base rows and <html lang="en">.
func TestFrontendText_EnglishRender(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "A note", Author: "ada", Section: "world", Published: date(2026, 6, 3)})
	seedFullCatalog(t, repo)
	genInto(t, repo, out, func(o *Options) { o.Lang = "en" })
	page := string(readArtifact(t, out, "latest/index.html"))

	if !strings.Contains(page, `<html lang="en">`) {
		t.Errorf("html lang attribute is not en")
	}
	if !strings.Contains(page, ">Editorial notice<") {
		t.Errorf("English footer heading missing")
	}
	// The world nav + heading read the English section label, not the Spanish "Mundo".
	if !strings.Contains(page, ">World</a>") {
		t.Errorf("English world nav label missing")
	}
	if strings.Contains(page, "Tecnología") || strings.Contains(page, ">Mundo<") {
		t.Errorf("Spanish label leaked into the English render")
	}
}

// The RSS/Atom/JSON feeds carry a language-consistent channel description: it now
// reads the catalog (Spanish by default), not the old hardcoded English string.
func TestFrontendText_FeedDescriptionFollowsLanguage(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Nota", Author: "ada", Section: "tech", Published: date(2026, 6, 3)})
	genInto(t, repo, out, func(o *Options) { o.SiteName = "Censurado" })
	feed := string(readArtifact(t, out, "feed.xml"))
	if !strings.Contains(feed, "Los últimos artículos publicados en Censurado.") {
		t.Errorf("feed channel description is not the Spanish catalog value:\n%s", feed)
	}
	if strings.Contains(feed, "Latest articles from") {
		t.Errorf("old hardcoded English feed description still present")
	}
}
