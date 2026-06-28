package generate

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hec-ovi/censurado-web-backend/domain"
)

// railListRe isolates the "Lo más leído" rail's <ol class="ranked-list"> ... </ol>
// so we can collect just the rail's permalinks, separate from the grid's.
var railListRe = regexp.MustCompile(`(?s)<ol class="ranked-list">(.*?)</ol>`)

func railPermalinks(b []byte) []string {
	m := railListRe.FindSubmatch(b)
	if m == nil {
		return nil
	}
	return permalinkRe.FindAllString(string(m[1]), -1)
}

// TestListingRail_ExcludesPageGrid pins the requested fix: "Lo más leído" must not
// repeat the articles already shown in the page's own grid. The rail is drawn from
// the scope's older articles (below this page's window), so the two sets are
// disjoint and the rail is still populated.
func TestListingRail_ExcludesPageGrid(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	for i := 1; i <= 8; i++ {
		seed(t, repo, seedSpec{
			Title:     fmt.Sprintf("Articulo %d", i),
			Author:    "ada",
			Section:   "tech",
			Published: date(2026, 6, i),
		})
	}
	genInto(t, repo, out, func(o *Options) { o.PageSize = 4 })

	page := readArtifact(t, out, "latest/index.html")
	grid := strSet(permalinksIn(page))
	rail := railPermalinks(page)

	if len(grid) == 0 {
		t.Fatal("no article cards in the grid")
	}
	if len(rail) == 0 {
		t.Fatal("rail 'Lo más leído' is empty; expected older articles below the page")
	}
	for _, r := range rail {
		if grid[r] {
			t.Errorf("rail repeats a front-page article: %s", r)
		}
	}
}

// TestArticleRails_CapAtFour proves both permalink rails clamp at 4: "Más de este
// autor" (lowered from 6) and "Relacionados". The newest piece has five older
// same-author, same-topic candidates, so an uncapped rail would show five.
func TestArticleRails_CapAtFour(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	var last domain.Article
	for i := 1; i <= 6; i++ {
		last = seed(t, repo, seedSpec{
			Title:     fmt.Sprintf("Pieza %d", i),
			Author:    "ada",
			Section:   "tech",
			Topics:    []string{"ai"},
			Published: date(2026, 6, i),
		})
	}
	genInto(t, repo, out, nil)

	article := string(readArtifact(t, out, articlePath(last)))
	if n := strings.Count(article, `class="author-more-item"`); n != 4 {
		t.Errorf("'Más de este autor' rendered %d items, want exactly 4 (5 candidates, cap 4)", n)
	}
	if n := strings.Count(article, `class="related-item"`); n < 1 || n > 4 {
		t.Errorf("'Relacionados' rendered %d items, want 1..4", n)
	}
}
