package generate

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templateFuncs = template.FuncMap{
	"rfc3339":   func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"humandate": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
}

// Two parsed sets share base.tmpl; each pairs it with one content template that
// defines "main". A single combined set is impossible because listing.tmpl and
// article.tmpl both define "main".
var (
	listingTmpl = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.tmpl", "templates/listing.tmpl"))
	articleTmpl = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.tmpl", "templates/article.tmpl"))
)

type pageView struct {
	Title     string
	Canonical string
	PrevURL   string
	NextURL   string
	IsLanding bool
	FeedLinks []feedLink
	Heading   string
	Items     []itemView
	Pager     pagerView
	Months    []monthLink
	Manifest  template.HTML
}

type itemView struct {
	Title       string
	URL         string
	AuthorLabel string
	AuthorURL   string
	Section     string
	SectionURL  string
	Topics      []topicLink
	PublishedAt time.Time
}

type topicLink struct{ Label, URL string }
type monthLink struct {
	Label, URL  string
	Year, Month int
}
type pagerView struct {
	Pages   []pageLink
	Current int
}
type pageLink struct {
	N   int
	URL string
}
type feedLink struct{ Rel, Type, Href, Title string }

type articleView struct {
	Title       string
	Canonical   string
	AuthorLabel string
	AuthorURL   string
	Section     string
	SectionURL  string
	Topics      []topicLink
	PublishedAt time.Time
	BodyHTML    template.HTML
}

// renderListing renders one Tier-A listing page.
//
// Append-only immutability is load-bearing: a sealed page must reproduce
// byte-identically forever, even as the scope gains articles, months, shard
// parts, or new sealed pages (see §6.5 and tests B1/E5/G2). So everything that
// depends on the live tail (the embedded manifest, the pager over all pages,
// the month navigator, the /latest/ feed alternates, and the forward Next
// pointer that flips to the landing when a higher page seals) is rendered ONLY
// on the mutable landing page. A sealed page renders solely from its own
// number, scope, and immutable article slice, plus a stable Prev link to the
// older page (PageURL(N-1), a function of N alone). The client-side Tier-B
// refiner reads the manifest from the landing, which is the scope entry point.
func renderListing(env *buildEnv, pg Page, manifest template.HTML) ([]byte, error) {
	sc := pg.Scope
	heading := scopeHeading(sc)
	view := pageView{
		Title:     pageTitle(heading, env.siteName, pg),
		Canonical: absolute(env.siteBase, pg.Canonical),
		IsLanding: pg.Landing,
		Heading:   heading,
	}
	// Prev points to the older page (PageURL(N-1) for sealed N, PageURL(full)
	// for the landing): stable for sealed pages because it depends only on N.
	if pg.Prev != "" {
		view.PrevURL = absolute(env.siteBase, pg.Prev)
	}
	for _, a := range pg.Articles {
		view.Items = append(view.Items, itemViewOf(a))
	}
	if pg.Landing {
		view.Manifest = manifest
		view.Pager = pagerFor(env.plan.Index, sc, env.pageSize, pg.Number)
		view.Months = monthLinksFor(env.plan.Index, sc)
		if pg.Next != "" {
			view.NextURL = absolute(env.siteBase, pg.Next)
		}
		if isLatest(sc) {
			view.FeedLinks = feedLinksFor(env.siteBase, env.siteName)
		}
	}

	var buf bytes.Buffer
	if err := listingTmpl.ExecuteTemplate(&buf, "base", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderArticle renders one permalink page.
func renderArticle(env *buildEnv, a domain.Article) ([]byte, error) {
	view := articleView{
		Title:       a.Title,
		Canonical:   absolute(env.siteBase, articleURL(a)),
		AuthorLabel: a.Author,
		AuthorURL:   facetURL("author", a.Author),
		Section:     a.Section,
		SectionURL:  facetURL("section", a.Section),
		Topics:      topicLinksOf(a),
		PublishedAt: a.PublishedAt,
		BodyHTML:    template.HTML(env.bodyHTML[a.ContentHash]),
	}
	var buf bytes.Buffer
	if err := articleTmpl.ExecuteTemplate(&buf, "base", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func itemViewOf(a domain.Article) itemView {
	return itemView{
		Title:       a.Title,
		URL:         articleURL(a),
		AuthorLabel: a.Author,
		AuthorURL:   facetURL("author", a.Author),
		Section:     a.Section,
		SectionURL:  facetURL("section", a.Section),
		Topics:      topicLinksOf(a),
		PublishedAt: a.PublishedAt,
	}
}

func topicLinksOf(a domain.Article) []topicLink {
	var out []topicLink
	seen := map[string]struct{}{}
	for _, t := range a.Topics {
		slug, ok := facetSlug(t)
		if !ok {
			continue
		}
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		out = append(out, topicLink{Label: t, URL: pageURL("topic/"+slug, 0)})
	}
	return out
}

// facetURL returns a single-facet scope URL, or "" when the value slugifies to
// empty (no /section// link is ever produced).
func facetURL(kind, raw string) string {
	slug, ok := facetSlug(raw)
	if !ok {
		return ""
	}
	return pageURL(kind+"/"+slug, 0)
}

func isLatest(s Scope) bool {
	return s.Section.Kind == AxisLatest && s.isSingle()
}

func scopeHeading(s Scope) string {
	if s.isSingle() {
		switch s.Section.Kind {
		case AxisLatest:
			return "Latest"
		case AxisSection, AxisAuthor, AxisTopic:
			return s.Section.labelOr()
		case AxisMonth:
			return monthKey(s.Section.Year, s.Section.Month)
		}
	}
	sub := ""
	switch s.Sub.Kind {
	case AxisAuthor, AxisTopic:
		sub = s.Sub.labelOr()
	case AxisMonth:
		sub = monthKey(s.Sub.Year, s.Sub.Month)
	}
	return s.Section.labelOr() + " / " + sub
}

func pageTitle(heading, siteName string, pg Page) string {
	if pg.Number >= 1 {
		return fmt.Sprintf("%s (page %d) | %s", heading, pg.Number, siteName)
	}
	return heading + " | " + siteName
}

func feedLinksFor(siteBase, siteName string) []feedLink {
	return []feedLink{
		{Rel: "alternate", Type: "application/rss+xml", Href: absolute(siteBase, "/feed.xml"), Title: siteName + " RSS"},
		{Rel: "alternate", Type: "application/atom+xml", Href: absolute(siteBase, "/atom.xml"), Title: siteName + " Atom"},
		{Rel: "alternate", Type: "application/feed+json", Href: absolute(siteBase, "/feed.json"), Title: siteName + " JSON Feed"},
	}
}

// pagerFor lists the scope's existing pages: sealed pages 1..full then the
// landing (N==0).
func pagerFor(idx *Index, s Scope, pageSize, current int) pagerView {
	n := len(idx.scopeIndices(s))
	full := n / pageSize
	var pages []pageLink
	for k := 1; k <= full; k++ {
		pages = append(pages, pageLink{N: k, URL: s.PageURL(k)})
	}
	pages = append(pages, pageLink{N: 0, URL: s.PageURL(0)})
	return pagerView{Pages: pages, Current: current}
}

// monthLinksFor returns the month navigator: Latest links to /Y/MM/ months;
// a single section links to /section/S/Y/MM/ months. Other scopes get none, so
// no navigator links a non-existent scope.
func monthLinksFor(idx *Index, s Scope) []monthLink {
	if isLatest(s) {
		return monthLinksFromYM(monthsNewestFirst(monthKeys(idx.months)), "")
	}
	if s.isSingle() && s.Section.Kind == AxisSection {
		months := monthsNewestFirst(setToMonths(idx.secMonths[s.Section.Value]))
		return monthLinksFromYM(months, "section/"+s.Section.Value)
	}
	return nil
}

func monthLinksFromYM(months []ym, base string) []monthLink {
	var out []monthLink
	for _, m := range months {
		scope := fmt.Sprintf("%04d/%02d", m.Year, m.Month)
		if base != "" {
			scope = base + "/" + scope
		}
		out = append(out, monthLink{
			Label: monthKey(m.Year, m.Month),
			URL:   pageURL(scope, 0),
			Year:  m.Year,
			Month: m.Month,
		})
	}
	return out
}
