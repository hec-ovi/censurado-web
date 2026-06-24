// Package cachepolicy is the single source of truth for the split CDN cache
// policy: which Cache-Control header each generated URL should carry. The policy
// is applied at the SERVING layer (a web server, CDN config, or S3 object metadata
// set at upload time), not by the generator, because the static files this repo
// produces carry no HTTP headers of their own. The one exception is /media, which
// the publish service serves itself and already sends the immutable header on.
//
// Classification is by URL pattern, not by generator artifact Kind, because a
// permalink and a listing page are both pages yet need opposite policies: a
// content-hash permalink is immutable (its hash is in the path, so a changed
// article is a new URL), while a listing page is mutable and must refresh quickly.
//
// The values are tuned for a news site whose readers poll the version sentinel
// every 30 to 60 seconds. See deploy/CACHING.md for the per-CDN guardrails (some
// edges cap or ignore stale-while-revalidate / stale-if-error).
package cachepolicy

import "strings"

const (
	// Immutable is for content-addressed URLs whose path changes when the bytes do:
	// article permalinks (/a/<slug>-<hash>/), the hashed-stable assets, and
	// content-addressed media. Safe to cache forever at the edge.
	Immutable = "public, max-age=31536000, immutable"

	// Listings is for the mutable surface: landing pages, feeds, JSON shards, and
	// manifests. A short fresh window plus stale-while-revalidate lets the edge serve
	// a stale copy instantly while it refetches once in the background, shielding the
	// origin from a spike; stale-if-error rides out an origin blip; and a missed
	// purge self-heals by the next window.
	Listings = "public, max-age=45, stale-while-revalidate=120, stale-if-error=86400"

	// Sentinel is for /latest/version.json, which clients poll with If-None-Match.
	// The tight window bounds the worst-case staleness for a reader who is not
	// actively polling, while the edge still answers almost every request as a 304.
	Sentinel = "public, max-age=10, stale-while-revalidate=60"
)

// sentinelPath is the one exact-match URL that gets the tight sentinel policy. It
// lives under /latest/, which is otherwise a listing, so it is matched first.
const sentinelPath = "/latest/version.json"

// immutablePrefixes are the path prefixes whose URLs are content-addressed and
// therefore immutable.
var immutablePrefixes = []string{"/a/", "/assets/", "/media/"}

// CacheControl returns the Cache-Control header value for a root-relative output
// path. An uploader or web-server config calls it per file to apply the policy.
func CacheControl(path string) string {
	if path == sentinelPath {
		return Sentinel
	}
	for _, p := range immutablePrefixes {
		if strings.HasPrefix(path, p) {
			return Immutable
		}
	}
	return Listings
}

// Rule is one entry of the machine-readable cache policy: how a set of URLs is
// matched and the Cache-Control they must carry. Rules are evaluated in order; the
// first match wins, and the final Default rule matches anything left.
type Rule struct {
	Description  string   `json:"description"`
	Exact        string   `json:"exact,omitempty"`
	Prefixes     []string `json:"prefixes,omitempty"`
	Default      bool     `json:"default,omitempty"`
	CacheControl string   `json:"cache_control"`
}

// Rules returns the ordered cache policy as data, so a deploy step can render it to
// a web server config, an S3 sync's --cache-control flags, or a CDN rule set. The
// order matches CacheControl's evaluation.
func Rules() []Rule {
	return []Rule{
		{Description: "version sentinel", Exact: sentinelPath, CacheControl: Sentinel},
		{Description: "content-addressed permalinks, assets, and media", Prefixes: append([]string(nil), immutablePrefixes...), CacheControl: Immutable},
		{Description: "listings, feeds, shards, and manifests", Default: true, CacheControl: Listings},
	}
}
