package generate

import (
	"fmt"
	"path"
	"strconv"

	"github.com/hec-ovi/censurado-web-backend/domain"
)

// facetSlug slugifies a facet value for AXIS/URL purposes. ok is false when
// Slugify yields "", so the caller drops it from that axis (no /section// URL is
// ever produced). No hash fallback is applied here; the shard projection
// (ShardEntryOf) applies the ContentHash[:12] fallback so Latest/Month shard
// entries stay schema-valid even for fully non-ASCII facets.
func facetSlug(raw string) (slug string, ok bool) {
	s := domain.Slugify(raw)
	return s, s != ""
}

// hash8 is the cache-busting permalink suffix: the first 8 hex chars of the
// content hash. ContentHash is always hex, so the suffix matches [0-9a-f]{8}.
func hash8(contentHash string) string {
	if len(contentHash) < 8 {
		return contentHash
	}
	return contentHash[:8]
}

func articleURL(a domain.Article) string { return "/a/" + a.Slug + "-" + hash8(a.ContentHash) + "/" }
func articlePath(a domain.Article) string {
	return path.Join("a", a.Slug+"-"+hash8(a.ContentHash), "index.html")
}

// pageDir is the OutDir-relative directory for page n of a scope. n==0 is the
// bare landing remainder; n>=1 is a sealed full page.
func pageDir(scope string, n int) string {
	if n == 0 {
		return scope
	}
	return path.Join(scope, "page", strconv.Itoa(n))
}
func pagePath(scope string, n int) string { return path.Join(pageDir(scope, n), "index.html") }
func pageURL(scope string, n int) string  { return "/" + pageDir(scope, n) + "/" }

func shardDir(scope string) string { return path.Join("shards", scope) }

// manifestPath/manifestURL locate a scope's standalone PageManifest artifact.
// scope is the Scope.ShardKey() (e.g. "latest", "section/tech"). The href is a
// pure function of the scope, so it stays byte-stable on sealed pages.
func manifestPath(scope string) string { return path.Join("manifest", scope, "index.json") }
func manifestURL(scope string) string  { return "/" + manifestPath(scope) }

// shardPartPath is the OutDir-relative path of one shard part. Part 1 (and any
// single-part month) is <mm>.json; higher parts are <mm>.<part>.json.
func shardPartPath(scope string, y, m, part int) string {
	base := fmt.Sprintf("%s/%04d/%02d", shardDir(scope), y, m)
	if part <= 1 {
		return base + ".json"
	}
	return fmt.Sprintf("%s.%d.json", base, part)
}

// absolute joins a trailing-slash-trimmed origin with a root-relative URL.
func absolute(base, rootRel string) string { return base + rootRel }

func monthKey(y, m int) string { return fmt.Sprintf("%04d-%02d", y, m) }
