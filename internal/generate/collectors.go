package generate

import (
	"context"
	"time"

	"github.com/hec-ovi/censurado-web/internal/content"
)

// collector produces a slice of the artifact set.
type collector interface {
	name() string
	collect(ctx context.Context, env *buildEnv, out *ArtifactSet) error
}

// buildEnv carries the shared per-run state collectors read.
type buildEnv struct {
	siteBase   string
	now        time.Time
	pageSize   int
	capE       int
	capB       int
	siteName   string
	plan       *Plan
	bodyHTML   map[string]string     // ContentHash -> rendered body, rendered once
	shardCache map[Scope]scopeShards // scope -> shard build, computed once per scope
}

// scopeShards is the per-scope shard build (months newest-first, parts keyed by
// "yyyy-mm", and the total entry count). The HTML page, the standalone manifest,
// and the shard files all read it, so a scope is scanned and packed exactly once.
type scopeShards struct {
	months       []ym
	partsByMonth map[string][]ShardPart
	total        int
}

func buildEnvFrom(opts Options) *buildEnv {
	return &buildEnv{
		siteBase:   opts.BaseURL,
		now:        opts.Now,
		pageSize:   opts.PageSize,
		capE:       opts.ShardMaxEntries,
		capB:       opts.ShardMaxGzipBytes,
		siteName:   opts.SiteName,
		shardCache: map[Scope]scopeShards{},
	}
}

// shardsFor returns the scope's shard build, computing and caching it on first
// use so the three collectors that need it (pages, manifest, shards) never re-pack
// the same scope.
func (env *buildEnv) shardsFor(sc Scope) (scopeShards, error) {
	if ss, ok := env.shardCache[sc]; ok {
		return ss, nil
	}
	months, partsByMonth, total, err := buildScopeShards(env, sc)
	if err != nil {
		return scopeShards{}, err
	}
	ss := scopeShards{months: months, partsByMonth: partsByMonth, total: total}
	if env.shardCache == nil {
		env.shardCache = map[Scope]scopeShards{}
	}
	env.shardCache[sc] = ss
	return ss, nil
}

// manifestFor builds a scope's PageManifest from its cached shard build.
func (env *buildEnv) manifestFor(sc Scope) (PageManifest, error) {
	ss, err := env.shardsFor(sc)
	if err != nil {
		return PageManifest{}, err
	}
	return BuildManifest(sc, env.capE, env.capB, ss.months, ss.partsByMonth, ss.total), nil
}

// collectors returns the collectors in fixed registration order; the order is a
// deterministic tie-break for the duplicate-path error.
func collectors() []collector {
	return []collector{
		tierAPageCollector{},
		articleCollector{},
		shardCollector{},
		manifestCollector{},
		assetCollector{},
		metaCollector{},
	}
}

// renderBodiesOnce renders every article body exactly once, keyed by content
// hash (identical hashes share a body).
func renderBodiesOnce(p *Plan) (map[string]string, error) {
	out := map[string]string{}
	for _, a := range p.Index.All {
		if _, ok := out[a.ContentHash]; ok {
			continue
		}
		html, err := content.RenderMarkdown(a.Body)
		if err != nil {
			return nil, err
		}
		out[a.ContentHash] = html
	}
	return out, nil
}

// scopeMonthBuckets buckets a scope's insertion-ordered indices by published
// month, returning the months newest-first and the per-month indices (each in
// insertion order).
func (idx *Index) scopeMonthBuckets(s Scope) ([]ym, map[ym][]int) {
	byMonth := map[ym][]int{}
	for _, i := range idx.scopeIndices(s) {
		a := idx.All[i]
		m := ym{a.PublishedAt.Year(), int(a.PublishedAt.Month())}
		byMonth[m] = append(byMonth[m], i)
	}
	keys := make([]ym, 0, len(byMonth))
	for k := range byMonth {
		keys = append(keys, k)
	}
	return monthsNewestFirst(keys), byMonth
}

// buildScopeShards emits the shard parts for every month of a scope. It returns
// the months newest-first, the parts keyed by "yyyy-mm", and the total entry
// count across all months.
func buildScopeShards(env *buildEnv, s Scope) (months []ym, partsByMonth map[string][]ShardPart, total int, err error) {
	monthsNewest, byMonth := env.plan.Index.scopeMonthBuckets(s)
	partsByMonth = map[string][]ShardPart{}
	scopePath := s.ShardKey()
	for _, m := range monthsNewest {
		idxs := byMonth[m]
		entries := make([]ShardEntry, 0, len(idxs))
		for _, i := range idxs {
			entries = append(entries, ShardEntryOf(env.plan.Index.All[i]))
		}
		parts, perr := EmitShardsForScopeMonth(scopePath, m.Year, m.Month, entries, env.capE, env.capB)
		if perr != nil {
			return nil, nil, 0, perr
		}
		partsByMonth[monthKey(m.Year, m.Month)] = parts
		total += len(idxs)
	}
	return monthsNewest, partsByMonth, total, nil
}

// tierAPageCollector emits Tier-A listing HTML, one per Page, with the scope's
// embedded manifest.
type tierAPageCollector struct{}

func (tierAPageCollector) name() string { return "tier-a-pages" }

func (tierAPageCollector) collect(ctx context.Context, env *buildEnv, out *ArtifactSet) error {
	pagesByScope := map[Scope][]Page{}
	for _, pg := range env.plan.Pages {
		pagesByScope[pg.Scope] = append(pagesByScope[pg.Scope], pg)
	}
	for _, sc := range env.plan.Index.Scopes() {
		pm, err := env.manifestFor(sc)
		if err != nil {
			return err
		}
		manifest, err := ManifestScript(pm)
		if err != nil {
			return err
		}
		for _, pg := range pagesByScope[sc] {
			b, err := renderListing(env, pg, manifest)
			if err != nil {
				return err
			}
			scopePath := sc.scopePath()
			if err := out.Add(Artifact{
				Path:  pagePath(scopePath, pg.Number),
				URL:   pageURL(scopePath, pg.Number),
				Kind:  KindPage,
				Bytes: b,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// articleCollector emits each article's permalink HTML.
type articleCollector struct{}

func (articleCollector) name() string { return "articles" }

func (articleCollector) collect(ctx context.Context, env *buildEnv, out *ArtifactSet) error {
	for _, a := range env.plan.Index.All {
		b, err := renderArticle(env, a)
		if err != nil {
			return err
		}
		if err := out.Add(Artifact{
			Path:  articlePath(a),
			URL:   articleURL(a),
			Kind:  KindPage,
			Bytes: b,
		}); err != nil {
			return err
		}
	}
	return nil
}

// shardCollector emits month shards per scope. It processes one (scope, month)
// bucket at a time; stable months reproduce byte-identically.
type shardCollector struct{}

func (shardCollector) name() string { return "shards" }

func (shardCollector) collect(ctx context.Context, env *buildEnv, out *ArtifactSet) error {
	for _, sc := range env.plan.Index.Scopes() {
		ss, err := env.shardsFor(sc)
		if err != nil {
			return err
		}
		for _, m := range ss.months {
			for _, part := range ss.partsByMonth[monthKey(m.Year, m.Month)] {
				if err := out.Add(Artifact{
					Path:  part.Path,
					URL:   "/" + part.Path,
					Kind:  KindShard,
					Bytes: part.Bytes,
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// manifestCollector emits one standalone PageManifest file per scope at
// manifest/<ShardKey>/index.json. Its bytes are byte-identical to the inline
// #cnz-manifest payload on the scope's landing. It is KindShard (purged when the
// scope's tail grows), and the referencing <link rel="manifest"> href on the page
// depends only on the scope, so sealed-page HTML stays byte-stable.
type manifestCollector struct{}

func (manifestCollector) name() string { return "manifests" }

func (manifestCollector) collect(ctx context.Context, env *buildEnv, out *ArtifactSet) error {
	for _, sc := range env.plan.Index.Scopes() {
		pm, err := env.manifestFor(sc)
		if err != nil {
			return err
		}
		b, err := MarshalManifest(pm)
		if err != nil {
			return err
		}
		key := sc.ShardKey()
		if err := out.Add(Artifact{
			Path:  manifestPath(key),
			URL:   manifestURL(key),
			Kind:  KindShard,
			Bytes: b,
		}); err != nil {
			return err
		}
	}
	return nil
}

// assetCollector emits the CSS and JS at stable, non-content-hashed URLs. The
// constant hrefs keep sealed-page HTML byte-immutable even when the asset bytes
// later change: only the asset files are rewritten (and purged, KindMeta), never
// the HTML that references them.
type assetCollector struct{}

func (assetCollector) name() string { return "assets" }

func (assetCollector) collect(ctx context.Context, env *buildEnv, out *ArtifactSet) error {
	for _, a := range []struct{ src, path, url string }{
		{"templates/assets/style.css", "assets/style.css", "/assets/style.css"},
		{"templates/assets/app.js", "assets/app.js", "/assets/app.js"},
		{"templates/assets/favicon.svg", "assets/favicon.svg", "/assets/favicon.svg"},
	} {
		b, err := assetFS.ReadFile(a.src)
		if err != nil {
			return err
		}
		if err := out.Add(Artifact{Path: a.path, URL: a.url, Kind: KindMeta, Bytes: b}); err != nil {
			return err
		}
	}
	return nil
}

// metaCollector emits robots.txt, the sitemap index plus its child sitemaps, and
// the three /latest/ feeds.
type metaCollector struct{}

func (metaCollector) name() string { return "meta" }

func (metaCollector) collect(ctx context.Context, env *buildEnv, out *ArtifactSet) error {
	if err := out.Add(Artifact{Path: "robots.txt", URL: "/robots.txt", Kind: KindMeta, Bytes: buildRobots(env.siteBase)}); err != nil {
		return err
	}

	// Listings sitemap: the Tier-A pages + root that BuildSitemap produces.
	listings, err := BuildSitemap(env.siteBase, env.plan)
	if err != nil {
		return err
	}
	if err := out.Add(Artifact{Path: listingsSitemapPath, URL: "/" + listingsSitemapPath, Kind: KindMeta, Bytes: listings}); err != nil {
		return err
	}
	children := []string{"/" + listingsSitemapPath}

	// Per publication-month article sitemaps. A month with unchanged articles is
	// byte-identical run to run, so only growing months purge.
	idx := env.plan.Index
	for _, m := range sortedMonths(monthKeys(idx.months)) {
		arts := idx.articlesAt(idx.months[m])
		b, serr := buildArticleSitemap(env.siteBase, arts)
		if serr != nil {
			return serr
		}
		p := articleSitemapPath(m.Year, m.Month)
		if err := out.Add(Artifact{Path: p, URL: "/" + p, Kind: KindMeta, Bytes: b}); err != nil {
			return err
		}
		children = append(children, "/"+p)
	}

	// The sitemap index references every child sitemap (absolute URLs).
	smi, err := BuildSitemapIndex(env.siteBase, children)
	if err != nil {
		return err
	}
	if err := out.Add(Artifact{Path: "sitemap.xml", URL: "/sitemap.xml", Kind: KindMeta, Bytes: smi}); err != nil {
		return err
	}

	in := latestFeedInput(env)
	rss, err := BuildRSS(in)
	if err != nil {
		return err
	}
	if err := out.Add(Artifact{Path: "feed.xml", URL: "/feed.xml", Kind: KindMeta, Bytes: rss}); err != nil {
		return err
	}
	atom, err := BuildAtom(in)
	if err != nil {
		return err
	}
	if err := out.Add(Artifact{Path: "atom.xml", URL: "/atom.xml", Kind: KindMeta, Bytes: atom}); err != nil {
		return err
	}
	jf, err := BuildJSONFeed(in)
	if err != nil {
		return err
	}
	if err := out.Add(Artifact{Path: "feed.json", URL: "/feed.json", Kind: KindMeta, Bytes: jf}); err != nil {
		return err
	}

	// The version sentinel: a byte-stable fingerprint of the latest window that the
	// client polls to detect new content. KindMeta, so it rides the diff/purge
	// pipeline and lands in purge.json exactly when it changes.
	sentinel, err := buildVersionSentinel(env)
	if err != nil {
		return err
	}
	if err := out.Add(Artifact{Path: "latest/version.json", URL: "/latest/version.json", Kind: KindMeta, Bytes: sentinel}); err != nil {
		return err
	}
	return nil
}

// latestFeedInput builds the /latest/ feed window: the newest P articles in
// display order, each carrying the full rendered body.
func latestFeedInput(env *buildEnv) FeedInput {
	idx := env.plan.Index
	in := FeedInput{
		SiteURL:     env.siteBase,
		Title:       env.siteName,
		Description: "Latest articles from " + env.siteName,
		Items:       []FeedArticle{},
	}
	if len(idx.All) == 0 {
		return in
	}
	articles := idx.articlesAt(idx.scopeIndices(Scope{}))
	sortDisplay(articles)
	if len(articles) > env.pageSize {
		articles = articles[:env.pageSize]
	}
	for _, a := range articles {
		in.Items = append(in.Items, FeedArticle{Article: a, BodyHTML: env.bodyHTML[a.ContentHash]})
	}
	if len(articles) > 0 {
		in.Updated = articles[0].PublishedAt
	}
	return in
}
