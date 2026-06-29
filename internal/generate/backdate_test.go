package generate

import (
	"strings"
	"testing"
)

// B1: pages paginate by PUBLICATION date, not insertion order, so the portada leads
// with the newest-published article and a back-dated insert surfaces in its published
// slot (matching the client's published-DESC infinite scroll).
func TestPagination_ByPublicationOrder(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Scrambled insert order; distinct published dates so there are no ties.
	for _, d := range []int{10, 20, 15, 25, 5} {
		seed(t, repo, seedSpec{Title: "P" + string(rune('a'+d)), Author: "ada", Section: "tech", Published: date(2026, 6, d)})
	}
	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 })

	// N=5, P=2 -> full=2, rem=1. The portada is the single newest-published (June 25),
	// regardless of the scrambled insert order.
	landing := permalinksIn(readArtifact(t, out, "latest/index.html"))
	if len(landing) != 1 {
		t.Fatalf("landing = %d articles, want 1 (N=5,P=2,rem=1)", len(landing))
	}
	// Pages descend to older (page 1 the oldest); both full pages hold 2.
	if p1 := permalinksIn(readArtifact(t, out, "latest/page/1/index.html")); len(p1) != 2 {
		t.Fatalf("page/1 = %d, want 2", len(p1))
	}
	if p2 := permalinksIn(readArtifact(t, out, "latest/page/2/index.html")); len(p2) != 2 {
		t.Fatalf("page/2 = %d, want 2", len(p2))
	}

	// Back-date a NEW article published June 22 (between 20 and 25). It must surface in
	// its PUBLISHED position, not pinned to the insertion tail.
	bd := seed(t, repo, seedSpec{Title: "Backdated", Author: "ada", Section: "tech", Published: date(2026, 6, 22)})
	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 })

	// N=6, P=2 -> rem==0; the landing mirrors the newest pair: June 25 then the
	// back-dated June 22 (display order, newest published first).
	landing2 := permalinksIn(readArtifact(t, out, "latest/index.html"))
	if len(landing2) != 2 {
		t.Fatalf("landing after backdate = %d, want 2", len(landing2))
	}
	if landing2[1] != articleURL(bd) {
		t.Errorf("back-dated June 22 not in its published slot: landing = %v, want second = %q", landing2, articleURL(bd))
	}
}

// B2: a back-dated insert into a closed month rebuilds that month's shard.
func TestBackdate_PriorMonthShardRebuilt(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "BD May 1", Author: "ada", Section: "bd", Published: date(2026, 5, 5)})
	seed(t, repo, seedSpec{Title: "BD May 2", Author: "ada", Section: "bd", Published: date(2026, 5, 8)})
	seed(t, repo, seedSpec{Title: "BD June 1", Author: "ada", Section: "bd", Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)

	mayShard := "shards/section/bd/2026/05.json"
	before := string(readArtifact(t, out, mayShard))

	// Back-dated May article (newest insert, published in the closed month).
	bd := seed(t, repo, seedSpec{Title: "BD May 3", Author: "ada", Section: "bd", Published: date(2026, 5, 6)})
	res := genInto(t, repo, out, nil)

	after := string(readArtifact(t, out, mayShard))
	if after == before {
		t.Errorf("closed-month shard was not rebuilt")
	}
	if !strings.Contains(after, articleURL(bd)) {
		t.Errorf("rebuilt shard missing the back-dated entry")
	}
	if !strSet(res.Purge)["/shards/section/bd/2026/05.json"] {
		t.Errorf("purge missing the rebuilt closed-month shard URL; got %v", res.Purge)
	}
	if !strSet(res.Purge)["/section/bd/"] {
		t.Errorf("purge missing the affected manifest (section/bd landing) URL")
	}
}

// B3: a back-dated insert into a multi-part month re-cuts and re-purges parts.
func TestBackdate_MultiPartReorder(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "MP A", Author: "ada", Section: "bd3", Published: date(2026, 6, 2)})
	seed(t, repo, seedSpec{Title: "MP B", Author: "ada", Section: "bd3", Published: date(2026, 6, 4)})
	seed(t, repo, seedSpec{Title: "MP C", Author: "ada", Section: "bd3", Published: date(2026, 6, 6)})
	genInto(t, repo, out, func(o *Options) { o.ShardMaxEntries = 2 })

	part2 := "shards/section/bd3/2026/06.2.json"
	if !exists(out, part2) {
		t.Fatalf("expected a 2-part June month")
	}
	before := string(readArtifact(t, out, part2))

	bd := seed(t, repo, seedSpec{Title: "MP D", Author: "ada", Section: "bd3", Published: date(2026, 6, 3)})
	res := genInto(t, repo, out, func(o *Options) { o.ShardMaxEntries = 2 })

	if string(readArtifact(t, out, part2)) == before {
		t.Errorf("multi-part month part 2 not re-cut")
	}
	if !strSet(res.Purge)["/shards/section/bd3/2026/06.2.json"] {
		t.Errorf("purge missing the re-cut part URL; got %v", res.Purge)
	}
	if !strings.Contains(string(readArtifact(t, out, part2)), articleURL(bd)) {
		t.Errorf("re-cut part missing the back-dated entry")
	}

	// Deterministic: a third no-op run purges nothing.
	res3 := genInto(t, repo, out, func(o *Options) { o.ShardMaxEntries = 2 })
	if len(res3.Purge) != 0 || res3.Written != 0 {
		t.Errorf("re-cut not deterministic: written=%d purge=%v", res3.Written, res3.Purge)
	}
}
