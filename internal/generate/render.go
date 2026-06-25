package generate

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/media"
)

//go:embed templates/*.tmpl templates/components/*.tmpl
var templateFS embed.FS

// assetFS holds the public frontend assets emitted at stable /assets/ URLs:
// stylesheet, client-side Tier-B facet/menu/theme script, favicon, and the cycling
// masthead background videos.
//
//go:embed templates/assets/style.css templates/assets/app.js templates/assets/favicon.svg templates/assets/video1.mp4 templates/assets/video2.mp4 templates/assets/video3.mp4 templates/assets/video4.mp4
var assetFS embed.FS

var templateFuncs = template.FuncMap{
	"rfc3339":   func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"humandate": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
}

// sectionLabelsES maps a section slug (the English URL slug discovered from
// articles) to its Spanish display name. The URL slug stays English (e.g.
// /section/tech/) while the heading and facet label render in Spanish. A slug not
// listed here falls back to the stored, Title-cased label (Facet.labelOr). This
// applies ONLY to section-axis facets, never to author or topic labels.
var sectionLabelsES = map[string]string{
	"tech":      "Tecnología",
	"world":     "Mundo",
	"politics":  "Política",
	"economics": "Economía",
	"economy":   "Economía",
	"crypto":    "Cripto",
}

// sectionDisplayLabel resolves a section facet's reader-facing label: the Spanish
// name when the slug is mapped, else the facet's stored label (Title-cased). It is
// keyed on Facet.Value (the slug), so it depends only on the scope and stays
// byte-stable on sealed pages.
func sectionDisplayLabel(f Facet) string {
	if es, ok := sectionLabelsES[f.Value]; ok {
		return es
	}
	return f.labelOr()
}

// facetDisplayLabel returns the reader-facing label for any facet, applying the
// Spanish section map only on the section axis and leaving author/topic labels
// (and month, which has no label) to the stored casing.
func facetDisplayLabel(f Facet) string {
	if f.Kind == AxisSection {
		return sectionDisplayLabel(f)
	}
	return f.labelOr()
}

// Two parsed sets share base.tmpl; each pairs it with one content template that
// defines "content". A single combined set is impossible because listing.tmpl and
// article.tmpl both define "content" (and listing.tmpl also defines "headextra").
var (
	listingTmpl = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.tmpl", "templates/listing.tmpl", "templates/components/*.tmpl"))
	articleTmpl = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.tmpl", "templates/article.tmpl", "templates/components/*.tmpl"))
	aboutTmpl   = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS, "templates/base.tmpl", "templates/about.tmpl", "templates/components/*.tmpl"))
)

// headData is the shared <head> model: the SEO/social meta plus the per-scope
// manifest link. Every field is derived from the page's own fixed inputs (its
// scope/article, BaseURL, SiteName), never from env.now or the live index size,
// so it is byte-stable on sealed pages. It is embedded in both view types, so the
// base template reads .Title, .Canonical, etc. on either page kind.
type headData struct {
	Title       string        // <title> and og:title/twitter:title
	Canonical   string        // absolute self URL; also og:url
	Description string        // <meta name=description>, og/twitter description
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
	Pager     pagerView
	Months    []monthLink
	Manifest  template.HTML
	// Author bio block, populated only on a single-author listing page.
	AuthorName   string
	AuthorBio    string
	AuthorAvatar string
}

type itemView struct {
	Title         string
	Subtitle      string // authored dek (metadata.subtitle); "" when absent
	Description   string // authored standfirst (metadata.description); "" when absent
	URL           string
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
	Thumb         mediaView
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
	Subtitle      string // authored dek (metadata.subtitle); "" when absent
	Standfirst    string // authored description/lede (metadata.description); "" when absent
	AuthorLabel   string
	AuthorURL     string
	AuthorSlug    string // slug form, for the client-side "Más de este autor" fetch
	AuthorInitial string
	AuthorAvatar  string
	Section       string
	SectionURL    string
	Topics        []topicLink
	PublishedAt   time.Time
	BodyHTML      template.HTML
	Media         mediaView  // optional lead media: image, video, or YouTube
	HeroImage     string     // compatibility alias for optional hero <img> src
	HeroAlt       string     // compatibility alias for optional hero alt text
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
	heading := scopeHeading(sc)
	title := pageTitle(heading, env.siteName, pg)
	canonical := absolute(env.siteBase, pg.Canonical)
	desc := scopeDescription(sc, env.siteName)
	ld, err := collectionJSONLD(heading, canonical, desc)
	if err != nil {
		return nil, err
	}
	view := pageView{
		headData: headData{
			Title:       title,
			Canonical:   canonical,
			Description: desc,
			SiteName:    env.siteName,
			OGType:      "website",
			TwitterCard: "summary",
			JSONLD:      ld,
			// Pure function of the scope, so it is byte-stable on sealed pages.
			ManifestURL: manifestURL(sc.ShardKey()),
			NavLinks:    navLinksForArticles(pg.Articles),
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
		view.Items = append(view.Items, itemViewOf(a))
	}
	view.Rail = railItems(view.Items, 5)
	if isAuthorScope(sc) {
		slug := sc.Section.Value
		view.AuthorName = env.plan.Index.authorName[slug]
		if view.AuthorName == "" {
			// No metadata.author_name on any carrier: fall back to the stored
			// author label so the bio block still has a heading.
			view.AuthorName = sc.Section.labelOr()
		}
		view.AuthorBio = env.plan.Index.authorBio[slug]
		if raw := env.plan.Index.authorAvatar[slug]; raw != "" {
			view.AuthorAvatar, _ = media.SafeMediaURL(env.siteBase, raw)
		}
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

// aboutView is the model for the /about/ page: the site head plus the roster of
// every author with at least one article.
type aboutView struct {
	headData
	Heading string
	Intro   string
	Authors []authorCardView
}

type authorCardView struct {
	Name    string
	URL     string
	Avatar  string
	Initial string
	Bio     string
}

// aboutHeading and aboutIntro are the Spanish title and lede of the /about/ page.
// aboutHeading also labels the nav link to /about/ (navLinksForArticles).
const (
	aboutHeading = "Autores"
	aboutIntro   = "Las firmas que escriben en El Censurado Web, en primera persona."
)

// aboutAuthorOrder is the editorial precedence of the Autores roster: the lead
// political voice first, then the opinion/markets and literary bylines, with the
// AI persona last. Author slugs not listed fall in afterwards in deterministic
// slug order, so a new persona still appears (just not ahead of the leads).
var aboutAuthorOrder = []string{"lara-arianna", "borge-luis-jorge", "glorieta-sadeta", "vector-omni"}

// orderedAuthorSlugs returns the author slugs present in the index, the curated
// aboutAuthorOrder first, then any remaining slugs in slug order.
func orderedAuthorSlugs(authors map[string][]int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(authors))
	for _, slug := range aboutAuthorOrder {
		if _, ok := authors[slug]; ok {
			out = append(out, slug)
			seen[slug] = true
		}
	}
	for _, slug := range sortedStringKeys(authors) {
		if !seen[slug] {
			out = append(out, slug)
		}
	}
	return out
}

// authorCards lists every author scope's display name, avatar, bio, and link to
// the single-author page, in the index's deterministic author-slug order.
func authorCards(env *buildEnv) []authorCardView {
	idx := env.plan.Index
	var out []authorCardView
	for _, slug := range orderedAuthorSlugs(idx.authors) {
		card := authorCardView{
			Name: idx.authorName[slug],
			URL:  pageURL("author/"+slug, 0),
			Bio:  idx.authorBio[slug],
		}
		if card.Name == "" {
			card.Name = idx.authorLabel[slug]
		}
		card.Initial = authorInitial(card.Name)
		if raw := idx.authorAvatar[slug]; raw != "" {
			card.Avatar, _ = media.SafeMediaURL(env.siteBase, raw)
		}
		out = append(out, card)
	}
	return out
}

// renderAbout renders the /about/ page listing every author.
func renderAbout(env *buildEnv) ([]byte, error) {
	canonical := absolute(env.siteBase, "/about/")
	ld, err := collectionJSONLD(aboutHeading, canonical, aboutIntro)
	if err != nil {
		return nil, err
	}
	view := aboutView{
		headData: headData{
			Title:       aboutHeading + " | " + env.siteName,
			Canonical:   canonical,
			Description: aboutIntro,
			SiteName:    env.siteName,
			OGType:      "website",
			TwitterCard: "summary",
			JSONLD:      ld,
			NavLinks:    navLinksForArticles(env.plan.Index.All),
		},
		Heading: aboutHeading,
		Intro:   aboutIntro,
		Authors: authorCards(env),
	}
	var buf bytes.Buffer
	if err := aboutTmpl.ExecuteTemplate(&buf, "base", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderArticle renders one permalink page.
func renderArticle(env *buildEnv, a domain.Article) ([]byte, error) {
	canonical := absolute(env.siteBase, articleURL(a))
	bodyHTML := env.bodyHTML[a.ContentHash]

	media := mediaForArticle(env.siteBase, a)
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
			Canonical:   canonical,
			Description: metaDescription(bodyHTML),
			SiteName:    env.siteName,
			OGType:      "article",
			OGImage:     ogImage,
			OGVideo:     ogVideo,
			TwitterCard: twitterCard,
			JSONLD:      ld,
			NavLinks:    navLinksForArticle(a),
		},
		Subtitle:      firstMetadataString(a.Metadata, "subtitle"),
		Standfirst:    firstMetadataString(a.Metadata, "description"),
		AuthorLabel:   authorDisplayLabel(a),
		AuthorURL:     facetURL("author", a.Author),
		AuthorSlug:    ShardEntryOf(a).Author,
		AuthorInitial: authorInitial(authorDisplayLabel(a)),
		AuthorAvatar:  metadataMediaSrc(env.siteBase, a.Metadata, "author_avatar", "avatar"),
		Section:       sectionLabelOf(a),
		SectionURL:    facetURL("section", a.Section),
		Topics:        topicLinksOf(a),
		PublishedAt:   a.PublishedAt,
		BodyHTML:      template.HTML(bodyHTML),
		Media:         media.view,
	}
	if media.view.Kind == "image" {
		view.HeroImage = media.view.Src
		view.HeroAlt = media.view.Alt
	}
	view.AuthorMore = articleAuthorMore(env.plan.Index, a, 6)
	view.Related = articleRelated(env.plan.Index, a, 4)
	var buf bytes.Buffer
	if err := articleTmpl.ExecuteTemplate(&buf, "base", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func itemViewOf(a domain.Article) itemView {
	// Reuse the canonical shard projection for the slug-form data-* hooks so the
	// client refiner's membership matches the server-rendered shards exactly.
	se := ShardEntryOf(a)
	return itemView{
		Title:         a.Title,
		Subtitle:      firstMetadataString(a.Metadata, "subtitle"),
		Description:   firstMetadataString(a.Metadata, "description"),
		URL:           articleURL(a),
		AuthorLabel:   authorDisplayLabel(a),
		AuthorURL:     facetURL("author", a.Author),
		AuthorSlug:    se.Author,
		AuthorInitial: authorInitial(authorDisplayLabel(a)),
		AuthorAvatar:  metadataMediaSrc("", a.Metadata, "author_avatar", "avatar"),
		Section:       sectionLabelOf(a),
		SectionURL:    facetURL("section", a.Section),
		SectionSlug:   se.Section,
		Topics:        topicLinksOf(a),
		TopicsAttr:    strings.Join(se.Topics, " "),
		MonthSlug:     monthKey(a.PublishedAt.Year(), int(a.PublishedAt.Month())),
		PublishedAt:   a.PublishedAt,
		Thumb:         thumbForArticle(a),
	}
}

func navLinksForArticles(arts []domain.Article) []navLink {
	links := []navLink{
		{Label: "Lo último", URL: "/latest/"},
		{Label: aboutHeading, URL: "/about/"},
	}
	seen := map[string]struct{}{"/latest/": {}, "/about/": {}}
	if s, ok := facetSlug("Lo último"); ok {
		seen["label:"+s] = struct{}{}
	}
	if s, ok := facetSlug(aboutHeading); ok {
		seen["label:"+s] = struct{}{}
	}
	for _, a := range arts {
		if len(links) >= 7 {
			break
		}
		if slug, ok := facetSlug(a.Section); ok {
			label := sectionDisplayLabel(Facet{Kind: AxisSection, Value: slug, Label: a.Section})
			addNavLink(&links, seen, label, facetURL("section", a.Section))
		}
	}
	for _, a := range arts {
		if len(links) >= 7 {
			break
		}
		for _, topic := range a.Topics {
			if len(links) >= 7 {
				break
			}
			if slug, ok := facetSlug(topic); ok {
				addNavLink(&links, seen, slug, facetURL("topic", topic))
			}
		}
	}
	return links
}

func navLinksForArticle(a domain.Article) []navLink {
	return navLinksForArticles([]domain.Article{a})
}

func addNavLink(links *[]navLink, seen map[string]struct{}, label, href string) {
	if href == "" {
		return
	}
	if _, ok := seen[href]; ok {
		return
	}
	// Also dedupe by display label (accent- and case-folded via facetSlug), so a
	// section and a topic that read the same in the menu (e.g. the section
	// "Política" and the topics "politica" / "política") do not both appear.
	var lkey string
	if s, ok := facetSlug(label); ok {
		lkey = "label:" + s
		if _, ok := seen[lkey]; ok {
			return
		}
	}
	seen[href] = struct{}{}
	if lkey != "" {
		seen[lkey] = struct{}{}
	}
	*links = append(*links, navLink{Label: label, URL: href})
}

func railItems(items []itemView, n int) []itemView {
	if n > len(items) {
		n = len(items)
	}
	out := make([]itemView, n)
	copy(out, items[:n])
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

func itemViewsOf(arts []domain.Article, n int) []itemView {
	if n > len(arts) {
		n = len(arts)
	}
	out := make([]itemView, 0, n)
	for _, a := range arts[:n] {
		out = append(out, itemViewOf(a))
	}
	return out
}

// articleAuthorMore is the "Más de este autor" lateral rail for a permalink: the
// same author's other articles, up to and excluding self (newest first).
// Restricting to "up to self" keeps the permalink byte-stable when the author
// publishes again later.
func articleAuthorMore(idx *Index, self domain.Article, n int) []itemView {
	var same []domain.Article
	for _, a := range articlesUpToSelf(idx, self) {
		if a.Author == self.Author {
			same = append(same, a)
		}
	}
	return itemViewsOf(same, n)
}

// articleRelated is the "Relacionados" block: articles up to self that share at
// least one topic with self (falling back to the same section when none do),
// newest first.
func articleRelated(idx *Index, self domain.Article, n int) []itemView {
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
	return itemViewsOf(related, n)
}

func thumbForArticle(a domain.Article) mediaView {
	src := metadataMediaSrc("", a.Metadata, "image")
	if src == "" {
		return mediaView{}
	}
	alt := firstMetadataString(a.Metadata, "image_alt", "alt")
	if alt == "" {
		alt = a.Title
	}
	return mediaView{Kind: "image", Src: src, Alt: alt}
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
// on the scope and site name, so it is byte-stable on sealed pages.
func scopeDescription(s Scope, siteName string) string {
	if s.isSingle() {
		switch s.Section.Kind {
		case AxisLatest:
			return "Los últimos artículos publicados en " + siteName + "."
		case AxisSection:
			return "Artículos de la sección " + facetDisplayLabel(s.Section) + " de " + siteName + "."
		case AxisAuthor:
			return "Artículos de " + facetDisplayLabel(s.Section) + " en " + siteName + "."
		case AxisTopic:
			return "Artículos etiquetados como " + facetDisplayLabel(s.Section) + " en " + siteName + "."
		case AxisMonth:
			return "Artículos publicados en " + monthKey(s.Section.Year, s.Section.Month) + " en " + siteName + "."
		}
	}
	return scopeHeading(s) + " en " + siteName + "."
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

func mediaForArticle(base string, a domain.Article) articleMedia {
	imgSrc, imgAbs := metadataMediaURL(base, a.Metadata, "image")
	imgAlt := firstMetadataString(a.Metadata, "image_alt", "alt")
	if imgAlt == "" {
		imgAlt = a.Title
	}

	if yt, ok := metadataString(a.Metadata, "youtube"); ok {
		if embed := media.YouTubeEmbedURL(yt); embed != "" {
			return articleMedia{
				view:    mediaView{Kind: "youtube", Src: embed, Title: "Vídeo: " + a.Title, Poster: imgSrc},
				ogImage: imgAbs,
				ogVideo: embed,
			}
		}
	}
	if yt, ok := metadataString(a.Metadata, "youtube_id"); ok {
		if embed := media.YouTubeEmbedURL(yt); embed != "" {
			return articleMedia{
				view:    mediaView{Kind: "youtube", Src: embed, Title: "Vídeo: " + a.Title, Poster: imgSrc},
				ogImage: imgAbs,
				ogVideo: embed,
			}
		}
	}
	if vidSrc, vidAbs := metadataMediaURL(base, a.Metadata, "video"); vidSrc != "" {
		return articleMedia{
			view:    mediaView{Kind: "video", Src: vidSrc, Title: "Vídeo: " + a.Title, Poster: imgSrc},
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

// sectionLabelOf is the reader-facing section label for one article: the Spanish
// display name when the slug is mapped, else the stored section string. The URL
// slug and data-section hook are unchanged (they keep the English slug).
func sectionLabelOf(a domain.Article) string {
	slug, ok := facetSlug(a.Section)
	if !ok {
		return a.Section
	}
	return sectionDisplayLabel(Facet{Kind: AxisSection, Value: slug, Label: a.Section})
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

func scopeHeading(s Scope) string {
	if s.isSingle() {
		switch s.Section.Kind {
		case AxisLatest:
			return "Lo último"
		case AxisSection, AxisAuthor, AxisTopic:
			return facetDisplayLabel(s.Section)
		case AxisMonth:
			return monthKey(s.Section.Year, s.Section.Month)
		}
	}
	sub := ""
	switch s.Sub.Kind {
	case AxisAuthor, AxisTopic:
		sub = facetDisplayLabel(s.Sub)
	case AxisMonth:
		sub = monthKey(s.Sub.Year, s.Sub.Month)
	}
	return facetDisplayLabel(s.Section) + " / " + sub
}

func pageTitle(heading, siteName string, pg Page) string {
	if pg.Number >= 1 {
		return fmt.Sprintf("%s (página %d) | %s", heading, pg.Number, siteName)
	}
	return heading + " | " + siteName
}

func feedLinksFor(siteBase, siteName string) []feedLink {
	return []feedLink{
		{Rel: "alternate", Type: "application/rss+xml", Href: absolute(siteBase, "/feed.xml"), Title: "Canal RSS de " + siteName},
		{Rel: "alternate", Type: "application/atom+xml", Href: absolute(siteBase, "/atom.xml"), Title: "Canal Atom de " + siteName},
		{Rel: "alternate", Type: "application/feed+json", Href: absolute(siteBase, "/feed.json"), Title: "Canal JSON de " + siteName},
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
