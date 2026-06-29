package generate

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

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

// youtubePosterURL builds the deterministic YouTube poster (thumbnail) URL for a
// video id. hqdefault exists for every video, so the URL is a pure function of the
// id and a card built from it stays byte-stable.
func youtubePosterURL(id string) string {
	return "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg"
}

// firstBodyVideoPoster returns the YouTube poster URL for the FIRST {{video:...}}
// marker in the article body when that first video is a YouTube reference, or ""
// when the body has no video marker or its first video is self-hosted (a
// non-YouTube .mp4 has no poster, so the card stays text-only). It reuses
// media.YouTubeEmbedURL (the single id parser) so the listing thumb and the client
// shard entry agree on the same id, and only the FIRST video decides the card
// thumbnail even if a later marker is a YouTube one.
func firstBodyVideoPoster(a domain.Article) string {
	for _, m := range markerRe.FindAllStringSubmatch(a.Body, -1) {
		if m[1] != "video" {
			continue
		}
		val := strings.TrimSpace(m[2])
		if embed := media.YouTubeEmbedURL(val); embed != "" {
			id := strings.TrimPrefix(embed, "https://www.youtube-nocookie.com/embed/")
			return youtubePosterURL(id)
		}
		return "" // the first video is self-hosted (non-YouTube): no poster
	}
	return ""
}

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
	erased := metaBool(snap, "erased")
	verified := metaBool(snap, "verified")
	date := tweetDate(snap)

	var b strings.Builder
	b.WriteString(`<aside class="tweet-card">`)

	// Header: avatar, name (with the verified seal), @handle, the X brand.
	b.WriteString(`<div class="tweet-card-head">`)
	if avatar != "" {
		b.WriteString(`<img class="tweet-card-avatar" src="` + html.EscapeString(avatar) + `" alt="" loading="lazy" decoding="async">`)
	}
	b.WriteString(`<span class="tweet-card-id">`)
	if name != "" {
		b.WriteString(`<span class="tweet-card-name">` + html.EscapeString(name))
		if verified {
			b.WriteString(tweetBadgeSVG)
		}
		b.WriteString(`</span>`)
	}
	if handle != "" {
		b.WriteString(`<span class="tweet-card-handle">@` + html.EscapeString(handle) + `</span>`)
	}
	b.WriteString(`</span><span class="tweet-card-brand" aria-hidden="true">𝕏</span></div>`)

	// The captured text.
	if text != "" {
		b.WriteString(`<p class="tweet-card-text">` + html.EscapeString(text) + `</p>`)
	}
	if erased {
		b.WriteString(`<p class="tweet-card-erased">Publicación eliminada en X. Censurado conserva esta copia.</p>`)
	}

	// Meta line: the date (links to the original on X) and the view count.
	label := date
	if label == "" {
		label = "Ver en X"
		if erased {
			label = "Ver enlace original"
		}
	}
	b.WriteString(`<p class="tweet-card-meta">`)
	if link != "" {
		aria := "Ver en X"
		if erased {
			aria = "Ver enlace original"
		}
		b.WriteString(`<a class="tweet-card-date" href="` + html.EscapeString(link) + `" rel="nofollow noopener" target="_blank" aria-label="` + aria + `">` + html.EscapeString(label) + `</a>`)
	} else {
		b.WriteString(`<span class="tweet-card-date">` + html.EscapeString(label) + `</span>`)
	}
	if v, ok := metaInt(snap, "views"); ok && v > 0 {
		b.WriteString(` · <span class="tweet-card-views"><strong>` + humanizeCount(v) + `</strong> Views</span>`)
	}
	b.WriteString(`</p>`)

	// Stat row: replies, retweets, likes, bookmarks. Only when the snapshot carries
	// them; older snapshots without metrics simply omit the row.
	if stats := tweetStatsHTML(snap); stats != "" {
		b.WriteString(`<div class="tweet-card-stats">` + stats + `</div>`)
	}

	b.WriteString(`</aside>`)
	return b.String()
}

// X glyphs (24x24, currentColor) for the tweet card's stat row and verified seal.
const (
	tweetIcoReply    = `<svg class="tweet-card-ico" viewBox="0 0 24 24" aria-hidden="true"><path d="M1.751 10c0-4.42 3.584-8 8.005-8h4.366c4.49 0 8.129 3.64 8.129 8.13 0 2.96-1.607 5.68-4.196 7.11l-8.054 4.46v-3.69h-.067c-4.49.1-8.183-3.51-8.183-8.01z"/></svg>`
	tweetIcoRetweet  = `<svg class="tweet-card-ico" viewBox="0 0 24 24" aria-hidden="true"><path d="M4.5 3.88l4.432 4.14-1.364 1.46L5.5 7.55V16c0 1.1.896 2 2 2H13v2H7.5c-2.209 0-4-1.79-4-4V7.55L1.432 9.48.068 8.02 4.5 3.88zM16.5 6H11V4h5.5c2.209 0 4 1.79 4 4v8.45l2.068-1.93 1.364 1.46-4.432 4.14-4.432-4.14 1.364-1.46 2.068 1.93V8c0-1.1-.896-2-2-2z"/></svg>`
	tweetIcoLike     = `<svg class="tweet-card-ico" viewBox="0 0 24 24" aria-hidden="true"><path d="M16.697 5.5c-1.222-.06-2.679.51-3.89 2.16l-.805 1.09-.806-1.09C9.984 6.01 8.526 5.44 7.304 5.5c-1.243.07-2.349.78-2.91 1.91-.552 1.12-.633 2.78.479 4.82 1.074 1.97 3.257 4.27 7.129 6.61 3.87-2.34 6.052-4.64 7.126-6.61 1.111-2.04 1.03-3.7.477-4.82-.561-1.13-1.666-1.84-2.908-1.91z"/></svg>`
	tweetIcoBookmark = `<svg class="tweet-card-ico" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 4.5C4 3.12 5.119 2 6.5 2h11C18.881 2 20 3.12 20 4.5V22l-8-3.5L4 22V4.5z"/></svg>`
	tweetBadgeSVG    = `<svg class="tweet-card-badge" viewBox="0 0 24 24" aria-hidden="true"><path d="M22.25 12c0-1.43-.88-2.67-2.19-3.34.46-1.39.2-2.9-.81-3.91s-2.52-1.27-3.91-.81c-.66-1.31-1.91-2.19-3.34-2.19s-2.68.88-3.34 2.19c-1.39-.46-2.9-.2-3.91.81s-1.27 2.52-.81 3.91c-1.31.66-2.19 1.91-2.19 3.34s.88 2.67 2.19 3.34c-.46 1.39-.2 2.9.81 3.91s2.52 1.27 3.91.81c.66 1.31 1.91 2.19 3.34 2.19s2.68-.88 3.34-2.19c1.39.46 2.9.2 3.91-.81s1.27-2.52.81-3.91c1.31-.67 2.19-1.91 2.19-3.34zm-11.71 4.2L6.8 12.46l1.41-1.42 2.26 2.26 4.8-5.23 1.47 1.36-6.2 6.77z"/></svg>`
)

// tweetStatsHTML renders the reply/retweet/like/bookmark counts present in the snapshot,
// each as an icon plus its X-abbreviated count; an absent metric is skipped.
func tweetStatsHTML(snap map[string]any) string {
	stats := []struct{ key, ico, label string }{
		{"replies", tweetIcoReply, "respuestas"},
		{"retweets", tweetIcoRetweet, "reposteos"},
		{"likes", tweetIcoLike, "me gusta"},
		{"bookmarks", tweetIcoBookmark, "guardados"},
	}
	var b strings.Builder
	for _, s := range stats {
		n, ok := metaInt(snap, s.key)
		if !ok {
			continue
		}
		c := humanizeCount(n)
		b.WriteString(`<span class="tweet-card-stat" aria-label="` + c + ` ` + s.label + `">` + s.ico + `<span>` + c + `</span></span>`)
	}
	return b.String()
}

// tweetDate formats a snapshot's timestamp the way X shows it ("3:44 AM · Jun 28, 2026"),
// in Argentina time. It prefers the unix created_timestamp, then the native Twitter
// created_at string, and falls back to whatever created_at string is stored.
func tweetDate(snap map[string]any) string {
	loc := time.FixedZone("ART", -3*60*60)
	if ts, ok := metaInt(snap, "created_timestamp"); ok && ts > 0 {
		return time.Unix(int64(ts), 0).In(loc).Format("3:04 PM · Jan 2, 2006")
	}
	raw := strings.TrimSpace(metaStr(snap, "created_at"))
	if t, err := time.Parse("Mon Jan 02 15:04:05 -0700 2006", raw); err == nil {
		return t.In(loc).Format("3:04 PM · Jan 2, 2006")
	}
	return raw
}

// humanizeCount abbreviates a count the way X does: 942, 21.9K, 3.4M (one decimal,
// trailing .0 dropped).
func humanizeCount(n int) string {
	if n < 0 {
		n = 0
	}
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		return trimUnit(n, 1000) + "K"
	default:
		return trimUnit(n, 1_000_000) + "M"
	}
}

func trimUnit(n, unit int) string {
	whole := n / unit
	frac := (n % unit) * 10 / unit // one decimal, truncated the way X does
	if frac == 0 {
		return strconv.Itoa(whole)
	}
	return strconv.Itoa(whole) + "." + strconv.Itoa(frac)
}

// metaInt reads a numeric metadata field as an int (JSON numbers arrive as float64);
// the bool reports whether the key was present and numeric.
func metaInt(m map[string]any, key string) (int, bool) {
	switch v := m[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
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
