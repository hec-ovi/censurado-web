package generate

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/store"
)

// ym is a published-month bucket key.
type ym struct {
	Year, Month int
}

// Index is the single archive scan: the whole corpus in canonical insertion
// order plus the facet membership maps. Each membership slice is a subsequence
// of All's insertion order, so it is itself insertion-ordered.
type Index struct {
	All        []domain.Article
	sections   map[string][]int
	authors    map[string][]int
	topics     map[string][]int
	months     map[ym][]int
	secAuthors map[string]map[string]struct{}
	secTopics  map[string]map[string]struct{}
	secMonths  map[string]map[ym]struct{}

	sectionLabel map[string]string
	authorLabel  map[string]string
	topicLabel   map[string]string
}

func newIndex(n int) *Index {
	return &Index{
		All:          make([]domain.Article, 0, n),
		sections:     map[string][]int{},
		authors:      map[string][]int{},
		topics:       map[string][]int{},
		months:       map[ym][]int{},
		secAuthors:   map[string]map[string]struct{}{},
		secTopics:    map[string]map[string]struct{}{},
		secMonths:    map[string]map[ym]struct{}{},
		sectionLabel: map[string]string{},
		authorLabel:  map[string]string{},
		topicLabel:   map[string]string{},
	}
}

// BuildIndex reads the whole corpus, truncates every PublishedAt/CreatedAt to
// whole seconds for adapter-independent bytes, and sorts into canonical
// insertion order (CreatedAt ASC, id ASC) which is the append-only seal axis.
func BuildIndex(ctx context.Context, repo store.Repository) (*Index, error) {
	raw, err := repo.Find(ctx, store.Filter{Order: store.OldestFirst})
	if err != nil {
		return nil, err
	}
	for i := range raw {
		raw[i].PublishedAt = raw[i].PublishedAt.UTC().Truncate(time.Second)
		raw[i].CreatedAt = raw[i].CreatedAt.UTC().Truncate(time.Second)
	}
	sort.SliceStable(raw, func(a, b int) bool {
		if !raw[a].CreatedAt.Equal(raw[b].CreatedAt) {
			return raw[a].CreatedAt.Before(raw[b].CreatedAt)
		}
		return idLess(raw[a].ID, raw[b].ID)
	})

	idx := newIndex(len(raw))
	idx.All = raw
	for i, a := range raw {
		sSlug, sOK := facetSlug(a.Section)
		aSlug, aOK := facetSlug(a.Author)
		m := ym{a.PublishedAt.Year(), int(a.PublishedAt.Month())}
		if sOK {
			idx.sections[sSlug] = append(idx.sections[sSlug], i)
			recordLabel(idx.sectionLabel, sSlug, a.Section)
		}
		if aOK {
			idx.authors[aSlug] = append(idx.authors[aSlug], i)
			recordLabel(idx.authorLabel, aSlug, a.Author)
		}
		idx.months[m] = append(idx.months[m], i)

		seen := map[string]struct{}{}
		for _, t := range a.Topics {
			tSlug, ok := facetSlug(t)
			if !ok {
				continue
			}
			if _, dup := seen[tSlug]; dup {
				continue
			}
			seen[tSlug] = struct{}{}
			idx.topics[tSlug] = append(idx.topics[tSlug], i)
			recordLabel(idx.topicLabel, tSlug, t)
		}
		if sOK {
			if aOK {
				addKey(idx.secAuthors, sSlug, aSlug)
			}
			for tSlug := range seen {
				addKey(idx.secTopics, sSlug, tSlug)
			}
			addYM(idx.secMonths, sSlug, m)
		}
	}
	return idx, nil
}

// idLess compares decimal id strings numerically; both adapters use integer PKs.
// It falls back to a lexical compare when an id is unparseable (defensive only).
func idLess(a, b string) bool {
	ai, aerr := strconv.ParseInt(a, 10, 64)
	bi, berr := strconv.ParseInt(b, 10, 64)
	if aerr == nil && berr == nil {
		return ai < bi
	}
	return a < b
}

// recordLabel records the slug's original casing once. Because All is
// insertion-ordered, the representative is the earliest-inserted carrier.
func recordLabel(m map[string]string, slug, raw string) {
	if _, ok := m[slug]; !ok {
		m[slug] = raw
	}
}

func addKey(m map[string]map[string]struct{}, k, v string) {
	if m[k] == nil {
		m[k] = map[string]struct{}{}
	}
	m[k][v] = struct{}{}
}

func addYM(m map[string]map[ym]struct{}, k string, v ym) {
	if m[k] == nil {
		m[k] = map[ym]struct{}{}
	}
	m[k][v] = struct{}{}
}

// Scopes returns every canonical scope with at least one article, in a fully
// deterministic order: Latest, then sections, authors, topics, months, then the
// section-anchored matrix scopes.
func (idx *Index) Scopes() []Scope {
	var out []Scope
	if len(idx.All) == 0 {
		return out
	}
	out = append(out, Scope{}) // Latest
	for _, s := range sortedStringKeys(idx.sections) {
		out = append(out, Scope{Section: idx.sectionFacet(s)})
	}
	for _, a := range sortedStringKeys(idx.authors) {
		out = append(out, Scope{Section: idx.authorFacet(a)})
	}
	for _, t := range sortedStringKeys(idx.topics) {
		out = append(out, Scope{Section: idx.topicFacet(t)})
	}
	for _, m := range sortedMonths(monthKeys(idx.months)) {
		out = append(out, Scope{Section: monthFacet(m)})
	}
	for _, s := range sortedStringKeys(idx.sections) {
		sf := idx.sectionFacet(s)
		for _, a := range sortedSetKeys(idx.secAuthors[s]) {
			out = append(out, Scope{Section: sf, Sub: idx.authorFacet(a)})
		}
		for _, t := range sortedSetKeys(idx.secTopics[s]) {
			out = append(out, Scope{Section: sf, Sub: idx.topicFacet(t)})
		}
		for _, m := range sortedMonths(setToMonths(idx.secMonths[s])) {
			out = append(out, Scope{Section: sf, Sub: monthFacet(m)})
		}
	}
	return out
}

func (idx *Index) sectionFacet(s string) Facet {
	return Facet{Kind: AxisSection, Value: s, Label: idx.sectionLabel[s]}
}
func (idx *Index) authorFacet(a string) Facet {
	return Facet{Kind: AxisAuthor, Value: a, Label: idx.authorLabel[a]}
}
func (idx *Index) topicFacet(t string) Facet {
	return Facet{Kind: AxisTopic, Value: t, Label: idx.topicLabel[t]}
}
func monthFacet(m ym) Facet {
	return Facet{Kind: AxisMonth, Year: m.Year, Month: m.Month}
}

// scopeIndices returns the scope's All-indices in canonical insertion order.
func (idx *Index) scopeIndices(s Scope) []int {
	if s.isSingle() {
		switch s.Section.Kind {
		case AxisLatest:
			out := make([]int, len(idx.All))
			for i := range out {
				out[i] = i
			}
			return out
		case AxisSection:
			return idx.sections[s.Section.Value]
		case AxisAuthor:
			return idx.authors[s.Section.Value]
		case AxisTopic:
			return idx.topics[s.Section.Value]
		case AxisMonth:
			return idx.months[ym{s.Section.Year, s.Section.Month}]
		}
	}
	sec := idx.sections[s.Section.Value]
	var sub []int
	switch s.Sub.Kind {
	case AxisAuthor:
		sub = idx.authors[s.Sub.Value]
	case AxisTopic:
		sub = idx.topics[s.Sub.Value]
	case AxisMonth:
		sub = idx.months[ym{s.Sub.Year, s.Sub.Month}]
	}
	return mergeIntersect(sec, sub)
}

// mergeIntersect intersects two ascending (insertion-ordered) index slices via a
// two-pointer merge, preserving insertion order.
func mergeIntersect(a, b []int) []int {
	var out []int
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// articlesAt copies the articles at the given All-indices.
func (idx *Index) articlesAt(indices []int) []domain.Article {
	out := make([]domain.Article, 0, len(indices))
	for _, i := range indices {
		out = append(out, idx.All[i])
	}
	return out
}

func sortedStringKeys(m map[string][]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSetKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func monthKeys(m map[ym][]int) []ym {
	out := make([]ym, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func setToMonths(m map[ym]struct{}) []ym {
	out := make([]ym, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// sortedMonths returns a copy sorted ascending (oldest first), the canonical
// enumeration order.
func sortedMonths(ms []ym) []ym {
	out := append([]ym{}, ms...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year < out[j].Year
		}
		return out[i].Month < out[j].Month
	})
	return out
}

// monthsNewestFirst returns a copy sorted descending (newest first), the shard
// and manifest order.
func monthsNewestFirst(ms []ym) []ym {
	out := append([]ym{}, ms...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year > out[j].Year
		}
		return out[i].Month > out[j].Month
	})
	return out
}

// Plan is the full artifact-independent build state: the index plus every page
// across every scope (scope order, then page/1..full, then the landing).
type Plan struct {
	Index *Index
	Pages []Page
}

func BuildPlan(ctx context.Context, repo store.Repository, pageSize int) (*Plan, error) {
	idx, err := BuildIndex(ctx, repo)
	if err != nil {
		return nil, err
	}
	p := &Plan{Index: idx}
	for _, s := range idx.Scopes() {
		p.Pages = append(p.Pages, idx.chunkPages(s, pageSize)...)
	}
	return p, nil
}

func (p *Plan) TierAURLCount() int { return len(p.Pages) }
