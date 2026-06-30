package generate

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"
)

// metaContent extracts the content attribute of a <meta {attr}="{val}" ...> tag.
func metaContent(t *testing.T, page, attr, val string) string {
	t.Helper()
	re := regexp.MustCompile(`<meta ` + attr + `="` + regexp.QuoteMeta(val) + `" content="([^"]*)"`)
	m := re.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no <meta %s=%q ...> in page", attr, val)
	}
	return m[1]
}

var jsonLDRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

func rawJSONLD(t *testing.T, page string) []byte {
	t.Helper()
	m := jsonLDRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no JSON-LD script in page")
	}
	return []byte(m[1])
}

func jsonLD(t *testing.T, page string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rawJSONLD(t, page), &out); err != nil {
		t.Fatalf("JSON-LD not valid JSON: %v", err)
	}
	return out
}

// D4: article SEO/social meta, derived only from the article's own fixed fields.
// Title carries a quote and a <script> to prove escaping in both og:title and JSON-LD.
func TestSEO_ArticleMeta(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{
		Title:     `Quote " and <script>alert(1)</script>`,
		Body:      "This is the article body with several plain words used to derive a meta description excerpt.",
		Author:    "Ada Lovelace",
		Section:   "tech",
		Published: date(2026, 6, 7),
	})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, articlePath(a)))

	canonical := absolute("https://news.example", articleURL(a))
	if got := metaContent(t, page, "property", "og:url"); got != canonical {
		t.Errorf("og:url = %q, want %q", got, canonical)
	}
	if got := metaContent(t, page, "property", "og:type"); got != "article" {
		t.Errorf("og:type = %q, want article", got)
	}
	if got := metaContent(t, page, "property", "og:site_name"); got != "Censurado" {
		t.Errorf("og:site_name = %q", got)
	}
	if got := metaContent(t, page, "name", "robots"); got != "index,follow" {
		t.Errorf("robots = %q, want index,follow", got)
	}
	if got := metaContent(t, page, "name", "twitter:card"); got != "summary" {
		t.Errorf("twitter:card = %q, want summary (no image)", got)
	}

	// og:title is HTML-escaped: no raw <script>, and it unescapes back to the title.
	ogTitle := metaContent(t, page, "property", "og:title")
	if strings.Contains(ogTitle, "<script>") {
		t.Errorf("og:title carries a raw <script>: %q", ogTitle)
	}
	if html.UnescapeString(ogTitle) != a.Title {
		t.Errorf("og:title unescapes to %q, want %q", html.UnescapeString(ogTitle), a.Title)
	}

	// JSON-LD: valid JSON, escaped (no raw <script> in the raw bytes), NewsArticle,
	// datePublished == the article's RFC3339 publish time, headline round-trips.
	if strings.Contains(string(rawJSONLD(t, page)), "<script>alert(1)") {
		t.Errorf("JSON-LD raw bytes leaked an unescaped <script>")
	}
	ld := jsonLD(t, page)
	if ld["@type"] != "NewsArticle" {
		t.Errorf("@type = %v, want NewsArticle", ld["@type"])
	}
	if ld["datePublished"] != a.PublishedAt.UTC().Format(time.RFC3339) {
		t.Errorf("datePublished = %v, want %q", ld["datePublished"], a.PublishedAt.UTC().Format(time.RFC3339))
	}
	if ld["headline"] != a.Title {
		t.Errorf("headline = %v, want %q", ld["headline"], a.Title)
	}
	if _, has := ld["image"]; has {
		t.Errorf("JSON-LD has image without metadata.image")
	}
}

// D4: listing pages carry CollectionPage JSON-LD and website og:type.
func TestSEO_ListingMeta(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "L One", Author: "ada", Section: "tech", Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, "section/tech/index.html"))

	if got := metaContent(t, page, "property", "og:type"); got != "website" {
		t.Errorf("listing og:type = %q, want website", got)
	}
	if got := metaContent(t, page, "property", "og:url"); got != "https://news.example/section/tech/" {
		t.Errorf("listing og:url = %q", got)
	}
	ld := jsonLD(t, page)
	if ld["@type"] != "CollectionPage" {
		t.Errorf("listing @type = %v, want CollectionPage", ld["@type"])
	}
	if ld["url"] != "https://news.example/section/tech/" {
		t.Errorf("CollectionPage url = %v", ld["url"])
	}
}

// D5: an article WITH metadata.image renders the hero figure and the image meta;
// one WITHOUT omits all of them (no empty tags).
func TestSEO_MediaImage(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	const img = "https://cdn.example/hero.jpg"
	withImg := seed(t, repo, seedSpec{
		Title: "Has Image", Author: "ada", Section: "tech", Published: date(2026, 6, 8),
		Metadata: map[string]any{"image": img},
	})
	without := seed(t, repo, seedSpec{Title: "No Image", Author: "ada", Section: "tech", Published: date(2026, 6, 9)})
	genInto(t, repo, out, nil)

	withPage := string(readArtifact(t, out, articlePath(withImg)))
	if !strings.Contains(withPage, `<figure class="hero">`) || !strings.Contains(withPage, `src="`+img+`"`) {
		t.Errorf("hero figure/img missing on imaged article")
	}
	if got := metaContent(t, withPage, "property", "og:image"); got != img {
		t.Errorf("og:image = %q, want %q", got, img)
	}
	if got := metaContent(t, withPage, "name", "twitter:image"); got != img {
		t.Errorf("twitter:image = %q, want %q", got, img)
	}
	if got := metaContent(t, withPage, "name", "twitter:card"); got != "summary_large_image" {
		t.Errorf("twitter:card = %q, want summary_large_image", got)
	}
	if imgs, ok := jsonLD(t, withPage)["image"].([]any); !ok || len(imgs) != 1 || imgs[0] != img {
		t.Errorf("JSON-LD image = %v, want [%q]", jsonLD(t, withPage)["image"], img)
	}

	withoutPage := string(readArtifact(t, out, articlePath(without)))
	if strings.Contains(withoutPage, `<figure class="hero">`) {
		t.Errorf("hero figure present without metadata.image")
	}
	for _, frag := range []string{`property="og:image"`, `name="twitter:image"`} {
		if strings.Contains(withoutPage, frag) {
			t.Errorf("imageless article emitted %q", frag)
		}
	}
	if got := metaContent(t, withoutPage, "name", "twitter:card"); got != "summary" {
		t.Errorf("imageless twitter:card = %q, want summary", got)
	}
	if _, has := jsonLD(t, withoutPage)["image"]; has {
		t.Errorf("imageless JSON-LD has an image")
	}
}

// D4: the meta description is a deterministic, word-boundary excerpt with a single
// trailing ellipsis when truncated; the body itself is never altered.
func TestSEO_MetaDescriptionExcerpt(t *testing.T) {
	long := strings.Repeat("alpha bravo charlie delta echo foxtrot ", 20)
	d := metaDescription("<p>" + long + "</p>")
	rd := []rune(d)
	if len(rd) > metaDescriptionMax+1 {
		t.Errorf("description %d runes, want <= %d", len(rd), metaDescriptionMax+1)
	}
	if rd[len(rd)-1] != '…' {
		t.Errorf("truncated description missing trailing ellipsis: %q", d)
	}
	if strings.ContainsAny(d, "<>") {
		t.Errorf("description still contains tags: %q", d)
	}
	if !strings.HasPrefix(d, "alpha bravo") {
		t.Errorf("description = %q", d)
	}
	if short := metaDescription("<p>Just a few words.</p>"); short != "Just a few words." {
		t.Errorf("short description = %q, want untruncated", short)
	}
}

// D5b: metadata.youtube renders the nocookie embed iframe + og:video and wins over a
// co-present image; metadata.video renders the native <video> with the image as the
// poster. This locks the mediaForArticle precedence and og:video emission after
// SafeMediaURL/YouTubeEmbedURL moved into internal/media.
func TestSEO_MediaYouTubeAndVideo(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	const embed = "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"
	const img = "https://cdn.example/poster.jpg"
	const vid = "https://cdn.example/clip.mp4"

	yt := seed(t, repo, seedSpec{
		Title: "Has YouTube", Author: "ada", Section: "tech", Published: date(2026, 6, 10),
		Metadata: map[string]any{"youtube": "https://youtu.be/dQw4w9WgXcQ", "image": img},
	})
	vidArt := seed(t, repo, seedSpec{
		Title: "Has Video", Author: "ada", Section: "tech", Published: date(2026, 6, 11),
		Metadata: map[string]any{"video": vid, "image": img},
	})
	genInto(t, repo, out, nil)

	ytPage := string(readArtifact(t, out, articlePath(yt)))
	if !strings.Contains(ytPage, `class="hero-embed"`) || !strings.Contains(ytPage, `src="`+embed+`"`) {
		t.Errorf("youtube article missing the nocookie embed iframe (src %q)", embed)
	}
	if got := metaContent(t, ytPage, "property", "og:video"); got != embed {
		t.Errorf("og:video = %q, want %q", got, embed)
	}
	// The youtube branch wins precedence over the co-present image: no <img> hero.
	if strings.Contains(ytPage, `class="hero-img"`) {
		t.Errorf("youtube article rendered an <img> hero; the youtube branch must win precedence")
	}

	vidPage := string(readArtifact(t, out, articlePath(vidArt)))
	if !strings.Contains(vidPage, `class="hero-video"`) || !strings.Contains(vidPage, `src="`+vid+`"`) {
		t.Errorf("video article missing the native <video> hero (src %q)", vid)
	}
	if !strings.Contains(vidPage, `poster="`+img+`"`) {
		t.Errorf("video article missing the image poster %q", img)
	}
	if got := metaContent(t, vidPage, "property", "og:video"); got != vid {
		t.Errorf("og:video = %q, want %q", got, vid)
	}
}

// An article emits a <meta name="keywords"> built from its topics plus any
// article-specific metadata.keywords, de-duplicated case-insensitively.
func TestSEO_Keywords(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{
		Title:     "Pieza con etiquetas",
		Body:      "Cuerpo con palabras suficientes para derivar una descripción razonable.",
		Author:    "ada",
		Section:   "politics",
		Topics:    []string{"elecciones", "fraude"},
		Published: date(2026, 6, 7),
		Metadata: map[string]any{
			"keywords": []any{"voto exterior", "elecciones"}, // "elecciones" duplicates a topic
		},
	})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, articlePath(a)))

	kw := metaContent(t, page, "name", "keywords")
	for _, want := range []string{"elecciones", "fraude", "voto exterior"} {
		if !strings.Contains(kw, want) {
			t.Errorf("keywords meta %q missing %q", kw, want)
		}
	}
	if strings.Count(kw, "elecciones") != 1 {
		t.Errorf("expected de-duplicated keywords, got %q", kw)
	}

	// A listing page carries no keywords meta (the tag is article-only).
	home := string(readArtifact(t, out, "latest/index.html"))
	if strings.Contains(home, `name="keywords"`) {
		t.Errorf("listing page should not emit a keywords meta")
	}
}
