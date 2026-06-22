package generate

import (
	"encoding/xml"
	"fmt"
	"sort"
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
)

// listingsSitemapPath is the child sitemap carrying the Tier-A listing URLs.
const listingsSitemapPath = "sitemaps/listings.xml"

// articleSitemapPath is the per-publication-month article sitemap path.
func articleSitemapPath(y, m int) string {
	return fmt.Sprintf("sitemaps/articles-%04d-%02d.xml", y, m)
}

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type sitemapIndex struct {
	XMLName  xml.Name            `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 sitemapindex"`
	Sitemaps []sitemapIndexEntry `xml:"sitemap"`
}

type sitemapIndexEntry struct {
	Loc string `xml:"loc"`
}

// BuildSitemap emits sitemap.xml listing only Tier-A HTML URLs (absolute via
// BaseURL) plus the site root. No /a/ permalinks, no shard/.json/feed URLs.
// lastmod per URL is the newest truncated PublishedAt in that scope. A rem==0
// landing's canonical points at its sealed page, so it is naturally deduped.
func BuildSitemap(siteBase string, plan *Plan) ([]byte, error) {
	idx := plan.Index

	newest := map[Scope]time.Time{}
	var global time.Time
	for _, sc := range idx.Scopes() {
		var mx time.Time
		for _, i := range idx.scopeIndices(sc) {
			if t := idx.All[i].PublishedAt; t.After(mx) {
				mx = t
			}
		}
		newest[sc] = mx
		if mx.After(global) {
			global = mx
		}
	}

	set := sitemapURLSet{}
	root := sitemapURL{Loc: absolute(siteBase, "/")}
	if !global.IsZero() {
		root.LastMod = global.UTC().Format(time.RFC3339)
	}
	set.URLs = append(set.URLs, root)

	for _, pg := range plan.Pages {
		// Include only pages whose canonical is their own URL; a rem==0 landing's
		// canonical points at the sealed page, so it is skipped here.
		if pg.Canonical != pg.Scope.PageURL(pg.Number) {
			continue
		}
		u := sitemapURL{Loc: absolute(siteBase, pg.Canonical)}
		if t := newest[pg.Scope]; !t.IsZero() {
			u.LastMod = t.UTC().Format(time.RFC3339)
		}
		set.URLs = append(set.URLs, u)
	}

	sort.Slice(set.URLs, func(i, j int) bool { return set.URLs[i].Loc < set.URLs[j].Loc })
	return marshalXML(set)
}

// buildArticleSitemap emits one month's article permalinks as a <urlset>, sorted
// by absolute Loc for deterministic bytes. lastmod is each article's own
// PublishedAt, so an unchanged month reproduces byte-identically.
func buildArticleSitemap(siteBase string, arts []domain.Article) ([]byte, error) {
	set := sitemapURLSet{}
	for _, a := range arts {
		set.URLs = append(set.URLs, sitemapURL{
			Loc:     absolute(siteBase, articleURL(a)),
			LastMod: a.PublishedAt.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(set.URLs, func(i, j int) bool { return set.URLs[i].Loc < set.URLs[j].Loc })
	return marshalXML(set)
}

// BuildSitemapIndex emits the <sitemapindex> referencing every child sitemap by
// absolute URL, in the given order.
func BuildSitemapIndex(siteBase string, childURLs []string) ([]byte, error) {
	idx := sitemapIndex{}
	for _, u := range childURLs {
		idx.Sitemaps = append(idx.Sitemaps, sitemapIndexEntry{Loc: absolute(siteBase, u)})
	}
	return marshalXML(idx)
}

// buildRobots emits a robots.txt that allows all crawling and points crawlers at
// the sitemap index. The bytes depend only on BaseURL, so they are stable.
func buildRobots(siteBase string) []byte {
	return []byte("User-agent: *\nAllow: /\n\nSitemap: " + absolute(siteBase, "/sitemap.xml") + "\n")
}
