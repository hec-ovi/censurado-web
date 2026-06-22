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
	siteBase string
	now      time.Time
	pageSize int
	capE     int
	capB     int
	siteName string
	plan     *Plan
	bodyHTML map[string]string // ContentHash -> rendered body, rendered once
}

func buildEnvFrom(opts Options) *buildEnv {
	return &buildEnv{
		siteBase: opts.BaseURL,
		now:      opts.Now,
		pageSize: opts.PageSize,
		capE:     opts.ShardMaxEntries,
		capB:     opts.ShardMaxGzipBytes,
		siteName: opts.SiteName,
	}
}

// collectors returns the collectors in fixed registration order; the order is a
// deterministic tie-break for the duplicate-path error.
func collectors() []collector {
	return []collector{
		tierAPageCollector{},
		articleCollector{},
		shardCollector{},
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
		months, partsByMonth, total, err := buildScopeShards(env, sc)
		if err != nil {
			return err
		}
		manifest, err := ManifestScript(BuildManifest(sc, env.capE, env.capB, months, partsByMonth, total))
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
		months, partsByMonth, _, err := buildScopeShards(env, sc)
		if err != nil {
			return err
		}
		for _, m := range months {
			for _, part := range partsByMonth[monthKey(m.Year, m.Month)] {
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

// metaCollector emits the sitemap and the three /latest/ feeds.
type metaCollector struct{}

func (metaCollector) name() string { return "meta" }

func (metaCollector) collect(ctx context.Context, env *buildEnv, out *ArtifactSet) error {
	sm, err := BuildSitemap(env.siteBase, env.plan)
	if err != nil {
		return err
	}
	if err := out.Add(Artifact{Path: "sitemap.xml", URL: "/sitemap.xml", Kind: KindMeta, Bytes: sm}); err != nil {
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
