package generate

import (
	"sort"

	"github.com/hec-ovi/censurado-web-backend/domain"
)

// Page is one Tier-A listing page. Full pages have Number 1.. (page 1 the oldest) and
// hold exactly P articles; the landing (Number 0) holds the newest-published remainder.
type Page struct {
	Scope     Scope
	Number    int
	Landing   bool
	Articles  []domain.Article // display order (newest-published-first)
	Canonical string           // self URL, except a rem==0 landing -> page/<full>/
	Prev      string
	Next      string
}

// displayLess orders for display: newest published first, ties by newest id.
// HTML listings and shard parts both sort with this comparator (entryLess
// mirrors it for ShardEntry), so listing order and shard order match.
func displayLess(a, b domain.Article) bool {
	if !a.PublishedAt.Equal(b.PublishedAt) {
		return a.PublishedAt.After(b.PublishedAt)
	}
	return idLess(b.ID, a.ID) // larger id first
}

// chunkPages paginates a scope by PUBLICATION date: the portada (landing) leads with
// the newest-published article and pages descend to older ones, so the static pages
// match the client's published-DESC infinite scroll. indices is sorted oldest-
// published-first, then cut into pages of P; n=len, full=n/P, rem=n%P. Page k holds
// indices[(k-1)*P:k*P] (the lower the number, the older); the landing holds the rem
// newest-published. A back-dated insert therefore re-cuts the affected pages, by
// design; shards stay insertion-sealed for the client merge.
func (idx *Index) chunkPages(s Scope, P int) []Page {
	indices := append([]int(nil), idx.scopeIndices(s)...) // copy: we re-sort locally
	sort.SliceStable(indices, func(a, b int) bool {
		ia, ib := idx.All[indices[a]], idx.All[indices[b]]
		if !ia.PublishedAt.Equal(ib.PublishedAt) {
			return ia.PublishedAt.Before(ib.PublishedAt) // oldest published first
		}
		return idLess(ia.ID, ib.ID) // stable tie-break
	})
	if isLatest(s) && idx.hasCuratedDay(indices) {
		return idx.chunkLatestByDay(s, P, indices)
	}
	n := len(indices)
	full := n / P

	// A curated newest day is the front page's editorial unit. Keep every card
	// from that day on the mutable landing, even when the day crosses the fixed
	// page-size boundary. Without this, a plan can name a lead that has already
	// been sealed onto an older page before portadaSort gets a chance to run.
	if n > 0 {
		newestDay := dayKey(idx.All[indices[n-1]])
		if idx.portadaDays[newestDay] {
			firstNewestDay := n - 1
			for firstNewestDay > 0 && dayKey(idx.All[indices[firstNewestDay-1]]) == newestDay {
				firstNewestDay--
			}
			full = firstNewestDay / P
		}
	}

	var pages []Page
	for k := 1; k <= full; k++ {
		arts := idx.articlesAt(indices[(k-1)*P : k*P])
		idx.portadaSort(arts)
		pg := Page{
			Scope:     s,
			Number:    k,
			Articles:  arts,
			Canonical: s.PageURL(k),
		}
		if k >= 2 {
			pg.Prev = s.PageURL(k - 1)
		}
		if k < full {
			pg.Next = s.PageURL(k + 1)
		} else {
			pg.Next = s.PageURL(0)
		}
		pages = append(pages, pg)
	}

	// The landing (Number 0) holds the newest insertion remainder. On an exact
	// page boundary (rem==0) that remainder is empty, which would render /latest/
	// blank; instead the landing mirrors the newest sealed page (its canonical
	// already points there), so the portada is never empty. Sealed pages are not
	// touched, so their byte-stability holds.
	landIdx := indices[full*P : n]
	landing := Page{
		Scope:     s,
		Number:    0,
		Landing:   true,
		Canonical: s.PageURL(0),
	}
	if n%P == 0 && full >= 1 {
		landIdx = indices[(full-1)*P : full*P]
		landing.Canonical = s.PageURL(full)
		if full >= 2 {
			landing.Prev = s.PageURL(full - 1)
		}
	} else if full >= 1 {
		landing.Prev = s.PageURL(full)
	}
	arts := idx.articlesAt(landIdx)
	idx.portadaSort(arts)
	landing.Articles = arts
	pages = append(pages, landing)
	return pages
}

// hasCuratedDay reports whether a scope contains an explicit per-day portada
// plan. The front page must treat those days as indivisible layout units: the
// lead, paired half-width cards, and full-row important cards only make sense
// when pagination does not cut the day in half.
func (idx *Index) hasCuratedDay(indices []int) bool {
	for _, i := range indices {
		if idx.portadaDays[dayKey(idx.All[i])] {
			return true
		}
	}
	return false
}

// chunkLatestByDay paginates the front page on whole UTC publication days once
// any day has an explicit portada plan. PageSize becomes a soft target: adjacent
// days are packed oldest-first without ever splitting a day, and the newest
// landing absorbs the preceding whole-day page when it would otherwise contain
// fewer than P articles. This keeps the landing a canonical prefix of the shard
// stream and preserves each day's lead/pair/important layout across day changes.
func (idx *Index) chunkLatestByDay(s Scope, P int, indices []int) []Page {
	if len(indices) == 0 {
		return []Page{{Scope: s, Number: 0, Landing: true, Canonical: s.PageURL(0)}}
	}

	var chunks [][]int
	current := make([]int, 0, P)
	flush := func() {
		if len(current) == 0 {
			return
		}
		chunks = append(chunks, current)
		current = make([]int, 0, P)
	}
	for start := 0; start < len(indices); {
		end := start + 1
		day := dayKey(idx.All[indices[start]])
		for end < len(indices) && dayKey(idx.All[indices[end]]) == day {
			end++
		}
		group := indices[start:end]
		if len(current) > 0 && len(current)+len(group) > P {
			flush()
		}
		current = append(current, group...)
		if len(current) >= P {
			flush()
		}
		start = end
	}
	flush()

	// A short newest day would otherwise leave the front page sparse. Pull the
	// preceding complete chunk into the mutable landing; because chunks end only
	// at day boundaries, this cannot manufacture a false lead or orphan a pair.
	last := len(chunks) - 1
	if last > 0 && len(chunks[last]) < P {
		merged := make([]int, 0, len(chunks[last-1])+len(chunks[last]))
		merged = append(merged, chunks[last-1]...)
		merged = append(merged, chunks[last]...)
		chunks[last-1] = merged
		chunks = chunks[:last]
	}

	sealed := len(chunks) - 1
	pages := make([]Page, 0, len(chunks))
	for i := 0; i < sealed; i++ {
		arts := idx.articlesAt(chunks[i])
		idx.portadaSort(arts)
		pg := Page{
			Scope:     s,
			Number:    i + 1,
			Articles:  arts,
			Canonical: s.PageURL(i + 1),
		}
		if i > 0 {
			pg.Prev = s.PageURL(i)
		}
		if i+1 < sealed {
			pg.Next = s.PageURL(i + 2)
		} else {
			pg.Next = s.PageURL(0)
		}
		pages = append(pages, pg)
	}

	arts := idx.articlesAt(chunks[len(chunks)-1])
	idx.portadaSort(arts)
	landing := Page{
		Scope:     s,
		Number:    0,
		Landing:   true,
		Articles:  arts,
		Canonical: s.PageURL(0),
	}
	if sealed > 0 {
		landing.Prev = s.PageURL(sealed)
	}
	pages = append(pages, landing)
	return pages
}

func sortDisplay(arts []domain.Article) {
	sort.SliceStable(arts, func(i, j int) bool { return displayLess(arts[i], arts[j]) })
}
