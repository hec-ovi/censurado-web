package generate

import (
	"strings"
	"testing"
	"time"
)

// Server-side day separators (Feature 6): a minimalist full-width line carrying the
// Spanish day label is rendered BETWEEN articles of different published days on every
// listing scope, with JS off. The newest day carries no separator (it is the first
// group); older days each get one, before that day's first card.

func sepLI(label string) string {
	return `<li class="day-separator" role="separator" aria-label="` + label +
		`"><span class="day-separator-label">` + label + `</span></li>`
}

func TestDaySeparators_EveryScope(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Same author/section/topic so this set lands on latest, the section, the author,
	// and the topic scope alike. Two on the 20th, one on the 19th, one on the 18th.
	seed(t, repo, seedSpec{Title: "Veinte A", Author: "ada", Section: "politics", Topics: []string{"reforma"}, Published: date(2026, 6, 20)})
	seed(t, repo, seedSpec{Title: "Veinte B", Author: "ada", Section: "politics", Topics: []string{"reforma"}, Published: date(2026, 6, 20).Add(1)})
	seed(t, repo, seedSpec{Title: "Diecinueve", Author: "ada", Section: "politics", Topics: []string{"reforma"}, Published: date(2026, 6, 19)})
	seed(t, repo, seedSpec{Title: "Dieciocho", Author: "ada", Section: "politics", Topics: []string{"reforma"}, Published: date(2026, 6, 18)})
	genInto(t, repo, out, nil)

	sep19 := sepLI("19 de junio de 2026")
	sep18 := sepLI("18 de junio de 2026")

	scopes := []string{
		"latest/index.html",
		"section/politics/index.html",
		"author/ada/index.html",
		"topic/reforma/index.html",
	}
	for _, scope := range scopes {
		page := string(readArtifact(t, out, scope))
		if !strings.Contains(page, sep19) {
			t.Errorf("%s: missing the 19 June separator", scope)
		}
		if !strings.Contains(page, sep18) {
			t.Errorf("%s: missing the 18 June separator", scope)
		}
		// The newest day (the 20th) is the first group and carries NO separator.
		if strings.Contains(page, "20 de junio de 2026") {
			t.Errorf("%s: the newest day must not carry a separator label", scope)
		}
		// The first separator appears AFTER the first article card (the newest day
		// has no leading separator), and the 19th's separator precedes the 18th's.
		firstItem := strings.Index(page, `class="article-item"`)
		firstSep := strings.Index(page, `class="day-separator"`)
		if firstItem < 0 || firstSep < 0 || firstSep < firstItem {
			t.Errorf("%s: a separator renders before the first card (firstItem=%d firstSep=%d)", scope, firstItem, firstSep)
		}
		if i19, i18 := strings.Index(page, sep19), strings.Index(page, sep18); i19 < 0 || i18 < 0 || i19 > i18 {
			t.Errorf("%s: separators out of order (19=%d 18=%d)", scope, i19, i18)
		}
	}
}

// A scope whose articles are all on the same day shows NO separators at all.
func TestDaySeparators_SameDayNone(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Uno", Author: "ada", Section: "tech", Published: date(2026, 6, 10)})
	seed(t, repo, seedSpec{Title: "Dos", Author: "ada", Section: "tech", Published: date(2026, 6, 10).Add(1)})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, "latest/index.html"))
	if strings.Contains(page, `class="day-separator"`) {
		t.Errorf("same-day listing should render no day separators")
	}
}

// A sealed page's day separators are a pure function of its own article slice, so
// publishing a newer article never changes an older sealed page's bytes.
func TestDaySeparators_SealedPageByteStable(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// P=2: page/1 seals the two oldest by insertion; both are different days, so the
	// sealed page carries a separator. A later publish must not touch its bytes.
	seed(t, repo, seedSpec{Title: "A", Author: "ada", Section: "tech", Published: date(2026, 6, 1)})
	seed(t, repo, seedSpec{Title: "B", Author: "ada", Section: "tech", Published: date(2026, 6, 2)})
	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 })
	sealed := string(readArtifact(t, out, "latest/page/1/index.html"))
	if !strings.Contains(sealed, `class="day-separator"`) {
		t.Fatalf("sealed page across two days should carry a separator")
	}
	seed(t, repo, seedSpec{Title: "C", Author: "ada", Section: "tech", Published: date(2026, 6, 3)})
	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 })
	if string(readArtifact(t, out, "latest/page/1/index.html")) != sealed {
		t.Errorf("sealed page bytes changed after a newer article was published")
	}
}

// A day separator is keyed in Argentina local time (UTC-3), so an article published
// just after UTC midnight (but before ART midnight) groups under the PREVIOUS
// calendar day the reader's stamp shows, not the UTC day. This is the timezone-bug
// fix: the separator and the ART card stamp now name the same day.
func TestDaySeparators_ARTMidnightBoundary(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Same author/section so all three co-locate on latest. Display order is
	// PublishedAt DESC: A (newest) -> B -> C.
	// A: 2026-06-20 12:00Z = ART 2026-06-20 -> newest ART day, no separator.
	seed(t, repo, seedSpec{Title: "Mediodia", Author: "ada", Section: "politics", Published: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)})
	// B: 2026-06-20 01:00Z = ART 2026-06-19 22:00 -> ART day 19 June (the boundary card).
	seed(t, repo, seedSpec{Title: "Madrugada", Author: "ada", Section: "politics", Published: time.Date(2026, 6, 20, 1, 0, 0, 0, time.UTC)})
	// C: 2026-06-18 12:00Z = ART 18 June.
	seed(t, repo, seedSpec{Title: "Antesdeayer", Author: "ada", Section: "politics", Published: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)})
	genInto(t, repo, out, nil)

	page := string(readArtifact(t, out, "latest/index.html"))
	// The 01:00Z article (UTC 20 June) groups under ART 19 June, so a 19-June
	// separator appears; under the old UTC grouping it shared 20 June with the newest
	// card and got none.
	if !strings.Contains(page, sepLI("19 de junio de 2026")) {
		t.Errorf("boundary article (01:00Z) not grouped under its ART day (19 June)")
	}
	if !strings.Contains(page, sepLI("18 de junio de 2026")) {
		t.Errorf("missing the 18 June separator")
	}
	// No 20-June separator: the newest ART-20-June card is the first group, and the
	// 01:00Z card moved to 19 June.
	if strings.Contains(page, sepLI("20 de junio de 2026")) {
		t.Errorf("a 20-June separator appeared; the boundary card should be 19 June (ART)")
	}
	// The boundary card's kicker date text is its ART day (19 June); its datetime attr
	// stays UTC (2026-06-20T01:00:00Z). This pins the humandateart change: a revert to a
	// UTC kicker would render >2026-06-20</time> and this substring would vanish. B is
	// the only ART-19-June card, so the text is unique to it.
	if !strings.Contains(page, `>2026-06-19</time>`) {
		t.Errorf("boundary card kicker is not its ART day (19 June); the kicker regressed to UTC")
	}

	// Determinism: regenerating over the same store writes nothing new.
	res := genInto(t, repo, out, nil)
	if res.Written != 0 || res.Deleted != 0 {
		t.Errorf("re-gen not idempotent: written=%d deleted=%d", res.Written, res.Deleted)
	}
}

// The ART separator must not disturb the UTC month/dayKey bucketing: an article
// published just after UTC midnight on the 1st (still the previous month in ART)
// keeps its UTC month shard, while its separator shows the ART day. This pins that
// the separator change is cosmetic and never touches the byte-stable shard buckets.
func TestDaySeparators_ARTBoundaryKeepsUTCMonthBucket(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Newest so the boundary card below is not the first group (and thus gets a separator).
	seed(t, repo, seedSpec{Title: "DosJulio", Author: "ada", Section: "tech", Published: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)})
	// Boundary: 2026-07-01 01:00Z = ART 2026-06-30 22:00. UTC month = July; ART day = 30 June.
	x := seed(t, repo, seedSpec{Title: "UnoJulioMadrugada", Author: "ada", Section: "tech", Published: time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC)})
	genInto(t, repo, out, nil)

	page := string(readArtifact(t, out, "latest/index.html"))
	// The separator for the boundary card is its ART day (30 June), NOT 1 July.
	if !strings.Contains(page, sepLI("30 de junio de 2026")) {
		t.Errorf("boundary card separator is not its ART day (30 June)")
	}
	if strings.Contains(page, sepLI("1 de julio de 2026")) {
		t.Errorf("boundary card got a UTC-day (1 July) separator instead of its ART day")
	}
	// The UTC month bucket is unchanged: the boundary article is in the JULY shard.
	if !exists(out, "shards/latest/2026/07.json") {
		t.Fatalf("boundary article did not land in the UTC July month shard (dayKey/month bucket moved)")
	}
	if julyShard := string(readArtifact(t, out, "shards/latest/2026/07.json")); !strings.Contains(julyShard, x.Slug) {
		t.Errorf("boundary article missing from the July shard; its UTC month bucket changed")
	}
}
