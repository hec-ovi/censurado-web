package generate

import (
	"encoding/xml"
	"sort"
	"time"
)

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
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
