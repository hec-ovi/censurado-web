package generate

import (
	"context"
	"testing"
	"time"
)

func buildLatestPages(t *testing.T, n, p int) (*Index, []Page) {
	t.Helper()
	repo := newStore(t)
	for i := 0; i < n; i++ {
		seed(t, repo, seedSpec{
			Title:     "Page Art " + string(rune('A'+i)),
			Author:    "ada",
			Section:   "tech",
			Published: date(2026, 6, 1).Add(time.Duration(i) * time.Hour),
		})
	}
	idx, err := BuildIndex(context.Background(), repo)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	return idx, idx.chunkPages(Scope{}, p)
}

// E4
func TestPaginationBoundaries(t *testing.T) {
	// P=2, N=5: full=2, rem=1.
	idx, pages := buildLatestPages(t, 5, 2)
	_ = idx
	if len(pages) != 3 {
		t.Fatalf("pages = %d, want 3 (2 sealed + landing)", len(pages))
	}
	p1, p2, landing := pages[0], pages[1], pages[2]

	if p1.Number != 1 || p1.Landing || len(p1.Articles) != 2 {
		t.Errorf("page1 = {N:%d landing:%v len:%d}, want {1 false 2}", p1.Number, p1.Landing, len(p1.Articles))
	}
	if p1.Canonical != "/latest/page/1/" || p1.Prev != "" || p1.Next != "/latest/page/2/" {
		t.Errorf("page1 links = {can:%q prev:%q next:%q}", p1.Canonical, p1.Prev, p1.Next)
	}
	if p2.Number != 2 || len(p2.Articles) != 2 {
		t.Errorf("page2 = {N:%d len:%d}, want {2 2}", p2.Number, len(p2.Articles))
	}
	if p2.Canonical != "/latest/page/2/" || p2.Prev != "/latest/page/1/" || p2.Next != "/latest/" {
		t.Errorf("page2 links = {can:%q prev:%q next:%q}", p2.Canonical, p2.Prev, p2.Next)
	}
	if !landing.Landing || landing.Number != 0 || len(landing.Articles) != 1 {
		t.Errorf("landing = {N:%d landing:%v len:%d}, want {0 true 1}", landing.Number, landing.Landing, len(landing.Articles))
	}
	if landing.Canonical != "/latest/" || landing.Prev != "/latest/page/2/" || landing.Next != "" {
		t.Errorf("landing links = {can:%q prev:%q next:%q}", landing.Canonical, landing.Prev, landing.Next)
	}

	// rem==0 seam: P=2, N=4 -> landing canonical points at sealed page/2.
	_, pages0 := buildLatestPages(t, 4, 2)
	l0 := pages0[len(pages0)-1]
	if !l0.Landing || l0.Canonical != "/latest/page/2/" {
		t.Errorf("rem==0 landing canonical = %q, want /latest/page/2/", l0.Canonical)
	}
}

// E6
func TestRelPrevNextChain(t *testing.T) {
	_, pages := buildLatestPages(t, 5, 2) // full=2
	// One connected chain page/1 -> page/2 -> landing.
	p1, p2, landing := pages[0], pages[1], pages[2]
	if p1.Next != "/latest/page/2/" {
		t.Errorf("page1.Next = %q", p1.Next)
	}
	if p2.Next != landing.Scope.PageURL(0) {
		t.Errorf("page full.Next = %q, want landing %q", p2.Next, landing.Scope.PageURL(0))
	}
	if landing.Next != "" {
		t.Errorf("landing.Next = %q, want empty", landing.Next)
	}
	if landing.Prev != "/latest/page/2/" {
		t.Errorf("landing.Prev = %q", landing.Prev)
	}
	// No link points beyond the highest sealed page.
	for _, pg := range pages {
		for _, link := range []string{pg.Prev, pg.Next} {
			if link == "/latest/page/3/" {
				t.Errorf("link to non-existent page/3 from page %d", pg.Number)
			}
		}
	}

	// rem==0 landing canonical points at page/full.
	_, pages0 := buildLatestPages(t, 6, 2) // full=3, rem=0
	l0 := pages0[len(pages0)-1]
	if l0.Canonical != "/latest/page/3/" {
		t.Errorf("rem==0 landing canonical = %q, want /latest/page/3/", l0.Canonical)
	}
}

// On an exact page boundary (rem==0) the landing must not be empty: /latest/
// mirrors the NEWEST sealed page (page/full), not page/1, so the portada keeps
// showing the latest articles instead of going blank.
func TestPagination_BoundaryLandingMirrorsNewest(t *testing.T) {
	_, pages := buildLatestPages(t, 4, 2) // full=2, rem=0
	p2 := pages[1]                        // newest sealed page
	landing := pages[len(pages)-1]
	if !landing.Landing || landing.Number != 0 {
		t.Fatalf("last page is not the landing")
	}
	if landing.Canonical != "/latest/page/2/" {
		t.Errorf("landing canonical = %q, want /latest/page/2/", landing.Canonical)
	}
	if len(landing.Articles) != len(p2.Articles) || len(landing.Articles) != 2 {
		t.Fatalf("landing has %d articles, want 2 (mirror of newest sealed page)", len(landing.Articles))
	}
	for i := range p2.Articles {
		if landing.Articles[i].ID != p2.Articles[i].ID {
			t.Errorf("landing[%d]=%q, want newest-page article %q", i, landing.Articles[i].ID, p2.Articles[i].ID)
		}
	}
}

// End-to-end: the rendered /latest/index.html on a page boundary contains
// article permalinks (the homepage is never blank when the article count is an
// exact multiple of the page size).
func TestPagination_BoundaryPortadaNotBlank(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	for d := 1; d <= 4; d++ {
		seed(t, repo, seedSpec{Title: "Art " + string(rune('A'+d)), Author: "ada", Section: "tech", Published: date(2026, 6, d)})
	}
	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 }) // N=4, P=2 -> rem==0
	got := permalinksIn(readArtifact(t, out, "latest/index.html"))
	if len(got) != 2 {
		t.Errorf("/latest/ at a page boundary shows %d articles, want 2 (must not be blank)", len(got))
	}
}
