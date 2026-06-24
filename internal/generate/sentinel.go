package generate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// versionSentinel is the payload of /latest/version.json: a tiny, edge-cacheable
// file the client polls with If-None-Match to learn when new content landed,
// without ever touching the origin's data layer. V is a content fingerprint over
// the newest articles in display order, so it is byte-stable when nothing changed
// (the edge answers 304) and changes the instant the top of the site does.
// LatestIDs are those articles' slugs, newest first, so a client can tell which
// stories are new before fetching a shard.
type versionSentinel struct {
	V         string   `json:"v"`
	LatestIDs []string `json:"latest_ids"`
}

// buildVersionSentinel computes the sentinel for the current latest window. The
// fingerprint hashes each window article's content hash and slug, length-prefixed
// so field boundaries cannot collide, in display order. An edit within the window
// (the content hash moves), a new top story, or a reorder all change V, while an
// unchanged window reproduces byte-identical output. It never reads env.now, which
// is what keeps the 304 contract intact: a regenerate over an unchanged store emits
// the identical sentinel.
func buildVersionSentinel(env *buildEnv) ([]byte, error) {
	in := latestFeedInput(env)
	ids := make([]string, 0, len(in.Items))
	h := sha256.New()
	for _, it := range in.Items {
		ids = append(ids, it.Article.Slug)
		fmt.Fprintf(h, "%d:%s|%d:%s\n",
			len(it.Article.ContentHash), it.Article.ContentHash,
			len(it.Article.Slug), it.Article.Slug)
	}
	b, err := json.Marshal(versionSentinel{V: hex.EncodeToString(h.Sum(nil)), LatestIDs: ids})
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
