package generate

import (
	"testing"

	"github.com/hec-ovi/censurado-web-backend/domain"
)

// genArt builds a minimal renderable article (a real section/author/hash so
// ShardEntryOf never hits the ContentHash fallback) carrying the given metadata
// and body.
func genArt(meta map[string]any, body string) domain.Article {
	return domain.Article{
		Title: "T", Body: body, Section: "tech", Author: "ada",
		ContentHash: "0123456789abcdef", Metadata: meta,
	}
}

// TestCardThumb_AuthoredCardDrivesTheListingCard proves the authored metadata.card
// block is the single source of what the listing card shows, decoupled from the
// body, and that the shard projection (client card) agrees with the server thumb.
func TestCardThumb_AuthoredCardDrivesTheListingCard(t *testing.T) {
	cases := []struct {
		name      string
		card      map[string]any
		body      string
		wantKind  string
		wantSrcNE bool
		wantVideo bool // shard play-badge flag
	}{
		{"image card", map[string]any{"type": "image", "src": "/media/abc.png", "alt": "A"}, "", "image", true, false},
		{"youtube card", map[string]any{"type": "youtube", "src": "eD099-68xvw"}, "", "youtube", true, true},
		{"self-video card (poster)", map[string]any{"type": "video", "src": "/media/poster.png"}, "", "video", true, true},
		// A text card wins even when the body embeds a video: the card is decoupled from content.
		{"text card beats a body video", map[string]any{"type": "text"}, "lead {{video:eD099-68xvw}} more", "", false, false},
		// An unknown type falls back to the legacy derivation (here, the body youtube poster).
		{"unknown type falls back to legacy", map[string]any{"type": "reel"}, "{{video:eD099-68xvw}}", "youtube", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := genArt(map[string]any{"card": tc.card}, tc.body)
			v := thumbForArticle(a)
			if v.Kind != tc.wantKind {
				t.Errorf("thumb Kind = %q, want %q", v.Kind, tc.wantKind)
			}
			if (v.Src != "") != tc.wantSrcNE {
				t.Errorf("thumb Src = %q, want non-empty=%v", v.Src, tc.wantSrcNE)
			}
			se := ShardEntryOf(a)
			if se.Image != v.Src {
				t.Errorf("shard Image = %q, want %q (must agree with the server thumb)", se.Image, v.Src)
			}
			if se.Video != tc.wantVideo {
				t.Errorf("shard Video badge = %v, want %v", se.Video, tc.wantVideo)
			}
		})
	}
}

// TestShardEntry_SectionLabelIsSpanish proves the client shard carries the reader-facing
// Spanish section label (section_label) alongside the English URL slug. A JS-filled rail
// ("Más de este autor") reads section_label, so it shows "Política" instead of the raw slug
// "politics" that CSS text-transform:uppercase would render as "POLITICS".
func TestShardEntry_SectionLabelIsSpanish(t *testing.T) {
	a := domain.Article{
		Title: "T", Body: "b", Section: "politics", Author: "ada",
		ContentHash: "0123456789abcdef",
	}
	se := ShardEntryOf(a)
	if se.Section != "politics" {
		t.Errorf("shard Section slug = %q, want %q (the URL slug stays English)", se.Section, "politics")
	}
	if se.SectionLabel != "Política" {
		t.Errorf("shard SectionLabel = %q, want %q (the reader-facing Spanish label)", se.SectionLabel, "Política")
	}
}

// TestCardThumb_LegacyUnchanged pins that a piece with NO card block still renders
// exactly as before (byte-stable for sealed pages): metadata.image -> an image
// card, a body YouTube video -> its poster with a play badge, plain text -> none.
func TestCardThumb_LegacyUnchanged(t *testing.T) {
	if v := thumbForArticle(genArt(map[string]any{"image": "/media/abc.png"}, "")); v.Kind != "image" || v.Src == "" {
		t.Errorf("legacy image thumb = %+v, want an image card", v)
	}
	if v := thumbForArticle(genArt(nil, "{{video:eD099-68xvw}}")); v.Kind != "youtube" || v.Src == "" {
		t.Errorf("legacy body-video thumb = %+v, want a youtube poster", v)
	}
	if v := thumbForArticle(genArt(nil, "just words")); v.Kind != "" || v.Src != "" {
		t.Errorf("legacy text thumb = %+v, want empty (text card)", v)
	}
}
