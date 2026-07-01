package generate

import (
	"strings"
	"testing"
	"time"
)

// TestCardSummary_DesktopFillAndImageHide pins the desktop card-height contract:
// every listing grid card carries the summary (.card-description) in the markup so
// mobile keeps it, but the desktop CSS hides it on IMAGE cards (the image carries
// the height, so they stop running "ridiculously" tall) and turns it into a flex
// fill on TEXT cards, which initCardFill then clamps with an ellipsis.
func TestCardSummary_DesktopFillAndImageHide(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// A lead (first item) plus two grid cards: one text-only, one with an image,
	// both carrying a summary. All three share ONE day (descending time) so the two
	// non-lead cards are regular grid cards, not per-day leads (the first card of a
	// new day now renders as that day's full-width lead).
	seed(t, repo, seedSpec{Title: "Lead", Author: "ada", Section: "tech", Published: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC),
		Metadata: map[string]any{"subtitle": "Dek lead.", "description": "Resumen del lead."}})
	seed(t, repo, seedSpec{Title: "Texto", Author: "ada", Section: "tech", Published: time.Date(2026, 6, 5, 11, 0, 0, 0, time.UTC),
		Metadata: map[string]any{"subtitle": "Dek texto.", "description": "El resumen del texto que llena la tarjeta."}})
	seed(t, repo, seedSpec{Title: "Imagen", Author: "ada", Section: "tech", Published: time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC),
		Metadata: map[string]any{"subtitle": "Dek imagen.", "description": "El resumen de la tarjeta con imagen.",
			"image": "/media/" + strings.Repeat("b", 64) + ".png"}})
	genInto(t, repo, out, nil)

	listing := string(readArtifact(t, out, "latest/index.html"))
	css := string(readArtifact(t, out, "assets/style.css"))
	js := string(readArtifact(t, out, "assets/app.js"))

	// The summary node renders in the grid cards (both the text card and the image
	// card; it is the desktop CSS, not the markup, that hides it on image cards).
	if !strings.Contains(listing, `<p class="card-description">El resumen del texto que llena la tarjeta.</p>`) {
		t.Errorf("text card summary (.card-description) not rendered")
	}
	if !strings.Contains(listing, `<p class="card-description">El resumen de la tarjeta con imagen.</p>`) {
		t.Errorf("image card summary node missing from markup")
	}
	// Desktop CSS: image cards hide the summary; text cards turn it into a flex fill.
	for _, want := range []string{
		".article-item:not(:first-child) .card-has-media:not(.lead-card) .card-description",
		".article-item:not(:first-child) .card-textonly:not(.lead-card) .card-description",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("desktop card-summary CSS missing %q", want)
		}
	}
	// The client carries the same summary (so a filtered/appended card matches the
	// server) and the fill enhancement.
	if !strings.Contains(js, "card-description") {
		t.Errorf("app.js card builder does not render .card-description")
	}
	if !strings.Contains(js, "initCardFill") {
		t.Errorf("app.js missing initCardFill")
	}
}
