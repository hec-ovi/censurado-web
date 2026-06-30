package generate

import (
	"strings"
	"testing"
)

// A listing card for an article whose only media is a body {{video:<id>}} marker
// (no metadata.image) borrows the YouTube poster as its thumbnail and shows a play
// badge over it, so the card reads as a video without the embed itself.
func TestVideoThumb_BodyVideoPosterOnCard(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{
		Title: "Transmisión en vivo", Author: "bob", Section: "world",
		Body:      "Intro.\n\n{{video:RdhnMD40q3Y}}\n\nOutro.",
		Published: date(2026, 6, 10),
	})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, "section/world/index.html"))

	for _, want := range []string{
		"card-has-video",
		"https://i.ytimg.com/vi/RdhnMD40q3Y/hqdefault.jpg",
		`class="card-media-play"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("body-video card missing %q\n%s", want, page)
		}
	}
	if strings.Contains(page, "card-textonly") {
		t.Errorf("body-video card should not be text-only\n%s", page)
	}
}

// A real metadata.image always wins over a body video: the card renders the image
// and carries no video badge, so image-article behavior is unchanged.
func TestVideoThumb_MetadataImageWinsOverBodyVideo(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	img := "/media/" + strings.Repeat("a", 64) + ".png"
	seed(t, repo, seedSpec{
		Title: "Con imagen propia", Author: "bob", Section: "world",
		Body:      "Intro.\n\n{{video:RdhnMD40q3Y}}\n\nOutro.",
		Published: date(2026, 6, 11),
		Metadata:  map[string]any{"image": img},
	})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, "section/world/index.html"))

	if !strings.Contains(page, `src="`+img+`"`) {
		t.Errorf("image card should render the metadata.image %q\n%s", img, page)
	}
	for _, notWant := range []string{
		"card-has-video",
		`class="card-media-play"`,
		"i.ytimg.com/vi/RdhnMD40q3Y",
	} {
		if strings.Contains(page, notWant) {
			t.Errorf("image-winning card should not contain %q\n%s", notWant, page)
		}
	}
}
