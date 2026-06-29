package generate

import (
	"time"

	"github.com/hec-ovi/censurado-web-backend/domain"
)

// ShardEntry is the body-free projection of an article for the client-side
// Tier-B refiner. Routing-axis facets are slug form so client membership matches
// server-rendered membership; author_label carries the original display string.
// The JSON field set is frozen (15 fields); id is unexported and never
// serialized, kept only to break display-order ties consistently with the HTML
// listing (ties by id). Subtitle, Description, Image and Avatar carry the authored
// dek, standfirst, hero URL and byline avatar so client-rebuilt cards (facet
// filter, live refresh, infinite scroll) match the server cards even for an author
// absent from the page's first batch; all are always present (possibly "") to keep
// the exact-key-set invariant. Image falls back to the first body video's YouTube
// poster when there is no metadata.image, and Video is then true so the client
// rebuilds the play badge over that poster (matching the server thumbForArticle).
type ShardEntry struct {
	Slug        string   `json:"slug"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Subtitle    string   `json:"subtitle"`
	Description string   `json:"description"`
	Image       string   `json:"image"`
	Video       bool     `json:"video"`
	Author      string   `json:"author"`
	AuthorLabel string   `json:"author_label"`
	Avatar      string   `json:"avatar"`
	Section     string   `json:"section"`
	Topics      []string `json:"topics"`
	PublishedAt string   `json:"published_at"`
	TS          int64    `json:"ts"`
	CreatedTS   int64    `json:"cts"` // created-at epoch (real insert time); drives the displayed card stamp

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
	// Mirror thumbForArticle: a real metadata.image wins; otherwise fall the card
	// image back to the first body video's YouTube poster and flag Video so the
	// client rebuilds the same play badge the server renders.
	image := metadataMediaSrc("", a.Metadata, "image")
	video := false
	if image == "" {
		if poster := firstBodyVideoPoster(a); poster != "" {
			image = poster
			video = true
		}
	}
	return ShardEntry{
		Slug:        a.Slug,
		URL:         articleURL(a),
		Title:       a.Title,
		Subtitle:    firstMetadataString(a.Metadata, "subtitle"),
		Description: firstMetadataString(a.Metadata, "description"),
		Image:       image,
		Video:       video,
		Author:      auth,
		AuthorLabel: authorDisplayLabel(a),
		Avatar:      metadataMediaSrc("", a.Metadata, "author_avatar", "avatar"),
		Section:     sec,
		Topics:      topics,
		PublishedAt: a.PublishedAt.UTC().Format(time.RFC3339),
		TS:          a.PublishedAt.UTC().Unix(),
		CreatedTS:   a.CreatedAt.UTC().Unix(),
		id:          a.ID,
	}
}
