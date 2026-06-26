package generate

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/hec-ovi/censurado-web-backend/domain"
)

// D3: robots.txt + sitemap index referencing the listings sitemap and per-month
// article sitemaps; a month sitemap lists exactly that month's permalinks.
func TestRobotsAndSitemapIndex(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	mayA := seed(t, repo, seedSpec{Title: "SI May", Author: "ada", Section: "tech", Published: date(2026, 5, 4)})
	junA := seed(t, repo, seedSpec{Title: "SI Jun A", Author: "ada", Section: "tech", Published: date(2026, 6, 4)})
	junB := seed(t, repo, seedSpec{Title: "SI Jun B", Author: "lin", Section: "sci", Published: date(2026, 6, 6)})
	genInto(t, repo, out, nil)

	robots := string(readArtifact(t, out, "robots.txt"))
	if !strings.Contains(robots, "Sitemap: https://news.example/sitemap.xml") {
		t.Errorf("robots missing absolute Sitemap line:\n%s", robots)
	}
	if !strings.Contains(robots, "Allow: /") {
		t.Errorf("robots missing Allow line:\n%s", robots)
	}

	sm := readArtifact(t, out, "sitemap.xml")
	if !strings.Contains(string(sm), "<sitemapindex") {
		t.Fatalf("sitemap.xml is not a sitemapindex:\n%s", sm)
	}
	var idx struct {
		Sitemaps []struct {
			Loc string `xml:"loc"`
		} `xml:"sitemap"`
	}
	if err := xml.Unmarshal(sm, &idx); err != nil {
		t.Fatalf("sitemap index not well-formed: %v", err)
	}
	children := map[string]bool{}
	for _, s := range idx.Sitemaps {
		children[s.Loc] = true
	}
	for _, want := range []string{
		"https://news.example/sitemaps/listings.xml",
		"https://news.example/sitemaps/articles-2026-05.xml",
		"https://news.example/sitemaps/articles-2026-06.xml",
	} {
		if !children[want] {
			t.Errorf("sitemap index missing child %q (got %v)", want, idx.Sitemaps)
		}
	}

	var urlset struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(readArtifact(t, out, "sitemaps/articles-2026-06.xml"), &urlset); err != nil {
		t.Fatalf("June article sitemap not well-formed: %v", err)
	}
	got := map[string]bool{}
	for _, u := range urlset.URLs {
		got[u.Loc] = true
	}
	for _, a := range []domain.Article{junA, junB} {
		if !got[absolute("https://news.example", articleURL(a))] {
			t.Errorf("June sitemap missing permalink %q", articleURL(a))
		}
	}
	if got[absolute("https://news.example", articleURL(mayA))] {
		t.Errorf("June sitemap leaked a May permalink")
	}

	res := genInto(t, repo, out, nil)
	if res.Written != 0 || len(res.Purge) != 0 {
		t.Errorf("sitemap re-run not idempotent: written=%d purge=%v", res.Written, res.Purge)
	}
}
