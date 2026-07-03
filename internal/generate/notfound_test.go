package generate

import (
	"strings"
	"testing"
)

// TestNotFoundPage_StyledAtRoot proves the generator writes a styled 404.html at
// the output ROOT that reuses the site chrome (header, nav, footer) and carries the
// Spanish "oops" message plus a link back to the portada. Both serving layers pick
// this file up by convention: local nginx via `error_page 404 /404.html` and
// Cloudflare Pages, which serves a root 404.html for any not-found route.
func TestNotFoundPage_StyledAtRoot(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title:     "Crypto Crash",
		Author:    "ada",
		Section:   "crypto",
		Published: date(2026, 6, 5),
	})
	genInto(t, repo, out, nil)

	// It lands at the site root, not under a scope directory.
	if !exists(out, "404.html") {
		t.Fatalf("missing 404.html at the output root")
	}
	page := string(readArtifact(t, out, "404.html"))

	// Reuses base.tmpl (Spanish chrome) exactly like every other page.
	if !strings.Contains(page, `<html lang="es">`) {
		t.Errorf("404 page does not reuse the Spanish base layout")
	}
	// Site header/nav markup from components/chrome.tmpl.
	if !strings.Contains(page, `data-site-header`) {
		t.Errorf("404 page missing the site header markup")
	}
	if !strings.Contains(page, `class="site-name brand-wordmark"`) {
		t.Errorf("404 page missing the masthead wordmark")
	}
	// Footer with the AI-disclosure the other pages carry.
	if !strings.Contains(page, `data-site-footer`) || !strings.Contains(page, "Aviso editorial") {
		t.Errorf("404 page missing the footer / Aviso editorial disclosure")
	}
	// The friendly Spanish "oops / no encontramos esta página" message.
	if !strings.Contains(page, "¡Ups! No encontramos esta página") {
		t.Errorf("404 page missing the oops heading")
	}
	if !strings.Contains(page, `<title>¡Ups! No encontramos esta página | Censurado</title>`) {
		t.Errorf("404 document title is not the oops message + site name")
	}
	// A clear link back to the home/latest page.
	if !strings.Contains(page, `href="/latest/"`) {
		t.Errorf("404 page missing a link back to /latest/")
	}
	if !strings.Contains(page, "Volver a la portada") {
		t.Errorf("404 page missing the home-link label")
	}
	// Reuses the site's CSS via the shared stylesheet (no divergent inline layout).
	if !strings.Contains(page, `<link rel="stylesheet" href="/assets/style.css">`) {
		t.Errorf("404 page does not load the shared site stylesheet")
	}
}

// TestNotFoundPage_EmptyCorpus proves the 404 page is emitted even with no
// articles, so a brand-new site still has a styled not-found page.
func TestNotFoundPage_EmptyCorpus(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	genInto(t, repo, out, nil)

	if !exists(out, "404.html") {
		t.Fatalf("404.html not emitted for an empty corpus")
	}
	page := string(readArtifact(t, out, "404.html"))
	if !strings.Contains(page, "¡Ups! No encontramos esta página") {
		t.Errorf("empty-corpus 404 page missing the oops heading")
	}
	if !strings.Contains(page, `href="/latest/"`) {
		t.Errorf("empty-corpus 404 page missing the home link")
	}
}

// TestNotFoundPage_ByteStable proves the 404 page does not depend on the live
// corpus: adding an article does not rewrite 404.html, so it never enters a purge
// diff (the append-only immutability the sealed pages also rely on).
func TestNotFoundPage_ByteStable(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "First", Author: "ada", Section: "tech", Published: date(2026, 6, 1)})
	genInto(t, repo, out, nil)
	before := string(readArtifact(t, out, "404.html"))

	seed(t, repo, seedSpec{Title: "Second", Author: "bob", Section: "world", Published: date(2026, 6, 2)})
	res := genInto(t, repo, out, nil)
	after := string(readArtifact(t, out, "404.html"))

	if before != after {
		t.Errorf("404.html changed after adding an article; it must be byte-stable")
	}
	if strSet(res.Purge)["/404.html"] {
		t.Errorf("404.html appeared in the purge set: %v", res.Purge)
	}
}
