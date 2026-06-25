package generate

import (
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
)

// ShardEntry is the body-free projection of an article for the client-side
// Tier-B refiner. Routing-axis facets are slug form so client membership matches
// server-rendered membership; author_label carries the original display string.
// The JSON field set is frozen (11 fields); id is unexported and never
// serialized, kept only to break display-order ties consistently with the HTML
// listing (ties by id). Subtitle and Image carry the authored dek and hero URL
// so client-rebuilt cards (facet filter + live refresh) match the server cards;
// both are always present (possibly "") to keep the exact-key-set invariant.
type ShardEntry struct {
	Slug        string   `json:"slug"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Subtitle    string   `json:"subtitle"`
	Image       string   `json:"image"`
	Author      string   `json:"author"`
	AuthorLabel string   `json:"author_label"`
	Section     string   `json:"section"`
	Topics      []string `json:"topics"`
	PublishedAt string   `json:"published_at"`
	TS          int64    `json:"ts"`

	id string // article id; sort tie-break only, never serialized
}

// ShardEntryOf projects an Article (already time-truncated in Index.All). An
// empty section/author slug falls back to ContentHash[:12] so the entry
// satisfies the frozen ^[a-z0-9]+...$ pattern even for fully non-ASCII facets.
func ShardEntryOf(a domain.Article) ShardEntry {
	topics := make([]string, 0, len(a.Topics))
	seen := map[string]struct{}{}
	for _, t := range a.Topics {
		s, ok := facetSlug(t)
		if !ok {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		topics = append(topics, s)
	}
	sec, ok := facetSlug(a.Section)
	if !ok {
		sec = a.ContentHash[:12]
	}
	auth, ok := facetSlug(a.Author)
	if !ok {
		auth = a.ContentHash[:12]
	}
	return ShardEntry{
		Slug:        a.Slug,
		URL:         articleURL(a),
		Title:       a.Title,
		Subtitle:    firstMetadataString(a.Metadata, "subtitle"),
		Image:       metadataMediaSrc("", a.Metadata, "image"),
		Author:      auth,
		AuthorLabel: authorDisplayLabel(a),
		Section:     sec,
		Topics:      topics,
		PublishedAt: a.PublishedAt.UTC().Format(time.RFC3339),
		TS:          a.PublishedAt.UTC().Unix(),
		id:          a.ID,
	}
}
