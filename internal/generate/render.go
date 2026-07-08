package generate

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/hec-ovi/censurado-web-backend/domain"
	"github.com/hec-ovi/censurado-web-backend/media"
)

//go:embed templates/*.tmpl templates/components/*.tmpl
var templateFS embed.FS

// assetFS holds the public frontend assets emitted at stable /assets/ URLs:
// stylesheet, client-side Tier-B facet/menu/theme script, favicon, and the cycling
// masthead background videos.
//
//go:embed templates/assets/style.css templates/assets/app.js templates/assets/favicon.svg templates/assets/video1.mp4 templates/assets/video2.mp4 templates/assets/video3.mp4 templates/assets/video4.mp4
var assetFS embed.FS

// argentinaZone is the portal's local timezone (UTC-3, no DST since 2009). The
// per-card signature stamp renders the publish instant in this zone so readers
// see local time; the machine datetime attr and the day separators stay UTC.
var argentinaZone = time.FixedZone("ART", -3*60*60)

var templateFuncs = template.FuncMap{
	"rfc3339": func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	// humandateart is the card kicker date ("2026-06-08") in Argentina local time
	// (UTC-3), so it names the same day as the ART day separator above it. The
	// companion datetime="" attr stays UTC (rfc3339), the machine/SEO instant.
	"humandateart": func(t time.Time) string { return t.In(argentinaZone).Format("2006-01-02") },
	"humandatees":  dayLabelES, // "28 de junio de 2026", matching the day separators
	// humanstampar is the displayed signature stamp in Argentina local time:
	// "29 de junio de 2026, 08:30PM" (long Spanish date, then AM/PM time). Used on
	// cards and the article view, fed by CreatedAt (the real, unspoofable insert time).
	"humanstampar": humanstampar,
	// t is the language-aware UI-string lookup. This package-level entry is a parse-
	// time placeholder so the base templates compile; each run rebinds it to its own
	// buildEnv.T (bindTemplates), so the real lookup is per-language.
	"t": func(string) string { return "" },
	// cnzI18N emits the window.__CNZ_I18N__ <script> app.js reads; a parse-time
	// placeholder rebound per run to buildEnv's resolved blob.
	"cnzI18N": func() template.HTML { return "" },
}

// canonicalSectionSlug folds the legacy `economics` section into its first-class
// name `misterio-y-conspiracion`. The section was historically STORED as
// `economics` and only relabelled to "Misterio y conspiración" in Spanish at
// render time, a confusing split from the same-named nav topic facet. New
// articles file directly under `misterio-y-conspiracion`; folding the legacy
// value at load keeps the handful of old `economics` pieces on the one canonical
// section page instead of an orphaned duplicate. Section axis ONLY: an
// `economics` TOPIC tag is a different thing and is left untouched.
func canonicalSectionSlug(section string) string {
	if section == "economics" {
		return "misterio-y-conspiracion"
	}
	return section
}

// sectionLabel resolves a section facet's reader-facing label: the catalog value
// (section.<slug>.label, from frontend_text or the compiled default) when the slug
// is known, else the facet's stored label (Title-cased). It is keyed on Facet.Value
// (the slug), so it depends only on the scope and the run language and stays
// byte-stable on sealed pages. This is the ONE section-label source the nav, the
// listing headings, the card kickers, and the shards all read.
func (env *buildEnv) sectionLabel(f Facet) string {
	if v, ok := env.lookup("section." + f.Value + ".label"); ok {
		return v
	}
	return f.labelOr()
}

// facetLabel returns the reader-facing label for any facet, applying the section
// catalog only on the section axis and leaving author/topic labels (and month,
// which has no label) to the stored casing.
func (env *buildEnv) facetLabel(f Facet) string {
	if f.Kind == AxisSection {
		return env.sectionLabel(f)
	}
	return f.labelOr()
}

// Two parsed sets share base.tmpl; each pairs it with one content template that
// defines "content". A single combined set is impossible because listing.tmpl and
// article.tmpl both define "content" (and listing.tmpl also defines "headextra").
// The four base trees are parsed once with a placeholder "t"; each run clones them
// and binds its own language-aware "t" (buildEnv.bindTemplates), so rendering is
// per-language while parsing stays one-time.
var (
	listingTmplBase  = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.tmpl", "templates/listing.tmpl", "templates/components/*.tmpl"))
	articleTmplBase  = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.tmpl", "templates/article.tmpl", "templates/components/*.tmpl"))
	aboutTmplBase    = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.tmpl", "templates/about.tmpl", "templates/components/*.tmpl"))
	notFoundTmplBase = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.tmpl", "templates/notfound.tmpl", "templates/components/*.tmpl"))
)

// headData is the shared <head> model: the SEO/social meta plus the per-scope
// manifest link. Every field is derived from the page's own fixed inputs (its
// scope/article, BaseURL, SiteName), never from env.now or the live index size,
// so it is byte-stable on sealed pages. It is embedded in both view types, so the
// base template reads .Title, .Canonical, etc. on either page kind.
type headData struct {
	Title       string        // <title> and og:title/twitter:title
	Lang        string        // render language ISO code; the <html lang> attribute
	Canonical   string        // absolute self URL; also og:url
	Description string        // <meta name=description>, og/twitter description
	Keywords    string        // <meta name=keywords>, "" when none (articles only)
	SiteName    string        // og:site_name, masthead
	OGType      string        // "article" on permalinks, "website" on listings
	OGImage     string        // absolute image URL, "" when none
	OGVideo     string        // absolute video URL, "" when none
	TwitterCard string        // "summary" or "summary_large_image"
	JSONLD      template.HTML // pre-rendered <script type=application/ld+json>
	ManifestURL string        // listing pages only; "" on articles
	NavLinks    []navLink     // page-local portal menu links; stable on sealed pages
}

type pageView struct {
	headData
	PrevURL   string
	NextURL   string
	IsLanding bool
	FeedLinks []feedLink
	Heading   string
	Items     []itemView
	Rail      []itemView
	// ShowRail renders the "Recomendado" widget even when Rail is empty. The front
	// page always shows it (so the two-column layout holds); other landings show it
	// only when the computed rail has items.
	ShowRail bool
	Pager    pagerView
	Months   []monthLink
	Manifest template.HTML
	// Author bio block, populated only on a single-author listing page.
	AuthorName    string
	AuthorBio     string
	AuthorGender  string
	AuthorAvatar  string
	AuthorInitial string
	AuthorTopics  []topicLink
}

type itemView struct {
	Title         string
	Subtitle      string // authored dek (metadata.subtitle); "" when absent
	Description   string // authored standfirst (metadata.description); "" when absent
	URL           string
	Canonical     string // absolute permalink (siteBase + URL); the card share targets
	AuthorLabel   string
	AuthorURL     string
	AuthorSlug    string // slug form, matching shard/server membership (data-author)
	AuthorInitial string
	AuthorAvatar  string
	Section       string
	SectionURL    string
	SectionSlug   string // slug form (data-section)
	Topics        []topicLink
	TopicsAttr    string // space-joined topic slugs (data-topics)
	MonthSlug     string // YYYY-MM publication month (data-month)
	PublishedAt   time.Time
	CreatedAt     time.Time // real insert time (backend-stamped, unspoofable); drives the displayed stamp
	Thumb         mediaView
	// DaySeparator is the Spanish day label ("27 de junio de 2026") shown as a
	// full-width separator ABOVE this item, set only on the first item of a new
	// published day and never on the first item overall (so the newest day carries
	// no separator). Empty otherwise. Server-rendered so the date grouping shows on
	// every scope with JS off; the client continues it on infinite-scroll appends.
	DaySeparator string
	// Important marks a curated full-row card (role "important" in the per-day plan):
	// it spans the whole grid row like the day lead but keeps the normal card body.
	// The day's lead (first item of the day) is handled positionally, so Important is
	// only honored on non-lead cards. Always false when no plan curates the day.
	Important bool
}

type topicLink struct{ Label, URL string }
type monthLink struct {
	Label, URL, Slug string
	Year, Month      int
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
type navLink struct{ Label, URL string }

type articleView struct {
	headData
	Slug          string // backend slug, the key the reactions bar votes under (data-slug)
	Subtitle      string // authored dek (metadata.subtitle); "" when absent
	Standfirst    string // authored description/lede (metadata.description); "" when absent
	ImageCaption  string // authored hero caption/epígrafe (metadata.image_caption); "" when absent
	ImageCredit   string // authored hero source credit (metadata.image_credit); "" when absent
	AuthorLabel   string
	AuthorURL     string
	AuthorSlug    string // slug form, for the client-side "Más de este autor" fetch
	AuthorInitial string
	AuthorAvatar  string
	Section       string
	SectionURL    string
	Topics        []topicLink
	PublishedAt   time.Time
	CreatedAt     time.Time // real insert time (backend-stamped, unspoofable); drives the displayed stamp
	BodyHTML      template.HTML
	Media         mediaView  // optional lead media: image, video, or YouTube
	AuthorMore    []itemView // "Más de este autor" lateral rail (same author, excluding self)
	Related       []itemView // articles sharing topics/section, excluding self
}

type mediaView struct {
	Kind   string // "image", "video", "youtube", or ""
	Src    string
	Alt    string
	Title  string
	Poster string
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
	heading := env.scopeHeading(sc)
	title := env.pageTitle(heading, pg)
	canonical := absolute(env.siteBase, pg.Canonical)
	desc := env.scopeDescription(sc)
	ld, err := collectionJSONLD(heading, canonical, desc)
	if err != nil {
		return nil, err
	}
	view := pageView{
		headData: headData{
			Title:       title,
			Lang:        env.lang,
			Canonical:   canonical,
			Description: desc,
			SiteName:    env.siteName,
			OGType:      "website",
			TwitterCard: "summary",
			JSONLD:      ld,
			// Pure function of the scope, so it is byte-stable on sealed pages.
			ManifestURL: manifestURL(sc.ShardKey()),
			NavLinks:    env.navLinks(),
		},
		IsLanding: pg.Landing,
		Heading:   heading,
	}
	// Prev points to the older page (PageURL(N-1) for sealed N, PageURL(full)
	// for the landing): stable for sealed pages because it depends only on N.
	if pg.Prev != "" {
		view.PrevURL = absolute(env.siteBase, pg.Prev)
	}
	for _, a := range pg.Articles {
		iv := env.itemViewOf(a)
		iv.Important = env.plan.Index.portadaRole[a.ID] == "important"
		view.Items = append(view.Items, iv)
	}
	authorLanding := isAuthorScope(sc) && pg.Landing
	if authorLanding {
		slug := sc.Section.Value
		view.AuthorName = env.plan.Index.authorName[slug]
		if view.AuthorName == "" {
			// No metadata.author_name on any carrier: fall back to the stored
			// author label so the bio block still has a heading.
			view.AuthorName = sc.Section.labelOr()
		}
		view.AuthorBio = env.plan.Index.authorBio[slug]
		view.AuthorGender = env.plan.Index.authorGender[slug]
		view.AuthorInitial = authorInitial(view.AuthorName)
		view.AuthorTopics = authorTopicLinks(env.plan.Index, slug)
		if raw := env.plan.Index.authorAvatar[slug]; raw != "" {
			view.AuthorAvatar, _ = media.SafeMediaURL(env.siteBase, raw)
		}
		view.Items = env.authorLatestItems(env.plan.Index, sc, env.pageSize)
	} else if pg.Landing {
		// "Recomendado" lives only on the scope landing (the front page and the
		// section/topic fronts). Sealed deep-pagination pages omit it on purpose: a
		// live "below page" rail there would break the append-only byte-stability of
		// older pages under a backdated insert.
		if isLatest(sc) {
			// The FRONT PAGE shows the site's single GLOBAL editor's-pick list: a
			// fixed, day-independent, persistent list an operator curates (NOT the
			// computed below-fold rail). The widget always renders here, even when the
			// list is empty, so the two-column layout holds.
			view.Rail = globalRail(env)
			view.ShowRail = true
		} else {
			// Section/topic/author fronts keep the computed below-fold rail, drawn from
			// articles below this page's window so it never repeats the grid; shown
			// only when it has items.
			view.Rail = env.railBelowPage(env.plan.Index, sc, pg.Articles, recomendadoCap)
			view.ShowRail = len(view.Rail) > 0
		}
	}
	// Group the listing by published day with full-width separators (every scope,
	// server-rendered). Runs after the author override and the rail copy so it
	// marks exactly the items that render in the list.
	markDaySeparators(view.Items)
	if pg.Landing {
		view.Manifest = manifest
		view.Pager = pagerFor(env.plan.Index, sc, env.pageSize, pg.Number)
		view.Months = monthLinksFor(env.plan.Index, sc)
		if pg.Next != "" {
			view.NextURL = absolute(env.siteBase, pg.Next)
		}
		if isLatest(sc) {
			view.FeedLinks = env.feedLinks()
		}
	}

	var buf bytes.Buffer
	if err := env.listingTmpl.ExecuteTemplate(&buf, "base", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// aboutView is the model for the /about/ page: the site head plus the roster of
// every author with at least one article.
type aboutView struct {
	headData
	Kicker  string
	Heading string
	Summary string
	Intro   []string
	Authors []authorCardView
}

type authorCardView struct {
	Name        string
	URL         string
	Avatar      string
	Initial     string
	Bio         string
	SectionSlug string // the author's beat slug, for the theme color (data-section)
	Beat        string // Spanish beat label shown beside the name
}

// orderedAuthorSlugs returns the author slugs present in the index in a
// deterministic, persona-agnostic order: most-published first (the lead voice),
// tie-broken by the author's earliest-inserted article, then by slug. No slug is
// hardcoded, so an empty corpus yields no authors and a brand-new persona is
// ranked purely by its own published output.
func orderedAuthorSlugs(authors map[string][]int) []string {
	out := sortedStringKeys(authors) // slug-ASC base; the final, stable tie-break
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := authors[out[i]], authors[out[j]]
		if len(ai) != len(aj) {
			return len(ai) > len(aj) // more articles first
		}
		if len(ai) == 0 {
			// Two registered authors with no articles yet: keep the slug-ASC base
			// order (indexing [0] below would panic on an empty membership slice).
			return false
		}
		// authors[slug] holds ascending All-indices (CreatedAt ASC, id ASC), so
		// [0] is the author's earliest-inserted article: founding order.
		return ai[0] < aj[0]
	})
	return out
}

// authorCards lists every author scope's display name, avatar, bio, and link to
// the single-author page, in the index's deterministic author-slug order.
func authorCards(env *buildEnv) []authorCardView {
	idx := env.plan.Index
	var out []authorCardView
	for _, slug := range orderedAuthorSlugs(idx.authors) {
		if idx.authorDeleted[slug] {
			continue // tombstoned author: keep their articles/page/bylines, drop the roster card
		}
		card := authorCardView{
			Name: idx.authorName[slug],
			URL:  pageURL("author/"+slug, 0),
			Bio:  idx.authorBio[slug],
		}
		if card.Name == "" {
			card.Name = idx.authorLabel[slug]
		}
		card.Initial = authorInitial(card.Name)
		if sec := idx.authorSection[slug]; sec != "" {
			card.SectionSlug = sec
			card.Beat = env.sectionLabel(Facet{Kind: AxisSection, Value: sec})
		}
		if raw := idx.authorAvatar[slug]; raw != "" {
			card.Avatar, _ = media.SafeMediaURL(env.siteBase, raw)
		}
		out = append(out, card)
	}
	return out
}

// renderAbout renders the /about/ page listing every author. Its copy (heading,
// kicker, summary, two-paragraph manifesto, meta description) reads the catalog, so
// the /about/ URL stays fixed while the text follows the run language.
func renderAbout(env *buildEnv) ([]byte, error) {
	canonical := absolute(env.siteBase, "/about/")
	heading := env.T("about.heading")
	desc := env.T("about.meta_description")
	ld, err := collectionJSONLD(heading, canonical, desc)
	if err != nil {
		return nil, err
	}
	view := aboutView{
		headData: headData{
			Title:       heading,
			Lang:        env.lang,
			Canonical:   canonical,
			Description: desc,
			SiteName:    env.siteName,
			OGType:      "website",
			TwitterCard: "summary",
			JSONLD:      ld,
			NavLinks:    env.navLinks(),
		},
		Kicker:  env.T("about.kicker"),
		Heading: heading,
		Summary: env.T("about.summary"),
		Intro:   []string{env.T("about.manifesto_1"), env.T("about.manifesto_2")},
		Authors: authorCards(env),
	}
	var buf bytes.Buffer
	if err := env.aboutTmpl.ExecuteTemplate(&buf, "base", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// notFoundView is the model for the site-root 404 page. It reuses the site chrome
// (header, nav, footer via base.tmpl) and carries a friendly Spanish message plus a
// link back to the portada.
type notFoundView struct {
	headData
	Heading   string
	Message   []string
	HomeURL   string
	HomeLabel string
}

// notFoundHomeURL is the one fixed 404 datum: the link target back to the front
// page. The copy (heading, message, CTA label, meta description) reads the catalog.
// The page never depends on the live corpus, so for a given language 404.html is
// byte-identical run to run (it never enters a purge diff) and is emitted even for
// an empty corpus.
const notFoundHomeURL = "/latest/"

// renderNotFound renders the /404.html page served for unknown routes. nginx serves
// it via `error_page 404 /404.html` (harness nginx/site.conf) and Cloudflare Pages
// serves the same root file by convention on the cloud deploy.
func renderNotFound(env *buildEnv) ([]byte, error) {
	canonical := absolute(env.siteBase, "/404.html")
	heading := env.T("notfound.heading")
	desc := env.T("notfound.meta_description")
	ld, err := collectionJSONLD(heading, canonical, desc)
	if err != nil {
		return nil, err
	}
	view := notFoundView{
		headData: headData{
			Title:       env.pageTitle(heading, Page{}),
			Lang:        env.lang,
			Canonical:   canonical,
			Description: desc,
			SiteName:    env.siteName,
			OGType:      "website",
			TwitterCard: "summary",
			JSONLD:      ld,
			NavLinks:    env.navLinks(),
		},
		Heading:   heading,
		Message:   []string{env.T("notfound.body_1"), env.T("notfound.body_2")},
		HomeURL:   notFoundHomeURL,
		HomeLabel: env.T("notfound.home_cta"),
	}
	var buf bytes.Buffer
	if err := env.notFoundTmpl.ExecuteTemplate(&buf, "base", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderArticle renders one permalink page.
func renderArticle(env *buildEnv, a domain.Article) ([]byte, error) {
	canonical := absolute(env.siteBase, articleURL(a))
	bodyHTML := env.bodyHTML[a.ContentHash]

	media := env.mediaForArticle(a)
	ogImage := media.ogImage
	ogVideo := media.ogVideo
	twitterCard := "summary"
	if ogImage != "" {
		twitterCard = "summary_large_image"
	}

	ld, err := articleJSONLD(a, canonical, ogImage)
	if err != nil {
		return nil, err
	}

	view := articleView{
		headData: headData{
			Title:       a.Title,
			Lang:        env.lang,
			Canonical:   canonical,
			Description: metaDescription(bodyHTML),
			Keywords:    keywordsFor(a),
			SiteName:    env.siteName,
			OGType:      "article",
			OGImage:     ogImage,
			OGVideo:     ogVideo,
			TwitterCard: twitterCard,
			JSONLD:      ld,
			NavLinks:    env.navLinks(),
		},
		Slug:          a.Slug,
		Subtitle:      firstMetadataString(a.Metadata, "subtitle"),
		Standfirst:    firstMetadataString(a.Metadata, "description"),
		ImageCaption:  firstMetadataString(a.Metadata, "image_caption"),
		ImageCredit:   firstMetadataString(a.Metadata, "image_credit"),
		AuthorLabel:   authorDisplayLabel(a),
		AuthorURL:     facetURL("author", a.Author),
		AuthorSlug:    ShardEntryOf(a).Author,
		AuthorInitial: authorInitial(authorDisplayLabel(a)),
		AuthorAvatar:  metadataMediaSrc(env.siteBase, a.Metadata, "author_avatar", "avatar"),
		Section:       env.sectionLabelOf(a),
		SectionURL:    facetURL("section", a.Section),
		Topics:        topicLinksOf(a),
		PublishedAt:   a.PublishedAt,
		CreatedAt:     a.CreatedAt,
		BodyHTML:      template.HTML(bodyHTML),
		Media:         media.view,
	}
	view.AuthorMore = env.articleAuthorMore(env.plan.Index, a, 4)
	view.Related = env.articleRelated(env.plan.Index, a, 4)
	var buf bytes.Buffer
	if err := env.articleTmpl.ExecuteTemplate(&buf, "base", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (env *buildEnv) itemViewOf(a domain.Article) itemView {
	// Reuse the canonical shard projection for the slug-form data-* hooks so the
	// client refiner's membership matches the server-rendered shards exactly.
	se := ShardEntryOf(a)
	return itemView{
		Title:         a.Title,
		Subtitle:      firstMetadataString(a.Metadata, "subtitle"),
		Description:   firstMetadataString(a.Metadata, "description"),
		URL:           articleURL(a),
		Canonical:     absolute(env.siteBase, articleURL(a)),
		AuthorLabel:   authorDisplayLabel(a),
		AuthorURL:     facetURL("author", a.Author),
		AuthorSlug:    se.Author,
		AuthorInitial: authorInitial(authorDisplayLabel(a)),
		AuthorAvatar:  metadataMediaSrc("", a.Metadata, "author_avatar", "avatar"),
		Section:       env.sectionLabelOf(a),
		SectionURL:    facetURL("section", a.Section),
		SectionSlug:   se.Section,
		Topics:        topicLinksOf(a),
		TopicsAttr:    strings.Join(se.Topics, " "),
		MonthSlug:     monthKey(a.PublishedAt.Year(), int(a.PublishedAt.Month())),
		PublishedAt:   a.PublishedAt,
		CreatedAt:     a.CreatedAt,
		Thumb:         thumbForArticle(a),
	}
}

// navLinks returns the fixed, curated top menu. It is identical on every page
// (landing, article, section, About), which removes the old reshuffle bug where
// the entries and their order were derived from the current page's articles and so
// changed as the reader clicked around. Every entry points at its section facet
// page, including "Misterio y conspiración", the first-class section the legacy
// `economics` slug folds into (canonicalSectionSlug). Labels read the ONE section
// catalog (section.<slug>.label) plus nav.latest/nav.about, so the world entry now
// reads the same "Mundo" as its heading instead of the old, divergent
// "Internacionales". The menu is intentionally fixed, so a category stays in the
// bar even on a page that does not feature it.
func (env *buildEnv) navLinks() []navLink {
	sec := func(slug string) navLink {
		return navLink{Label: env.sectionLabel(Facet{Kind: AxisSection, Value: slug}), URL: facetURL("section", slug)}
	}
	return []navLink{
		{Label: env.T("nav.latest"), URL: "/latest/"},
		{Label: env.T("nav.about"), URL: "/about/"},
		sec("politics"),
		sec("world"),
		sec("misterio-y-conspiracion"),
		sec("tech"),
		sec("literatura"),
	}
}

// monthsES are the Spanish month names, matching the client's formatDayES so a
// server-rendered day separator and a scroll-appended one read identically.
var monthsES = []string{
	"enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre",
}

// dayLabelES formats a published instant as "D de mes de YYYY" in Argentina local
// time (UTC-3), so the day separator matches the reader's ART signature stamp
// instead of UTC. The client derives the same ART day from the card's RFC3339
// datetime attribute.
func dayLabelES(t time.Time) string {
	u := t.In(argentinaZone)
	return fmt.Sprintf("%d de %s de %d", u.Day(), monthsES[int(u.Month())-1], u.Year())
}

// humanstampar is the displayed signature stamp in Argentina local time (UTC-3,
// no DST): "29 de junio de 2026, 20:30" (long Spanish date, then 24-hour time). The
// 24-hour clock replaces the old English AM/PM; the client longStampES mirrors it
// exactly so a scroll-appended card matches a sealed one.
func humanstampar(t time.Time) string {
	lt := t.In(argentinaZone)
	return fmt.Sprintf("%d de %s de %d, %s", lt.Day(), monthsES[int(lt.Month())-1], lt.Year(), lt.Format("15:04"))
}

// markDaySeparators sets DaySeparator on the first item of each new published day
// in a display-ordered (newest-first) item slice, skipping the first item so the
// newest day carries no separator. The day is keyed in Argentina local time (UTC-3)
// so the separator matches the reader's ART card stamp. Pure function of the slice,
// so a sealed page stays byte-stable. It mutates in place (itemView elements are
// addressable).
//
// The key is PublishedAt (the display sort key), so across days it is monotonic and
// the separators walk oldest-ward cleanly. Within one UTC day a per-day plan
// (portada) may reorder cards out of instant order; only such a curated day that
// also straddles 03:00Z (ART midnight) spans two ART days, in which case the
// separators reflect that split. Uncurated days are always instant-monotonic and
// unaffected.
func markDaySeparators(items []itemView) {
	lastDay := ""
	for i := range items {
		day := items[i].PublishedAt.In(argentinaZone).Format("2006-01-02")
		if i > 0 && lastDay != "" && day != lastDay {
			items[i].DaySeparator = dayLabelES(items[i].PublishedAt)
		}
		lastDay = day
	}
}

// railBelowPage builds the "Recomendado" rail from the scope's articles that are
// strictly OLDER than this page's oldest item, newest-first, capped at n. Bounding
// the rail below the page's window does two things: it never repeats the grid the
// reader is already looking at (the requested fix), and it stays byte-stable,
// because a later (newer) publish lands ABOVE the window and so can never enter a
// sealed page's rail.
func (env *buildEnv) railBelowPage(idx *Index, sc Scope, pageArts []domain.Article, n int) []itemView {
	if len(pageArts) == 0 {
		return nil
	}
	// The page's oldest article (last in display order) is the upper bound: only
	// strictly-older articles may enter the rail.
	oldest := pageArts[0]
	for _, a := range pageArts[1:] {
		if displayLess(oldest, a) { // oldest is newer than a, so a is older
			oldest = a
		}
	}
	scopeArts := idx.articlesAt(idx.scopeIndices(sc))
	sortDisplay(scopeArts)
	below := make([]domain.Article, 0, len(scopeArts))
	for _, a := range scopeArts {
		if displayLess(oldest, a) { // a is strictly older than the page's oldest
			below = append(below, a)
		}
	}
	return env.itemViewsOf(below, n)
}

// recomendadoCap bounds the front-page "Recomendado" rail (and the computed
// below-fold rail on the other landings). It matches the backend's recomendadoMax.
const recomendadoCap = 10

// globalRail resolves the site's single GLOBAL "Recomendado" list (idx.recomendado)
// to item views for the front-page rail, in the operator's stored order, skipping
// any slug with no live article and capping at recomendadoCap. It is day-independent
// and persistent: the same list renders on the front page every day until an
// operator replaces it. Returns an empty slice when nothing is curated (the caller
// still renders the widget so the layout holds).
func globalRail(env *buildEnv) []itemView {
	idx := env.plan.Index
	if len(idx.recomendado) == 0 {
		return nil
	}
	bySlug := make(map[string]domain.Article, len(idx.All))
	for _, a := range idx.All {
		bySlug[a.Slug] = a
	}
	out := make([]itemView, 0, len(idx.recomendado))
	for _, s := range idx.recomendado {
		if a, ok := bySlug[s]; ok {
			out = append(out, env.itemViewOf(a))
			if len(out) >= recomendadoCap {
				break
			}
		}
	}
	return out
}

// articlesUpToSelf returns every article that is NOT newer than self (older or
// same in display order), excluding self, in display order (newest first).
// Restricting an article permalink's rail/related to "up to self" keeps the
// page byte-stable when newer articles are later published: a forward publish
// never enters an older page's window, so it is not re-rendered or purged. This
// preserves the append-only immutability the listing pages also rely on.
func articlesUpToSelf(idx *Index, self domain.Article) []domain.Article {
	out := make([]domain.Article, 0, len(idx.All))
	for _, a := range idx.All {
		if a.ID == self.ID || displayLess(a, self) {
			continue
		}
		out = append(out, a)
	}
	sortDisplay(out)
	return out
}

func (env *buildEnv) itemViewsOf(arts []domain.Article, n int) []itemView {
	if n > len(arts) {
		n = len(arts)
	}
	out := make([]itemView, 0, n)
	for _, a := range arts[:n] {
		out = append(out, env.itemViewOf(a))
	}
	return out
}

func (env *buildEnv) authorLatestItems(idx *Index, sc Scope, n int) []itemView {
	arts := idx.articlesAt(idx.scopeIndices(sc))
	sortDisplay(arts)
	return env.itemViewsOf(arts, n)
}

// maxAuthorTopics bounds the auto-computed topic union on an author profile so a
// prolific author does not paper the page with dozens of chips. It caps ONLY the
// fallback: a curated profile_topics list (from the author registry) is shown in full,
// trusting the operator/agent that chose it.
const maxAuthorTopics = 8

func authorTopicLinks(idx *Index, authorSlug string) []topicLink {
	// Prefer the operator/agent-curated list when the registry carries one. Each entry is
	// normalized to its facet slug (so topics read the same as everywhere else) and kept
	// only when it has a real topic page (published articles anywhere), so a curated topic
	// never links to a dead page and the "never manufacture a heading" invariant holds. An
	// all-empty curated list (nothing survives) falls through to the computed union.
	if curated := idx.authorProfileTopics[authorSlug]; len(curated) > 0 {
		out := make([]topicLink, 0, len(curated))
		seen := map[string]struct{}{}
		for _, raw := range curated {
			slug, ok := facetSlug(raw)
			if !ok {
				continue
			}
			if _, dup := seen[slug]; dup {
				continue
			}
			if _, live := idx.topics[slug]; !live {
				continue
			}
			seen[slug] = struct{}{}
			out = append(out, topicLink{Label: slug, URL: pageURL("topic/"+slug, 0)})
		}
		if len(out) > 0 {
			return out
		}
	}

	counts := map[string]int{}
	for _, i := range idx.authors[authorSlug] {
		seen := map[string]struct{}{}
		for _, raw := range idx.All[i].Topics {
			slug, ok := facetSlug(raw)
			if !ok {
				continue
			}
			if _, dup := seen[slug]; dup {
				continue
			}
			seen[slug] = struct{}{}
			counts[slug]++
		}
	}
	slugs := make([]string, 0, len(counts))
	for slug := range counts {
		slugs = append(slugs, slug)
	}
	sort.Slice(slugs, func(i, j int) bool {
		if counts[slugs[i]] != counts[slugs[j]] {
			return counts[slugs[i]] > counts[slugs[j]]
		}
		return slugs[i] < slugs[j]
	})
	// Cap the union so the profile shows the author's dominant beats, not every stray tag.
	if len(slugs) > maxAuthorTopics {
		slugs = slugs[:maxAuthorTopics]
	}
	out := make([]topicLink, 0, len(slugs))
	for _, slug := range slugs {
		out = append(out, topicLink{Label: slug, URL: pageURL("topic/"+slug, 0)})
	}
	return out
}

// articleAuthorMore is the "Más de este autor" lateral rail for a permalink: the
// same author's other articles, up to and excluding self (newest first).
// Restricting to "up to self" keeps the permalink byte-stable when the author
// publishes again later.
func (env *buildEnv) articleAuthorMore(idx *Index, self domain.Article, n int) []itemView {
	var same []domain.Article
	for _, a := range articlesUpToSelf(idx, self) {
		if a.Author == self.Author {
			same = append(same, a)
		}
	}
	return env.itemViewsOf(same, n)
}

// articleRelated is the "Relacionados" block: articles up to self that share at
// least one topic with self (falling back to the same section when none do),
// newest first.
func (env *buildEnv) articleRelated(idx *Index, self domain.Article, n int) []itemView {
	selfTopics := map[string]struct{}{}
	for _, t := range self.Topics {
		if s, ok := facetSlug(t); ok {
			selfTopics[s] = struct{}{}
		}
	}
	pool := articlesUpToSelf(idx, self)
	var related []domain.Article
	for _, a := range pool {
		for _, t := range a.Topics {
			if s, ok := facetSlug(t); ok {
				if _, hit := selfTopics[s]; hit {
					related = append(related, a)
					break
				}
			}
		}
	}
	if len(related) == 0 {
		if sSlug, ok := facetSlug(self.Section); ok {
			for _, a := range pool {
				if s, ok2 := facetSlug(a.Section); ok2 && s == sSlug {
					related = append(related, a)
				}
			}
		}
	}
	return env.itemViewsOf(related, n)
}

// cardThumb builds the listing card's media from an AUTHORED metadata.card block
// (the explicit preview the writer chose). ok is false when the article authored no
// usable card (no card object, or an unknown/blank type), so thumbForArticle falls
// back to the legacy derivation below, which keeps every pre-card (sealed) page
// byte-identical. The card decouples the preview from the body: it is the single
// source of what the card shows, regardless of how many videos/images the body
// embeds. type "text" -> no thumbnail; "image"/"video" -> the /media src as an
// <img> (video adds the play badge, since an .mp4 has no auto thumbnail so src is a
// poster still); "youtube" -> the YouTube poster + play badge.
func cardThumb(a domain.Article) (mediaView, bool) {
	c, isMap := a.Metadata["card"].(map[string]any)
	if !isMap {
		return mediaView{}, false
	}
	typ, _ := c["type"].(string)
	typ = strings.TrimSpace(typ)
	src, _ := c["src"].(string)
	src = strings.TrimSpace(src)
	alt, _ := c["alt"].(string)
	if alt = strings.TrimSpace(alt); alt == "" {
		alt = a.Title
	}
	switch typ {
	case "text":
		return mediaView{}, true
	case "image", "video":
		resolved, _ := media.SafeMediaURL("", src)
		if resolved == "" {
			return mediaView{}, true // authored but no usable src -> a text card
		}
		kind := "image"
		if typ == "video" {
			kind = "video"
		}
		return mediaView{Kind: kind, Src: resolved, Alt: alt}, true
	case "youtube":
		if embed := media.YouTubeEmbedURL(src); embed != "" {
			id := strings.TrimPrefix(embed, "https://www.youtube-nocookie.com/embed/")
			return mediaView{Kind: "youtube", Src: youtubePosterURL(id), Alt: alt}, true
		}
		return mediaView{}, true // an unparseable youtube ref -> a text card
	}
	return mediaView{}, false // no/unknown type -> legacy fallback
}

// thumbForArticle picks a listing card's media. An AUTHORED metadata.card wins
// (cardThumb). Otherwise, for a legacy piece with no card block: a real
// metadata.image wins (Kind:"image"); else, when the body's FIRST {{video:...}}
// marker is a YouTube reference, the card borrows its poster as a thumbnail
// (Kind:"youtube", so the card shows a play badge over the poster); the click still
// opens the article where the real embed plays. A body with no image and no YouTube
// video (or whose first video is a self-hosted .mp4) stays text-only.
func thumbForArticle(a domain.Article) mediaView {
	if v, ok := cardThumb(a); ok {
		return v
	}
	if src := metadataMediaSrc("", a.Metadata, "image"); src != "" {
		alt := firstMetadataString(a.Metadata, "image_alt", "alt")
		if alt == "" {
			alt = a.Title
		}
		return mediaView{Kind: "image", Src: src, Alt: alt}
	}
	if poster := firstBodyVideoPoster(a); poster != "" {
		return mediaView{Kind: "youtube", Src: poster, Alt: a.Title}
	}
	return mediaView{}
}

// metaDescriptionMax bounds the SEO excerpt at ~160 runes. This is the only
// fixed-length truncation in the package: a structured <meta> tag over already
// stored prose. The article body itself is never truncated anywhere.
const metaDescriptionMax = 160

var (
	htmlTagRe = regexp.MustCompile(`<[^>]*>`)
	wsRe      = regexp.MustCompile(`\s+`)
)

// metaDescription derives a deterministic plain-text excerpt from rendered body
// HTML: strip tags, unescape entities, collapse whitespace, then cut at a word
// boundary at metaDescriptionMax runes, appending a single ellipsis if truncated.
// The result is re-escaped by the template when written into the meta attribute.
func metaDescription(bodyHTML string) string {
	text := htmlTagRe.ReplaceAllString(bodyHTML, " ")
	text = html.UnescapeString(text)
	text = strings.TrimSpace(wsRe.ReplaceAllString(text, " "))
	return truncateAtWord(text, metaDescriptionMax)
}

func truncateAtWord(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ") + "…"
}

// scopeDescription is a short, scope-derived listing description. It depends only
// on the scope, the site name, and the run language, so it is byte-stable on sealed
// pages.
func (env *buildEnv) scopeDescription(s Scope) string {
	site := env.siteName
	if s.isSingle() {
		switch s.Section.Kind {
		case AxisLatest:
			return fmt.Sprintf(env.T("meta.desc_latest"), site)
		case AxisSection:
			return fmt.Sprintf(env.T("meta.desc_section"), env.facetLabel(s.Section), site)
		case AxisAuthor:
			return fmt.Sprintf(env.T("meta.desc_author"), env.facetLabel(s.Section), site)
		case AxisTopic:
			return fmt.Sprintf(env.T("meta.desc_topic"), env.facetLabel(s.Section), site)
		case AxisMonth:
			return fmt.Sprintf(env.T("meta.desc_month"), monthKey(s.Section.Year, s.Section.Month), site)
		}
	}
	return fmt.Sprintf(env.T("meta.desc_generic"), env.scopeHeading(s), site)
}

// metadataString reads a non-empty string value from the open metadata object.
func metadataString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

type articleMedia struct {
	view    mediaView
	ogImage string
	ogVideo string
}

func (env *buildEnv) mediaForArticle(a domain.Article) articleMedia {
	base := env.siteBase
	imgSrc, imgAbs := metadataMediaURL(base, a.Metadata, "image")
	imgAlt := firstMetadataString(a.Metadata, "image_alt", "alt")
	if imgAlt == "" {
		imgAlt = a.Title
	}
	videoTitle := fmt.Sprintf(env.T("media.video_prefix"), a.Title)

	if yt, ok := metadataString(a.Metadata, "youtube"); ok {
		if embed := media.YouTubeEmbedURL(yt); embed != "" {
			return articleMedia{
				view:    mediaView{Kind: "youtube", Src: embed, Title: videoTitle, Poster: imgSrc},
				ogImage: imgAbs,
				ogVideo: embed,
			}
		}
	}
	if yt, ok := metadataString(a.Metadata, "youtube_id"); ok {
		if embed := media.YouTubeEmbedURL(yt); embed != "" {
			return articleMedia{
				view:    mediaView{Kind: "youtube", Src: embed, Title: videoTitle, Poster: imgSrc},
				ogImage: imgAbs,
				ogVideo: embed,
			}
		}
	}
	if vidSrc, vidAbs := metadataMediaURL(base, a.Metadata, "video"); vidSrc != "" {
		return articleMedia{
			view:    mediaView{Kind: "video", Src: vidSrc, Title: videoTitle, Poster: imgSrc},
			ogImage: imgAbs,
			ogVideo: vidAbs,
		}
	}
	if imgSrc != "" {
		return articleMedia{
			view:    mediaView{Kind: "image", Src: imgSrc, Alt: imgAlt},
			ogImage: imgAbs,
		}
	}
	return articleMedia{}
}

func metadataMediaSrc(base string, m map[string]any, keys ...string) string {
	for _, key := range keys {
		src, _ := metadataMediaURL(base, m, key)
		if src != "" {
			return src
		}
	}
	return ""
}

func metadataMediaURL(base string, m map[string]any, key string) (src, absoluteURL string) {
	raw, ok := metadataString(m, key)
	if !ok {
		return "", ""
	}
	return media.SafeMediaURL(base, raw)
}

func firstMetadataString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := metadataString(m, key); ok {
			return s
		}
	}
	return ""
}

// sectionLabelOf is the reader-facing section label for one article: the catalog
// display name when the slug is known, else the stored section string. The URL slug
// and data-section hook are unchanged (they keep the English slug).
func (env *buildEnv) sectionLabelOf(a domain.Article) string {
	slug, ok := facetSlug(a.Section)
	if !ok {
		return a.Section
	}
	return env.sectionLabel(Facet{Kind: AxisSection, Value: slug, Label: a.Section})
}

// sectionLabelDefault resolves an article's section label from the compiled catalog
// only (no DB), the language-neutral base the env-free client shard projection
// (ShardEntryOf) carries. buildScopeShards overrides it with the run's env-resolved
// label when a non-default language or a DB override applies, so the emitted shard
// still tracks the site language while ShardEntryOf stays env-free for its tests.
func sectionLabelDefault(a domain.Article) string {
	slug, ok := facetSlug(a.Section)
	if !ok {
		return a.Section
	}
	if v, ok := defaultText["section."+slug+".label"]; ok {
		return v
	}
	return Facet{Kind: AxisSection, Value: slug, Label: a.Section}.labelOr()
}

// authorDisplayLabel is the reader-facing byline label for one article: the
// sibling system's metadata.author_name when present, else the raw author string.
// The URL slug is still derived from a.Author, so /author/<slug>/ is unchanged.
func authorDisplayLabel(a domain.Article) string {
	if name := firstMetadataString(a.Metadata, "author_name"); name != "" {
		return name
	}
	return a.Author
}

func authorInitial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return strings.ToUpper(string(unicode.ToUpper(r)))
	}
	return "?"
}

type ldPerson struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

type articleLD struct {
	Context          string   `json:"@context"`
	Type             string   `json:"@type"`
	Headline         string   `json:"headline"`
	DatePublished    string   `json:"datePublished"`
	Author           ldPerson `json:"author"`
	ArticleSection   string   `json:"articleSection"`
	MainEntityOfPage string   `json:"mainEntityOfPage"`
	Image            []string `json:"image,omitempty"`
}

type collectionLD struct {
	Context     string `json:"@context"`
	Type        string `json:"@type"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// marshalLD serializes a JSON-LD document into a <script type=application/ld+json>
// element. json.Marshal escapes <,>,& as \uXXXX, so untrusted titles cannot break
// out of the script element and the payload is valid JSON.
func marshalLD(v any) (template.HTML, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return template.HTML(`<script type="application/ld+json">` + string(b) + `</script>`), nil
}

func articleJSONLD(a domain.Article, canonical, image string) (template.HTML, error) {
	ld := articleLD{
		Context:          "https://schema.org",
		Type:             "NewsArticle",
		Headline:         a.Title,
		DatePublished:    a.PublishedAt.UTC().Format(time.RFC3339),
		Author:           ldPerson{Type: "Person", Name: a.Author},
		ArticleSection:   a.Section,
		MainEntityOfPage: canonical,
	}
	if image != "" {
		ld.Image = []string{image}
	}
	return marshalLD(ld)
}

func collectionJSONLD(name, canonical, description string) (template.HTML, error) {
	return marshalLD(collectionLD{
		Context:     "https://schema.org",
		Type:        "CollectionPage",
		Name:        name,
		URL:         canonical,
		Description: description,
	})
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
		// Display the normalized slug (lowercase, no accents) rather than the raw
		// topic, so topics read consistently regardless of how they were stored
		// ("Análisis político", "política", "politica" all show the same).
		out = append(out, topicLink{Label: slug, URL: pageURL("topic/"+slug, 0)})
	}
	return out
}

// keywordsFor builds the comma-separated <meta name="keywords"> value for one
// article from its topics plus any article-specific metadata.keywords list,
// de-duplicated case-insensitively and order-preserving. Returns "" when empty so
// the meta tag is omitted. Topics contribute their normalized facet slug (the same
// form shown as a topic link), never the raw accented/capitalized stored string, so
// the page never leaks an unnormalized topic label; article-specific keywords from
// metadata are emitted verbatim.
func keywordsFor(a domain.Article) string {
	seen := map[string]struct{}{}
	var kw []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		key := strings.ToLower(s)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		kw = append(kw, s)
	}
	for _, t := range a.Topics {
		if slug, ok := facetSlug(t); ok {
			add(slug)
		}
	}
	if raw, ok := a.Metadata["keywords"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				add(s)
			}
		}
	}
	return strings.Join(kw, ", ")
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

// isAuthorScope reports whether s is a single-author listing page (Scope with
// AxisAuthor and no Sub), where the author bio block is rendered.
func isAuthorScope(s Scope) bool {
	return s.Section.Kind == AxisAuthor && s.isSingle()
}

func (env *buildEnv) scopeHeading(s Scope) string {
	if s.isSingle() {
		switch s.Section.Kind {
		case AxisLatest:
			return env.T("nav.latest")
		case AxisSection, AxisAuthor, AxisTopic:
			return env.facetLabel(s.Section)
		case AxisMonth:
			return monthKey(s.Section.Year, s.Section.Month)
		}
	}
	sub := ""
	switch s.Sub.Kind {
	case AxisAuthor, AxisTopic:
		sub = env.facetLabel(s.Sub)
	case AxisMonth:
		sub = monthKey(s.Sub.Year, s.Sub.Month)
	}
	return env.facetLabel(s.Section) + " / " + sub
}

func (env *buildEnv) pageTitle(heading string, pg Page) string {
	if pg.Number >= 1 {
		return fmt.Sprintf(env.T("meta.title_paged"), heading, pg.Number, env.siteName)
	}
	return fmt.Sprintf(env.T("meta.title"), heading, env.siteName)
}

func (env *buildEnv) feedLinks() []feedLink {
	site := env.siteName
	return []feedLink{
		{Rel: "alternate", Type: "application/rss+xml", Href: absolute(env.siteBase, "/feed.xml"), Title: fmt.Sprintf(env.T("feed.rss_title"), site)},
		{Rel: "alternate", Type: "application/atom+xml", Href: absolute(env.siteBase, "/atom.xml"), Title: fmt.Sprintf(env.T("feed.atom_title"), site)},
		{Rel: "alternate", Type: "application/feed+json", Href: absolute(env.siteBase, "/feed.json"), Title: fmt.Sprintf(env.T("feed.json_title"), site)},
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
			Slug:  monthKey(m.Year, m.Month),
			Year:  m.Year,
			Month: m.Month,
		})
	}
	return out
}
