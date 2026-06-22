package generate

import (
	"strings"
	"testing"
)

// B1: a back-dated insert never enters a sealed page (insertion-order seal).
//
// Deviation from the literal row: PageSize=3 (not 2) with 7 seeds. PageSize=2
// cannot satisfy both "page/1, page/2 sealed; landing=1" AND "lands only in the
// landing / no new sealed page" on a single insert, because rem_before would
// equal P-1 and seal. PageSize=3 with 7 seeds is the faithful construction.
func TestBackdate_SealedPageImmutable(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Insertion order = published descending (June 20..14).
	for d := 20; d >= 14; d-- {
		seed(t, repo, seedSpec{Title: "Seal " + string(rune('a'+20-d)), Author: "ada", Section: "tech", Published: date(2026, 6, d)})
	}
	genInto(t, repo, out, func(o *Options) { o.PageSize = 3 })

	page1 := string(readArtifact(t, out, "latest/page/1/index.html"))
	page2 := string(readArtifact(t, out, "latest/page/2/index.html"))

	// The lone pre-existing landing article is the oldest (June 14).
	oldest := permalinksIn(readArtifact(t, out, "latest/index.html"))
	if len(oldest) != 1 {
		t.Fatalf("landing before = %d articles, want 1", len(oldest))
	}
	oldestURL := oldest[0]

	// Back-date into page/1's published window; it is still the newest insert.
	bd := seed(t, repo, seedSpec{Title: "Backdated", Author: "ada", Section: "tech", Published: date(2026, 6, 19)})
	res := genInto(t, repo, out, func(o *Options) { o.PageSize = 3 })

	if string(readArtifact(t, out, "latest/page/1/index.html")) != page1 {
		t.Errorf("sealed page/1 changed under back-dating")
	}
	if string(readArtifact(t, out, "latest/page/2/index.html")) != page2 {
		t.Errorf("sealed page/2 changed under back-dating")
	}
	landing := permalinksIn(readArtifact(t, out, "latest/index.html"))
	if len(landing) != 2 {
		t.Fatalf("landing after = %d, want 2", len(landing))
	}
	// Ordered by published_at: back-dated June 19 sorts above the June 14 oldest.
	if landing[0] != articleURL(bd) || landing[1] != oldestURL {
		t.Errorf("landing display order = %v, want [%q %q]", landing, articleURL(bd), oldestURL)
	}
	for _, u := range res.Purge {
		if strings.Contains(u, "/page/") {
			t.Errorf("sealed-page URL in purge: %q", u)
		}
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
