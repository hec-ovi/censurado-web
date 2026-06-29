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
// allowlist-sanitized, so any author-written <iframe>/<div> is stripped). Three
// markers, each best placed on its own line:
//
//	{{video:<youtube id | youtube/youtu.be/shorts url | self-hosted .mp4 path>}}
//	{{relacionado:<article slug>}}
//	{{tweet:<tweet id>}}
//
// {{video:...}} renders a responsive, privacy-friendly embed (YouTube via the
// nocookie host, or a self-hosted <video>). {{relacionado:slug}} renders the "Ver
// artículo relacionado" card (title + trimmed subtitle + thumbnail) for another
// article in the corpus, resolved by its backend slug. {{tweet:id}} renders an
// X-style card from a snapshot stored in metadata.tweets[], so the quote survives
// the original post being deleted (the card then shows the retained text plus a
// "publicación eliminada" note and the original link).
//
// A {{video:id}} whose id is recorded unavailable in metadata.media_checks renders
// a "este video fue eliminado" placeholder (keeping the original link) instead of a
// dead iframe. Removal is detected out of band by a brain re-check sweep that writes
// the flag into metadata; the generator is static and never fetches at build time.
//
// Expansion is a sentinel sandwich around the markdown renderer: each marker is
// replaced in the RAW markdown by a plain-text sentinel (which goldmark cannot
// autolink or alter, unlike a bare URL), the markdown is rendered and sanitized,
// then each sentinel is swapped for the trusted, generator-built widget HTML. So
// the widgets are safe by construction and content-addressed: their payload comes
// from the immutable corpus, so a permalink stays byte-stable. An unresolved,
// self, or newer-than-self related target drops to nothing (the body stays
// deterministic).
var markerRe = regexp.MustCompile(`\{\{\s*(video|relacionado|tweet):\s*([^}]+?)\s*\}\}`)

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
			frag = videoEmbedHTML(val, self)
		case "relacionado":
			frag = relatedCardFor(val, bySlug, self)
		case "tweet":
			frag = tweetCardHTML(val, self)
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
// is dropped rather than rendered as broken markup). When the YouTube id is recorded
// unavailable in self's metadata.media_checks, it renders a "video eliminado"
// placeholder that keeps the original link instead of a dead iframe.
func videoEmbedHTML(raw string, self domain.Article) string {
	if embed := media.YouTubeEmbedURL(raw); embed != "" {
		id := strings.TrimPrefix(embed, "https://www.youtube-nocookie.com/embed/")
		if youtubeUnavailable(self.Metadata, id) {
			return youtubeRemovedHTML(id)
		}
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

// youtubeRemovedHTML is the placeholder shown in place of an embed when a cited
// YouTube video is no longer available; it keeps the original watch link.
func youtubeRemovedHTML(id string) string {
	watch := "https://www.youtube.com/watch?v=" + id
	return `<figure class="body-embed"><div class="embed-removed">` +
		`<p class="embed-removed-note">Este video fue eliminado de YouTube.</p>` +
		`<a class="embed-removed-link" href="` + html.EscapeString(watch) +
		`" rel="nofollow noopener" target="_blank">Ver enlace original</a></div></figure>`
}

// youtubeUnavailable reports whether metadata.media_checks[id].available is the
// literal false. Absent/unknown ids default to available (so an embed without a
// recorded check still renders normally).
func youtubeUnavailable(m map[string]any, id string) bool {
	checks, ok := m["media_checks"].(map[string]any)
	if !ok {
		return false
	}
	entry, ok := checks[id].(map[string]any)
	if !ok {
		return false
	}
	avail, ok := entry["available"].(bool)
	return ok && !avail
}

// tweetCardHTML renders an X-style card from a snapshot in self.metadata.tweets[]
// whose id matches ref, or "" when no snapshot is found. The snapshot is rendered
// verbatim (never fetched live), so the card survives the original being deleted;
// an erased snapshot keeps the captured text and adds a "publicación eliminada"
// note plus the retained original link.
func tweetCardHTML(ref string, self domain.Article) string {
	snap := findTweet(self.Metadata, ref)
	if snap == nil {
		return ""
	}
	text := strings.TrimSpace(metaStr(snap, "text"))
	link, _ := media.SafeMediaURL("", strings.TrimSpace(metaStr(snap, "url")))
	if text == "" && link == "" {
		return "" // nothing meaningful to show
	}
	name := strings.TrimSpace(metaStr(snap, "name"))
	handle := strings.TrimPrefix(strings.TrimSpace(metaStr(snap, "handle")), "@")
	avatar, _ := media.SafeMediaURL("", strings.TrimSpace(metaStr(snap, "avatar")))
	created := strings.TrimSpace(metaStr(snap, "created_at"))
	erased := metaBool(snap, "erased")

	var b strings.Builder
	b.WriteString(`<aside class="tweet-card">`)
	b.WriteString(`<div class="tweet-card-head">`)
	if avatar != "" {
		b.WriteString(`<img class="tweet-card-avatar" src="` + html.EscapeString(avatar) + `" alt="" loading="lazy" decoding="async">`)
	}
	b.WriteString(`<span class="tweet-card-id">`)
	if name != "" {
		b.WriteString(`<strong class="tweet-card-name">` + html.EscapeString(name) + `</strong>`)
	}
	if handle != "" {
		b.WriteString(`<span class="tweet-card-handle">@` + html.EscapeString(handle) + `</span>`)
	}
	b.WriteString(`</span><span class="tweet-card-brand" aria-hidden="true">𝕏</span></div>`)
	if text != "" {
		b.WriteString(`<p class="tweet-card-text">` + html.EscapeString(text) + `</p>`)
	}
	if erased {
		b.WriteString(`<p class="tweet-card-erased">Publicación eliminada en X. Censurado conserva esta copia.</p>`)
	}
	if link != "" {
		label := "Ver en X"
		if erased {
			label = "Ver enlace original"
		}
		b.WriteString(`<a class="tweet-card-link" href="` + html.EscapeString(link) + `" rel="nofollow noopener" target="_blank">` + label)
		if created != "" {
			b.WriteString(` · <time>` + html.EscapeString(created) + `</time>`)
		}
		b.WriteString(`</a>`)
	}
	b.WriteString(`</aside>`)
	return b.String()
}

// findTweet returns the metadata.tweets[] entry whose "id" equals ref, or nil.
func findTweet(m map[string]any, ref string) map[string]any {
	ref = strings.TrimSpace(ref)
	list, ok := m["tweets"].([]any)
	if !ok {
		return nil
	}
	for _, e := range list {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(metaStr(entry, "id")) == ref {
			return entry
		}
	}
	return nil
}

// metaStr reads a string field from a metadata object (empty when absent/non-string).
func metaStr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// metaBool reads a bool field from a metadata object (false when absent/non-bool).
func metaBool(m map[string]any, key string) bool {
	b, _ := m[key].(bool)
	return b
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
