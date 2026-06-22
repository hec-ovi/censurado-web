package generate

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web/internal/content"
	"github.com/hec-ovi/censurado-web/internal/domain"
)

func feedArticle(t *testing.T, title, body, author, section string, topics []string, pub time.Time) FeedArticle {
	t.Helper()
	in := domain.PublishInput{Title: title, Body: body, Author: author, Section: section, Topics: topics, PublishedAt: &pub}
	a, err := domain.NewArticle(in, pin)
	if err != nil {
		t.Fatalf("NewArticle: %v", err)
	}
	a.ID = "1"
	a.PublishedAt = a.PublishedAt.UTC().Truncate(time.Second)
	html, err := content.RenderMarkdown(body)
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	return FeedArticle{Article: a, BodyHTML: html}
}

func sampleFeedInput(t *testing.T) FeedInput {
	t.Helper()
	fa := feedArticle(t, "Feed Title", "**bold** body text", "Ada", "tech", []string{"ai", "go"}, date(2026, 6, 10))
	return FeedInput{
		SiteURL:     "https://news.example",
		Title:       "Censurado",
		Description: "Latest",
		Updated:     fa.Article.PublishedAt,
		Items:       []FeedArticle{fa},
	}
}

// F1
func TestBuildRSS_Wellformed(t *testing.T) {
	in := sampleFeedInput(t)
	b, err := BuildRSS(in)
	if err != nil {
		t.Fatalf("BuildRSS: %v", err)
	}
	var doc struct {
		Channel struct {
			Link          string `xml:"link"`
			LastBuildDate string `xml:"lastBuildDate"`
			Items         []struct {
				Title      string   `xml:"title"`
				Link       string   `xml:"link"`
				GUID       string   `xml:"guid"`
				PubDate    string   `xml:"pubDate"`
				Creator    string   `xml:"creator"`
				Categories []string `xml:"category"`
				Encoded    string   `xml:"encoded"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("RSS not well-formed: %v", err)
	}
	if doc.Channel.Link != "https://news.example/latest/" {
		t.Errorf("channel link = %q", doc.Channel.Link)
	}
	if len(doc.Channel.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(doc.Channel.Items))
	}
	it := doc.Channel.Items[0]
	wantLink := absolute(in.SiteURL, articleURL(in.Items[0].Article))
	if it.GUID != wantLink || it.Link != wantLink {
		t.Errorf("guid/link = %q/%q, want %q", it.GUID, it.Link, wantLink)
	}
	if _, err := time.Parse(time.RFC1123Z, it.PubDate); err != nil {
		t.Errorf("pubDate %q not RFC1123Z: %v", it.PubDate, err)
	}
	if it.Encoded != in.Items[0].BodyHTML {
		t.Errorf("content:encoded != full body:\n got %q\n want %q", it.Encoded, in.Items[0].BodyHTML)
	}
	if !strings.Contains(it.Encoded, "<strong>bold</strong>") {
		t.Errorf("body not fully rendered: %q", it.Encoded)
	}
	wantCats := map[string]bool{"tech": true, "ai": true, "go": true}
	if len(it.Categories) != 3 {
		t.Errorf("categories = %v, want section+topics", it.Categories)
	}
	for _, c := range it.Categories {
		if !wantCats[c] {
			t.Errorf("unexpected category %q", c)
		}
	}
}

// F2
func TestBuildAtom_Wellformed(t *testing.T) {
	in := sampleFeedInput(t)
	b, err := BuildAtom(in)
	if err != nil {
		t.Fatalf("BuildAtom: %v", err)
	}
	var doc struct {
		ID      string `xml:"id"`
		Updated string `xml:"updated"`
		Entries []struct {
			ID      string `xml:"id"`
			Updated string `xml:"updated"`
			Content struct {
				Type string `xml:"type,attr"`
				Body string `xml:",chardata"`
			} `xml:"content"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("Atom not well-formed: %v", err)
	}
	if doc.ID != "https://news.example/latest/" {
		t.Errorf("feed id = %q", doc.ID)
	}
	if _, err := time.Parse(time.RFC3339, doc.Updated); err != nil {
		t.Errorf("feed updated %q not RFC3339: %v", doc.Updated, err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("entries = %d", len(doc.Entries))
	}
	wantLink := absolute(in.SiteURL, articleURL(in.Items[0].Article))
	if doc.Entries[0].ID != wantLink {
		t.Errorf("entry id = %q, want %q", doc.Entries[0].ID, wantLink)
	}
	if doc.Entries[0].Content.Type != "html" || doc.Entries[0].Content.Body != in.Items[0].BodyHTML {
		t.Errorf("entry content = %+v, want full html body", doc.Entries[0].Content)
	}
}

// F3
func TestBuildJSONFeed_Wellformed(t *testing.T) {
	in := sampleFeedInput(t)
	b, err := BuildJSONFeed(in)
	if err != nil {
		t.Fatalf("BuildJSONFeed: %v", err)
	}
	var doc struct {
		Version string `json:"version"`
		Items   []struct {
			ContentHTML   string `json:"content_html"`
			DatePublished string `json:"date_published"`
			Authors       []struct {
				Name string `json:"name"`
			} `json:"authors"`
			Tags []string `json:"tags"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("JSON Feed invalid: %v", err)
	}
	if doc.Version != "https://jsonfeed.org/version/1.1" {
		t.Errorf("version = %q", doc.Version)
	}
	if len(doc.Items) != 1 {
		t.Fatalf("items = %d", len(doc.Items))
	}
	it := doc.Items[0]
	if it.ContentHTML != in.Items[0].BodyHTML {
		t.Errorf("content_html != full body")
	}
	if _, err := time.Parse(time.RFC3339, it.DatePublished); err != nil {
		t.Errorf("date_published %q not RFC3339: %v", it.DatePublished, err)
	}
	if len(it.Authors) != 1 || it.Authors[0].Name != "Ada" {
		t.Errorf("authors = %v, want [Ada]", it.Authors)
	}
	wantTags := map[string]bool{"ai": true, "go": true}
	if len(it.Tags) != 2 {
		t.Errorf("tags = %v, want topics", it.Tags)
	}
	for _, tg := range it.Tags {
		if !wantTags[tg] {
			t.Errorf("unexpected tag %q", tg)
		}
	}
}

// F4
func TestFeeds_Window_NotCap(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	bodies := map[string]string{}
	for i := 0; i < 4; i++ {
		a := seed(t, repo, seedSpec{
			Title:     "Window " + string(rune('A'+i)),
			Body:      "Body **number** " + string(rune('A'+i)) + " with enough words to detect truncation.",
			Author:    "ada",
			Section:   "tech",
			Published: date(2026, 6, 1).Add(time.Duration(i) * 24 * time.Hour),
		})
		html, _ := content.RenderMarkdown(a.Body)
		bodies[absolute("https://news.example", articleURL(a))] = html
	}
	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 })

	var jf struct {
		Items []struct {
			URL         string `json:"url"`
			ContentHTML string `json:"content_html"`
		} `json:"items"`
	}
	feedBytes := readArtifact(t, out, "feed.json")
	if err := json.Unmarshal(feedBytes, &jf); err != nil {
		t.Fatalf("decode feed.json: %v", err)
	}
	if len(jf.Items) != 2 {
		t.Fatalf("feed items = %d, want window P=2", len(jf.Items))
	}
	for _, it := range jf.Items {
		full := bodies[it.URL]
		if it.ContentHTML != full {
			t.Errorf("item body truncated:\n got %q\n want %q", it.ContentHTML, full)
		}
	}
	// No length/cap field anywhere in any feed document.
	for _, f := range []string{"feed.json", "feed.xml", "atom.xml"} {
		s := string(readArtifact(t, out, f))
		for _, forbidden := range []string{"maxlength", "max_length", "maxItems", "max_items", "\"length\""} {
			if strings.Contains(s, forbidden) {
				t.Errorf("%s contains a length/cap field %q", f, forbidden)
			}
		}
	}
}

// F5
func TestFeeds_Idempotent(t *testing.T) {
	in := sampleFeedInput(t)
	for _, build := range []func(FeedInput) ([]byte, error){BuildRSS, BuildAtom, BuildJSONFeed} {
		a, err := build(in)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		b, err := build(in)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if string(a) != string(b) {
			t.Errorf("feed not byte-identical across builds")
		}
	}
}

// K1
func TestSitemap_TierAOnly(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Exactly P=2 latest articles -> the latest scope is rem==0.
	seed(t, repo, seedSpec{Title: "Sitemap A", Author: "ada", Section: "tech", Published: date(2026, 6, 1)})
	seed(t, repo, seedSpec{Title: "Sitemap B", Author: "ada", Section: "tech", Published: date(2026, 6, 2)})
	genInto(t, repo, out, func(o *Options) { o.PageSize = 2 })

	var doc struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	// sitemap.xml is now a <sitemapindex>; the Tier-A urlset lives in the listings
	// child sitemap.
	if err := xml.Unmarshal(readArtifact(t, out, "sitemaps/listings.xml"), &doc); err != nil {
		t.Fatalf("listings sitemap not well-formed: %v", err)
	}
	locs := map[string]bool{}
	for _, u := range doc.URLs {
		locs[u.Loc] = true
		if strings.Contains(u.Loc, "/a/") {
			t.Errorf("sitemap lists a permalink: %q", u.Loc)
		}
		if strings.HasSuffix(u.Loc, ".json") || strings.Contains(u.Loc, "/shards/") {
			t.Errorf("sitemap lists a shard/json URL: %q", u.Loc)
		}
		if strings.Contains(u.Loc, "feed") || strings.Contains(u.Loc, "atom") {
			t.Errorf("sitemap lists a feed URL: %q", u.Loc)
		}
	}
	// rem==0 seam: the sealed page is listed, the duplicate landing is not.
	if !locs["https://news.example/latest/page/1/"] {
		t.Errorf("sitemap missing /latest/page/1/ (rem==0 sealed page)")
	}
	if locs["https://news.example/latest/"] {
		t.Errorf("sitemap lists the duplicate rem==0 landing /latest/")
	}

	// The 3 feed alternates appear in the /latest/ landing head, not in an article page.
	latestHead := string(readArtifact(t, out, "latest/index.html"))
	for _, f := range []string{"/feed.xml", "/atom.xml", "/feed.json"} {
		if !strings.Contains(latestHead, `href="https://news.example`+f+`"`) {
			t.Errorf("/latest/ head missing feed alternate %q", f)
		}
	}
	plan, _ := BuildPlan(context.Background(), repo, 2)
	art := plan.Index.All[0]
	artHTML := string(readArtifact(t, out, articlePath(art)))
	if strings.Contains(artHTML, `type="application/rss+xml"`) {
		t.Errorf("article page should not carry feed alternates")
	}
}
