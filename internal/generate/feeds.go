package generate

import (
	"encoding/json"
	"encoding/xml"
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
)

// FeedArticle pairs an article with its already-rendered body HTML (reused from
// env.bodyHTML so the body is rendered once).
type FeedArticle struct {
	Article  domain.Article
	BodyHTML string
}

// FeedInput is the windowed, display-ordered input for the /latest/ feeds. The
// window is a presentation window, not an output cap: each item carries the FULL
// rendered article HTML.
type FeedInput struct {
	SiteURL     string
	Title       string
	Description string
	Updated     time.Time // newest item's truncated PublishedAt; zero only when empty
	Items       []FeedArticle
}

// feedTime normalizes the build timestamp to UTC, leaving the zero value alone.
func feedTime(updated time.Time) time.Time {
	if updated.IsZero() {
		return updated
	}
	return updated.UTC()
}

func marshalXML(v any) ([]byte, error) {
	b, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	out := append([]byte(xml.Header), b...)
	return append(out, '\n'), nil
}

// --- RSS 2.0 ---

type rssCDATA struct {
	Value string `xml:",cdata"`
}

type rssGUID struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type rssFeed struct {
	XMLName   xml.Name   `xml:"rss"`
	Version   string     `xml:"version,attr"`
	ContentNS string     `xml:"xmlns:content,attr"`
	DcNS      string     `xml:"xmlns:dc,attr"`
	Channel   rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	Items         []rssItem `xml:"item"`
}

type rssItem struct {
	Title      string   `xml:"title"`
	Link       string   `xml:"link"`
	GUID       rssGUID  `xml:"guid"`
	PubDate    string   `xml:"pubDate"`
	Creator    string   `xml:"dc:creator"`
	Categories []string `xml:"category"`
	Encoded    rssCDATA `xml:"content:encoded"`
}

func BuildRSS(in FeedInput) ([]byte, error) {
	ch := rssChannel{
		Title:       in.Title,
		Link:        in.SiteURL + "/latest/",
		Description: in.Description,
	}
	if !in.Updated.IsZero() {
		ch.LastBuildDate = feedTime(in.Updated).Format(time.RFC1123Z)
	}
	for _, it := range in.Items {
		a := it.Article
		link := absolute(in.SiteURL, articleURL(a))
		cats := append([]string{a.Section}, a.Topics...)
		ch.Items = append(ch.Items, rssItem{
			Title:      a.Title,
			Link:       link,
			GUID:       rssGUID{IsPermaLink: true, Value: link},
			PubDate:    a.PublishedAt.UTC().Format(time.RFC1123Z),
			Creator:    a.Author,
			Categories: cats,
			Encoded:    rssCDATA{Value: it.BodyHTML},
		})
	}
	feed := rssFeed{
		Version:   "2.0",
		ContentNS: "http://purl.org/rss/1.0/modules/content/",
		DcNS:      "http://purl.org/dc/elements/1.1/",
		Channel:   ch,
	}
	return marshalXML(feed)
}

// --- Atom 1.0 ---

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

type atomContent struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	ID         string         `xml:"id"`
	Links      []atomLink     `xml:"link"`
	Published  string         `xml:"published"`
	Updated    string         `xml:"updated"`
	Author     atomAuthor     `xml:"author"`
	Categories []atomCategory `xml:"category"`
	Content    atomContent    `xml:"content"`
}

func BuildAtom(in FeedInput) ([]byte, error) {
	latest := in.SiteURL + "/latest/"
	feed := atomFeed{
		Title: in.Title,
		ID:    latest,
		Links: []atomLink{
			{Rel: "alternate", Href: latest},
			{Rel: "self", Href: in.SiteURL + "/atom.xml"},
		},
	}
	if !in.Updated.IsZero() {
		feed.Updated = feedTime(in.Updated).Format(time.RFC3339)
	} else {
		feed.Updated = time.Time{}.Format(time.RFC3339)
	}
	for _, it := range in.Items {
		a := it.Article
		link := absolute(in.SiteURL, articleURL(a))
		published := a.PublishedAt.UTC().Format(time.RFC3339)
		cats := make([]atomCategory, 0, len(a.Topics)+1)
		cats = append(cats, atomCategory{Term: a.Section})
		for _, t := range a.Topics {
			cats = append(cats, atomCategory{Term: t})
		}
		feed.Entries = append(feed.Entries, atomEntry{
			Title:      a.Title,
			ID:         link,
			Links:      []atomLink{{Rel: "alternate", Href: link}},
			Published:  published,
			Updated:    published,
			Author:     atomAuthor{Name: a.Author},
			Categories: cats,
			Content:    atomContent{Type: "html", Value: it.BodyHTML},
		})
	}
	return marshalXML(feed)
}

// --- JSON Feed 1.1 ---

type jsonFeed struct {
	Version     string         `json:"version"`
	Title       string         `json:"title"`
	HomePageURL string         `json:"home_page_url"`
	FeedURL     string         `json:"feed_url"`
	Description string         `json:"description,omitempty"`
	Items       []jsonFeedItem `json:"items"`
}

type jsonFeedItem struct {
	ID            string           `json:"id"`
	URL           string           `json:"url"`
	Title         string           `json:"title"`
	ContentHTML   string           `json:"content_html"`
	DatePublished string           `json:"date_published"`
	Authors       []jsonFeedAuthor `json:"authors"`
	Tags          []string         `json:"tags,omitempty"`
}

type jsonFeedAuthor struct {
	Name string `json:"name"`
}

func BuildJSONFeed(in FeedInput) ([]byte, error) {
	feed := jsonFeed{
		Version:     "https://jsonfeed.org/version/1.1",
		Title:       in.Title,
		HomePageURL: in.SiteURL + "/",
		FeedURL:     in.SiteURL + "/feed.json",
		Description: in.Description,
		Items:       []jsonFeedItem{},
	}
	for _, it := range in.Items {
		a := it.Article
		link := absolute(in.SiteURL, articleURL(a))
		item := jsonFeedItem{
			ID:            link,
			URL:           link,
			Title:         a.Title,
			ContentHTML:   it.BodyHTML,
			DatePublished: a.PublishedAt.UTC().Format(time.RFC3339),
			Authors:       []jsonFeedAuthor{{Name: a.Author}},
		}
		if len(a.Topics) > 0 {
			item.Tags = append([]string{}, a.Topics...)
		}
		feed.Items = append(feed.Items, item)
	}
	b, err := json.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
