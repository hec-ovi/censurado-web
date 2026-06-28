package generate

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/hec-ovi/censurado-web-backend/content"
	"github.com/hec-ovi/censurado-web-backend/domain"
	"github.com/hec-ovi/censurado-web-backend/media"
)

// Inline body markers let an author drop a rich widget into the prose that the
// markdown renderer itself cannot carry (it runs without raw HTML and is then
// allowlist-sanitized, so any author-written <iframe>/<div> is stripped). Two
// markers, each best placed on its own line:
//
//	{{video:<youtube id | youtube/youtu.be/shorts url | self-hosted .mp4 path>}}
//	{{relacionado:<article slug>}}
//
// {{video:...}} renders a responsive, privacy-friendly embed (YouTube via the
// nocookie host, or a self-hosted <video>). {{relacionado:slug}} renders the "Ver
// artículo relacionado" card (title + trimmed subtitle + thumbnail) for another
// article in the corpus, resolved by its backend slug.
//
// Expansion is a sentinel sandwich around the markdown renderer: each marker is
// replaced in the RAW markdown by a plain-text sentinel (which goldmark cannot
// autolink or alter, unlike a bare URL), the markdown is rendered and sanitized,
// then each sentinel is swapped for the trusted, generator-built widget HTML. So
// the widgets are safe by construction and content-addressed: their payload comes
// from the immutable corpus, so a permalink stays byte-stable. An unresolved,
// self, or newer-than-self related target drops to nothing (the body stays
// deterministic).
var markerRe = regexp.MustCompile(`\{\{\s*(video|relacionado):\s*([^}]+?)\s*\}\}`)

var videoExts = []string{".mp4", ".webm", ".ogg", ".ogv", ".mov", ".m4v"}

// renderBodyWithMarkers renders one article's markdown body to sanitized HTML and
// expands its inline markers. bySlug maps every article's backend slug to the
// article; self is the article whose body this is (so a related marker never links
// forward or to itself).
func renderBodyWithMarkers(markdown string, bySlug map[string]domain.Article, self domain.Article) (string, error) {
	replacements := map[string]string{}
	n := 0
	pre := markerRe.ReplaceAllStringFunc(markdown, func(m string) string {
		sub := markerRe.FindStringSubmatch(m)
		kind, val := sub[1], strings.TrimSpace(sub[2])
		var frag string
		switch kind {
		case "video":
			frag = videoEmbedHTML(val)
		case "relacionado":
			frag = relatedCardFor(val, bySlug, self)
		}
		if frag == "" {
			return "" // drop an unresolved/unsafe marker entirely
		}
		sentinel := fmt.Sprintf("cnzmarker%dcnz", n)
		n++
		replacements[sentinel] = frag
		// On its own line so goldmark renders it as a standalone <p>sentinel</p>.
		return "\n\n" + sentinel + "\n\n"
	})

	out, err := content.RenderMarkdown(pre)
	if err != nil {
		return "", err
	}
	for sentinel, frag := range replacements {
		out = strings.ReplaceAll(out, "<p>"+sentinel+"</p>", frag)
		out = strings.ReplaceAll(out, sentinel, frag) // inline fallback
	}
	return out, nil
}

// videoEmbedHTML builds a responsive embed for a YouTube reference or a safe
// self-hosted video URL; an unrecognized or unsafe reference yields "" (the marker
// is dropped rather than rendered as broken markup).
func videoEmbedHTML(raw string) string {
	if embed := media.YouTubeEmbedURL(raw); embed != "" {
		return `<figure class="body-embed"><div class="embed-shell">` +
			`<iframe class="body-embed-frame" src="` + html.EscapeString(embed) +
			`" title="Vídeo" loading="lazy" allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"` +
			` allowfullscreen referrerpolicy="strict-origin-when-cross-origin"></iframe></div></figure>`
	}
	if src, _ := media.SafeMediaURL("", raw); src != "" && isVideoURL(src) {
		return `<figure class="body-embed"><video class="body-embed-video" src="` + html.EscapeString(src) +
			`" controls preload="metadata" playsinline></video></figure>`
	}
	return ""
}

func isVideoURL(s string) bool {
	s = strings.ToLower(s)
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	for _, ext := range videoExts {
		if strings.HasSuffix(s, ext) {
			return true
		}
	}
	return false
}

// relatedCardMax bounds the related card's subtitle excerpt; the title is never cut.
const relatedCardMax = 120

// relatedCardFor resolves a slug to the "Ver artículo relacionado" card HTML, or ""
// when the target is missing, is self, or is newer than self (a forward reference).
func relatedCardFor(slug string, bySlug map[string]domain.Article, self domain.Article) string {
	target, ok := bySlug[slug]
	if !ok || target.ID == self.ID || target.PublishedAt.After(self.PublishedAt) {
		return ""
	}
	title := html.EscapeString(strings.TrimSpace(target.Title))
	subtitle := truncateAtWord(firstMetadataString(target.Metadata, "subtitle"), relatedCardMax)
	thumb := metadataMediaSrc("", target.Metadata, "image")

	var b strings.Builder
	b.WriteString(`<aside class="related-card"><a class="related-card-link" href="`)
	b.WriteString(articleURL(target))
	b.WriteString(`"><span class="related-card-kicker">Ver artículo relacionado</span><span class="related-card-main">`)
	if thumb != "" {
		b.WriteString(`<span class="related-card-thumb"><img src="`)
		b.WriteString(html.EscapeString(thumb))
		b.WriteString(`" alt="" loading="lazy" decoding="async"></span>`)
	}
	b.WriteString(`<span class="related-card-text"><strong class="related-card-title">`)
	b.WriteString(title)
	b.WriteString(`</strong>`)
	if subtitle != "" {
		b.WriteString(`<span class="related-card-subtitle">`)
		b.WriteString(html.EscapeString(subtitle))
		b.WriteString(`</span>`)
	}
	b.WriteString(`</span></span></a></aside>`)
	return b.String()
}
