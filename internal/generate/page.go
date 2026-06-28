package generate

import (
	"sort"

	"github.com/hec-ovi/censurado-web-backend/domain"
)

// Page is one Tier-A listing page. Sealed full pages have Number 1.. and hold
// exactly P articles; the landing remainder has Number 0 and is mutable.
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

// chunkPages seals boundaries on insertion order and renders each page in
// display order. indices is insertion-ordered; n=len, full=n/P, rem=n%P. Sealed
// page k holds indices[(k-1)*P:k*P]; the landing holds the rem newest-inserted.
func (idx *Index) chunkPages(s Scope, P int) []Page {
	indices := idx.scopeIndices(s)
	n := len(indices)
	full := n / P

	var pages []Page
	for k := 1; k <= full; k++ {
		arts := idx.articlesAt(indices[(k-1)*P : k*P])
		sortDisplay(arts)
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
	sortDisplay(arts)
	landing.Articles = arts
	pages = append(pages, landing)
	return pages
}

func sortDisplay(arts []domain.Article) {
	sort.SliceStable(arts, func(i, j int) bool { return displayLess(arts[i], arts[j]) })
}
