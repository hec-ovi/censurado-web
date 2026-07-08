package generate

import (
	"os"
	"regexp"
	"testing"
)

// TestCardShareHiddenOnDesktop guards the rule that listing/related CARDS drop the compact
// share row on desktop (>= 42rem): sharing belongs on the full article page (.article-share),
// which must stay visible. Cards keep the row on mobile, so only the desktop breakpoint hides it.
func TestCardShareHiddenOnDesktop(t *testing.T) {
	css, err := os.ReadFile("templates/assets/style.css")
	if err != nil {
		t.Fatal(err)
	}
	hide := regexp.MustCompile(`(?s)@media \(min-width: 42rem\) \{\s*\.card-share \{\s*display: none;`)
	if !hide.MatchString(string(css)) {
		t.Error("style.css must hide .card-share under @media (min-width: 42rem) so cards show no share buttons on desktop")
	}
	if regexp.MustCompile(`\.article-share \{\s*display: none`).MatchString(string(css)) {
		t.Error(".article-share must stay visible on the full article page (only .card-share is hidden on desktop)")
	}
}
