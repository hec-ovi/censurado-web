package generate

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hec-ovi/censurado-web-backend/domain"
	"github.com/hec-ovi/censurado-web-backend/store"
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

	// Author profile fields stamped on articles by a sibling system, keyed by
	// author slug and recorded from the first (earliest-inserted) carrier, the
	// same representative the labels use.
	authorBio    map[string]string
	authorName   map[string]string
	authorAvatar map[string]string
	// authorSection is the author's beat: the section slug of their first
	// (earliest-inserted) article, used to theme the Nosotros roster card.
	authorSection map[string]string
	// authorGender is the author's gender label, overlaid from the author registry
	// (Author.Metadata["gender"]); shown on the profile page. Empty == unspecified.
	authorGender map[string]string
	// authorProfileTopics is the operator/agent-curated topic list for an author's
	// public profile, overlaid from the author registry (Author.Metadata). When set for
	// a slug it REPLACES the uncapped union of every topic the author ever tagged; when
	// absent the union (capped) is used. Empty is never stored, so presence == curated.
	authorProfileTopics map[string][]string
	// authorDeleted marks author slugs whose registry row is tombstoned (soft-deleted).
	// A deleted author is dropped from the Nosotros roster (authorCards) but keeps their
	// articles, their /author/<slug>/ page, and their bylines, so no link ever dangles.
	// Populated from ListAuthors(includeDeleted=true); empty until an author is actually
	// deleted, so output stays byte-identical for any store with no tombstones.
	authorDeleted map[string]bool

	// portadaOrd is each article's within-day render position and portadaRole its
	// curated role ("" or "important"), both populated only for days a per-day plan
	// (PortadaStore) curates; the default 0/"" reproduces the newest-published-first
	// order so uncurated output stays byte-identical.
	portadaOrd  map[string]int
	portadaRole map[string]string
	// portadaDays records days with an explicit curation plan. The paginator uses
	// it to keep the newest curated day together on the mutable front-page landing.
	portadaDays map[string]bool
	// recomendado is the site's single GLOBAL editor's-pick slug list (RecomendadoStore),
	// day-independent and persistent, rendered as the front-page "Recomendado" rail in
	// stored order. Empty until an operator sets one.
	recomendado []string
}

func newIndex(n int) *Index {
	return &Index{
		All:                 make([]domain.Article, 0, n),
		sections:            map[string][]int{},
		authors:             map[string][]int{},
		topics:              map[string][]int{},
		months:              map[ym][]int{},
		secAuthors:          map[string]map[string]struct{}{},
		secTopics:           map[string]map[string]struct{}{},
		secMonths:           map[string]map[ym]struct{}{},
		sectionLabel:        map[string]string{},
		authorLabel:         map[string]string{},
		topicLabel:          map[string]string{},
		authorBio:           map[string]string{},
		authorName:          map[string]string{},
		authorAvatar:        map[string]string{},
		authorSection:       map[string]string{},
		authorGender:        map[string]string{},
		authorProfileTopics: map[string][]string{},
		authorDeleted:       map[string]bool{},
		portadaOrd:          map[string]int{},
		portadaRole:         map[string]string{},
		portadaDays:         map[string]bool{},
	}
}

// BuildIndex reads the whole corpus, truncates every PublishedAt/CreatedAt to
// whole seconds for deterministic bytes, and sorts into canonical
// insertion order (CreatedAt ASC, id ASC) which is the append-only seal axis.
func BuildIndex(ctx context.Context, repo store.Repository) (*Index, error) {
	raw, err := repo.Find(ctx, store.Filter{Order: store.OldestFirst})
	if err != nil {
		return nil, err
	}
	for i := range raw {
		raw[i].PublishedAt = raw[i].PublishedAt.UTC().Truncate(time.Second)
		raw[i].CreatedAt = raw[i].CreatedAt.UTC().Truncate(time.Second)
		raw[i].Section = canonicalSectionSlug(raw[i].Section)
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
			// Prefer the sibling system's metadata.author_name for the display
			// label, falling back to the raw author string. recordLabel keeps the
			// earliest-inserted carrier's value, matching the section/topic labels.
			name := firstMetadataString(a.Metadata, "author_name")
			label := a.Author
			if name != "" {
				label = name
			}
			recordLabel(idx.authorLabel, aSlug, label)
			if name != "" {
				recordLabel(idx.authorName, aSlug, name)
			}
			if bio := firstMetadataString(a.Metadata, "author_bio"); bio != "" {
				recordLabel(idx.authorBio, aSlug, bio)
			}
			if avatar := firstMetadataString(a.Metadata, "author_avatar"); avatar != "" {
				recordLabel(idx.authorAvatar, aSlug, avatar)
			}
			if sOK {
				recordLabel(idx.authorSection, aSlug, sSlug)
			}
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
	if err := overlayRegistry(ctx, idx, repo); err != nil {
		return nil, err
	}
	if err := overlayPortada(ctx, idx, repo); err != nil {
		return nil, err
	}
	if err := overlayRecomendado(ctx, idx, repo); err != nil {
		return nil, err
	}
	return idx, nil
}

// overlayRecomendado loads the site's single GLOBAL "Recomendado" list
// (RecomendadoStore) into the index for the front-page rail. It is day-independent:
// the same list renders on the front page every day until an operator replaces it,
// unlike the per-day portada order. Purely additive: a store without the registry,
// or one with no list set, leaves idx.recomendado empty and the front-page rail
// renders as an empty widget (the render layer still shows it so the layout holds).
func overlayRecomendado(ctx context.Context, idx *Index, repo store.Repository) error {
	rs, ok := repo.(store.RecomendadoStore)
	if !ok {
		return nil
	}
	slugs, err := rs.GetRecomendado(ctx)
	if err != nil {
		return err
	}
	rec := make([]string, 0, len(slugs))
	for _, s := range slugs {
		if s = strings.TrimSpace(s); s != "" {
			rec = append(rec, s)
		}
	}
	idx.recomendado = rec
	return nil
}

// dayKey is an article's published-day bucket in UTC ("2006-01-02"), the same
// grouping the day separators and the client use.
func dayKey(a domain.Article) string {
	return a.PublishedAt.UTC().Format("2006-01-02")
}

// portadaLess orders articles for display honoring the curated per-day plan: a
// newer published DAY first, and within a day by the stored ord. The default ord
// is 0 for every article, so an uncurated day falls straight through to displayLess
// (newest-published-first) and the output is byte-identical to the pre-curation
// build; only a day with a plan carries distinct ords and gets reordered.
func (idx *Index) portadaLess(a, b domain.Article) bool {
	if dayKey(a) != dayKey(b) {
		return displayLess(a, b)
	}
	oa, ob := idx.portadaOrd[a.ID], idx.portadaOrd[b.ID]
	if oa != ob {
		return oa < ob
	}
	return displayLess(a, b)
}

func (idx *Index) portadaSort(arts []domain.Article) {
	sort.SliceStable(arts, func(i, j int) bool { return idx.portadaLess(arts[i], arts[j]) })
}

// overlayPortada applies operator/agent-curated per-day plans (PortadaStore) over
// the default order. Each plan, keyed by published day, lists article slugs in the
// wanted order with an optional role: the listed articles take positions 0..k-1
// (position 0 is the day's lead), any same-day article absent from the plan follows
// in default display order, and role "important" marks a full-row card. (The
// front-page "Recomendado" rail is a separate GLOBAL list; see overlayRecomendado.)
// Purely additive: a store
// without the registry, or a corpus with no plans, leaves every ord at its default
// 0, so the generator (and every sealed shard) is byte-identical until a plan is
// written; a plan slug with no article published that day is ignored, so a plan
// never manufactures a card. Only a curated day carries non-zero ords, so growth on
// an uncurated day never shifts a sealed article's position.
func overlayPortada(ctx context.Context, idx *Index, repo store.Repository) error {
	ps, ok := repo.(store.PortadaStore)
	if !ok {
		return nil
	}
	plans, err := ps.ListPortadas(ctx, false)
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		return nil
	}
	// day -> that day's All-indices in default display order (newest-first), the
	// working set a plan reorders. Built only when at least one plan exists.
	byDay := map[string][]int{}
	for i := range idx.All {
		d := dayKey(idx.All[i])
		byDay[d] = append(byDay[d], i)
	}
	for d := range byDay {
		g := byDay[d]
		sort.SliceStable(g, func(x, y int) bool { return displayLess(idx.All[g[x]], idx.All[g[y]]) })
	}
	for _, plan := range plans {
		dayIdxs, has := byDay[plan.Date]
		if !has {
			continue // a plan for a day with no published articles: nothing to order
		}
		idx.portadaDays[plan.Date] = true
		bySlug := map[string]int{}
		for _, ix := range dayIdxs {
			bySlug[idx.All[ix].Slug] = ix
		}
		used := map[string]struct{}{}
		pos := 0
		for _, e := range plan.Entries {
			ix, live := bySlug[e.Slug]
			if !live {
				continue // slug not published that day: skip (no dead card)
			}
			if _, dup := used[e.Slug]; dup {
				continue
			}
			used[e.Slug] = struct{}{}
			id := idx.All[ix].ID
			idx.portadaOrd[id] = pos
			if e.Role == "important" {
				idx.portadaRole[id] = "important"
			} else {
				idx.portadaRole[id] = ""
			}
			pos++
		}
		// Same-day articles not named in the plan keep their relative display order
		// (dayIdxs is display-ordered), appended after the curated ones.
		for _, ix := range dayIdxs {
			a := idx.All[ix]
			if _, done := used[a.Slug]; done {
				continue
			}
			idx.portadaOrd[a.ID] = pos
			idx.portadaRole[a.ID] = ""
			pos++
		}
	}
	return nil
}

// overlayRegistry prefers the operator-managed author/topic registry over the
// per-article metadata for the public profile fields (author name/bio/avatar, topic
// label). It is purely additive: a store that does not implement the registries, or
// empty registries, leaves the metadata-derived values exactly as they were, so the
// generator behaves identically until the tables are populated. A table value
// overlays only when non-empty, so a half-filled registry row never blanks a good
// metadata-derived value; and only slugs that already have published articles are
// touched, so a registry entry never manufactures a roster card or a heading for an
// author/topic with no articles.
func overlayRegistry(ctx context.Context, idx *Index, repo store.Repository) error {
	if as, ok := repo.(store.AuthorStore); ok {
		// includeDeleted=true so tombstoned authors are visible here and can be
		// recorded in authorDeleted (to drop them from the roster). This is a no-op
		// for a store with no tombstones: the same rows come back, the a.Deleted
		// branch never fires, and every artifact stays byte-identical.
		authors, err := as.ListAuthors(ctx, true)
		if err != nil {
			return err
		}
		for _, a := range authors {
			slug, ok := facetSlug(a.Handle)
			if !ok {
				continue
			}
			if a.Deleted {
				// Tombstoned: record the slug so authorCards drops it from the Nosotros
				// roster, then skip. No profile overlay and no empty-set injection,
				// exactly as when ListAuthors(false) excluded it. The article-derived
				// scope, the /author/<slug>/ page, and every byline stay intact, so no
				// link dangles.
				idx.authorDeleted[slug] = true
				continue
			}
			if _, has := idx.authors[slug]; !has {
				// A registered author with no published articles yet is still a real
				// site author: inject an empty article set so the slug flows through
				// Scopes(), its /author/ page, and the Nosotros roster (the whole
				// pipeline is keyed on idx.authors). Require a display name, so a bare
				// handle never manufactures a blank card. Authors that already have
				// articles are untouched: this branch only fires for a slug absent from
				// the article-derived map.
				if a.Name == "" {
					continue
				}
				idx.authors[slug] = []int{}
				// No article to theme the roster card from; take the section from the
				// registry's beat metadata when present, so the card is still colored.
				if beat, _ := a.Metadata["beat"].(string); beat != "" {
					if bslug, okb := facetSlug(beat); okb {
						idx.authorSection[slug] = bslug
					}
				}
			}
			if a.Name != "" {
				idx.authorName[slug] = a.Name
				idx.authorLabel[slug] = a.Name
			}
			if a.Bio != "" {
				idx.authorBio[slug] = a.Bio
			}
			if a.Avatar != "" {
				idx.authorAvatar[slug] = a.Avatar
			}
			// Curated public-profile topics ride in the registry's Metadata blob
			// (the brain pushes them there). Overlay only when non-empty, so an
			// uncurated author keeps the computed union.
			if topics := metaStringSlice(a.Metadata["profile_topics"]); len(topics) > 0 {
				idx.authorProfileTopics[slug] = topics
			}
			if g, _ := a.Metadata["gender"].(string); g != "" {
				idx.authorGender[slug] = g
			}
		}
	}
	if ts, ok := repo.(store.TopicStore); ok {
		topics, err := ts.ListTopics(ctx, false)
		if err != nil {
			return err
		}
		for _, t := range topics {
			slug, ok := facetSlug(t.Slug)
			if !ok {
				continue
			}
			if _, has := idx.topics[slug]; !has {
				continue
			}
			if t.Label != "" {
				idx.topicLabel[slug] = t.Label
			}
		}
	}
	return nil
}

// metaStringSlice coerces a Metadata value into a clean []string. It accepts the
// []any a JSON array decodes to (the store's path) and a native []string, keeping only
// non-blank trimmed strings in order. Anything else (a scalar, a nested object, nil)
// yields nil, so a malformed blob degrades to "no curated topics" rather than an error.
func metaStringSlice(v any) []string {
	var out []string
	switch xs := v.(type) {
	case []string:
		for _, s := range xs {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case []any:
		for _, e := range xs {
			if s, ok := e.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// idLess compares decimal id strings numerically; the store uses integer PKs.
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
