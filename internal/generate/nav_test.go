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
