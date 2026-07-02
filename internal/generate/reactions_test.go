package generate

import (
	"strings"
	"testing"
)

// The article permalink ships the reactions bar server-rendered but hidden, keyed
// by the BACKEND slug (the same value the reactions API and the publish response
// use, not the permalink's hashed path segment), so initReactions votes for
// exactly this article, and a page without JS or without the API never shows an
// inert widget.
func TestArticlePageCarriesHiddenReactionsBar(t *testing.T) {
	repo := newStore(t)
	a := seed(t, repo, seedSpec{Title: "Reacciones De Prueba", Author: "ada", Section: "tech"})
	out := t.TempDir()
	genInto(t, repo, out, nil)

	page := string(readArtifact(t, out, strings.TrimPrefix(articleURL(a), "/")+"index.html"))
	for _, want := range []string{
		`<div class="reactions" data-reactions data-slug="` + a.Slug + `" hidden>`,
		`<button type="button" class="reaction-btn" data-vote="up" aria-pressed="false" aria-label="Me gusta">`,
		`<button type="button" class="reaction-btn" data-vote="down" aria-pressed="false" aria-label="No me gusta">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("article page missing %q", want)
		}
	}
	if strings.Index(page, "data-reactions") > strings.Index(page, `class="article-sign"`) {
		t.Errorf("reactions bar must render before the article signature")
	}
	if !strings.Contains(a.Slug, "reacciones-de-prueba") {
		t.Fatalf("unexpected backend slug %q", a.Slug)
	}
}
