package generate

import (
	"strings"
	"testing"
)

// The primary menu is a fixed, curated constant: the same entries in the same
// order on every page, so it never reshuffles as the reader moves between the
// landing, an article, a section and the About page (the previous bug, where the
// menu was derived from the current page's articles).
func TestNav_FixedCuratedMenu(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Reforma", Author: "lara", Section: "politics", Topics: []string{"ajuste"}, Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)

	navOf := func(name string) string {
		htmlStr := string(readArtifact(t, out, name))
		i := strings.Index(htmlStr, `<nav class="site-nav"`)
		if i < 0 {
			t.Fatalf("%s: no site-nav block", name)
		}
		return htmlStr[i : i+strings.Index(htmlStr[i:], "</nav>")]
	}

	latest := navOf("latest/index.html")
	if about := navOf("about/index.html"); latest != about {
		t.Fatalf("nav differs between the landing and the About page:\n  latest=%s\n  about=%s", latest, about)
	}

	want := []string{
		"Lo último", "Nosotros", "Política", "Internacionales",
		"Misterio y conspiración", "Tecnología", "Literatura",
	}
	last := -1
	for _, label := range want {
		at := strings.Index(latest, ">"+label+"</a>")
		if at < 0 {
			t.Fatalf("nav is missing %q\n  got: %s", label, latest)
		}
		if at < last {
			t.Fatalf("nav label %q is out of the fixed order\n  got: %s", label, latest)
		}
		last = at
	}
	// A page-derived topic must never leak back into the fixed bar.
	if strings.Contains(latest, `href="/topic/ajuste/"`) {
		t.Errorf("a page topic leaked into the fixed nav: %s", latest)
	}
}

// The fixed "Misterio y conspiración" entry must link to the topic page the
// generator actually emits. The slug is derived by Slugifying the full label
// ("misterio y conspiración" -> "misterio-y-conspiracion"); a hand-shortened
// "misterio" pointed every page at a /topic/misterio/ page that is never built,
// a site-wide dead link. This pins the link to a page that exists on disk.
func TestNav_MisterioLinkResolvesToRealTopicPage(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "El expediente", Author: "borge", Section: "world", Topics: []string{"misterio y conspiración"}, Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)

	latest := string(readArtifact(t, out, "latest/index.html"))
	if !strings.Contains(latest, `href="/topic/misterio-y-conspiracion/"`) {
		t.Errorf("nav does not link Misterio y conspiración to its real topic page /topic/misterio-y-conspiracion/\n  got: %s", latest)
	}
	if strings.Contains(latest, `href="/topic/misterio/"`) {
		t.Errorf("nav still points at the dead /topic/misterio/ page")
	}
	// The linked topic page must exist on disk (readArtifact fails if missing),
	// so the nav link is never dead.
	readArtifact(t, out, "topic/misterio-y-conspiracion/index.html")
}

// The masthead and footer wordmarks are links back to the portada (/latest/),
// so clicking "El Censurado Web" anywhere returns to the main page.
func TestNav_BrandLinksToPortada(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Reforma", Author: "lara", Section: "politics", Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)

	// check an inner article page too, not just the landing, since the chrome
	// is shared and the brand must link home from everywhere.
	for _, name := range []string{"latest/index.html", "about/index.html"} {
		htmlStr := string(readArtifact(t, out, name))
		if !strings.Contains(htmlStr, `<a class="site-name brand-wordmark" href="/latest/"`) {
			t.Errorf("%s: header wordmark is not a link to /latest/", name)
		}
		if !strings.Contains(htmlStr, `<a class="footer-wordmark brand-wordmark" href="/latest/"`) {
			t.Errorf("%s: footer wordmark is not a link to /latest/", name)
		}
		// the old non-clickable markup must be gone
		if strings.Contains(htmlStr, `<div class="site-name brand-wordmark"`) {
			t.Errorf("%s: header wordmark is still a non-clickable div", name)
		}
	}
}

// With no saved theme preference, a phone-width viewport boots in light mode.
func TestNav_MobileDefaultsLight(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "X", Author: "lara", Section: "politics", Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)
	htmlStr := string(readArtifact(t, out, "latest/index.html"))
	if !strings.Contains(htmlStr, "(max-width: 48rem)") || !strings.Contains(htmlStr, `dataset.theme="light"`) {
		t.Errorf("boot script missing the mobile light-mode default")
	}
}
