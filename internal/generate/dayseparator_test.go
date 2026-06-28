package generate

import (
	"strings"
	"testing"
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
	seed(t, repo, seedSpec{Title: "Veinte A", Author: "lara-arianna", Section: "politics", Topics: []string{"reforma"}, Published: date(2026, 6, 20)})
	seed(t, repo, seedSpec{Title: "Veinte B", Author: "lara-arianna", Section: "politics", Topics: []string{"reforma"}, Published: date(2026, 6, 20).Add(1)})
	seed(t, repo, seedSpec{Title: "Diecinueve", Author: "lara-arianna", Section: "politics", Topics: []string{"reforma"}, Published: date(2026, 6, 19)})
	seed(t, repo, seedSpec{Title: "Dieciocho", Author: "lara-arianna", Section: "politics", Topics: []string{"reforma"}, Published: date(2026, 6, 18)})
	genInto(t, repo, out, nil)

	sep19 := sepLI("19 de junio de 2026")
	sep18 := sepLI("18 de junio de 2026")

	scopes := []string{
		"latest/index.html",
		"section/politics/index.html",
		"author/lara-arianna/index.html",
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
