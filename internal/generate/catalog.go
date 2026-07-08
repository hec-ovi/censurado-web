package generate

// This file is the single source of the site's reader-facing UI strings. Every
// chrome/label string the generator renders is authored here once, with its
// English base value (En) and its Spanish translation (Es).
//
// Two things read this catalog:
//  1. The generator's render fallback. defaultText (key -> Es) is consulted when a
//     row is absent from the frontend_text table, so an UNSEEDED database renders
//     byte-identically to the pre-catalog build (today's live Spanish site). The
//     DB overlays this per key and per language (see text.go).
//  2. The seedtext command. FrontendTextSeed() returns every entry so the operator
//     can populate frontend_text with the English base and the Spanish rows, which
//     is also the source the one-time translate skill reads to add more languages.
//
// The base language is English (En); Spanish is a loaded translation (Es). The
// live Argentine site renders with -lang es, so Es is what readers see. Values
// that carry a %s/%d verb are fmt.Sprintf'd by the caller with the site name,
// facet label, page number, etc.; the verbs and their order are identical across
// languages.

// TextSeedEntry is one UI string: its stable lookup Key, the English base value,
// and the Spanish translation.
type TextSeedEntry struct {
	Key string
	En  string
	Es  string
}

// frontendSeed is the canonical, ordered catalog. Order is display-only (grouped
// by surface) and does not affect lookup.
var frontendSeed = []TextSeedEntry{
	// --- Global chrome: skip link, header nav, theme switch, footer ---
	{"nav.skip_link", "Skip to content", "Saltar al contenido"},
	{"nav.menu_toggle", "Menu", "Menú"},
	{"nav.theme_group_aria", "Theme", "Tema"},
	{"nav.theme_system", "System", "Sistema"},
	{"nav.theme_light", "Light", "Claro"},
	{"nav.theme_dark", "Dark", "Oscuro"},
	{"nav.logo_aria", "El Censurado Web, go to homepage", "El Censurado Web, ir a portada"},
	{"nav.youtube_aria", "El Censurado Web YouTube channel", "Canal de YouTube de El Censurado Web"},
	{"nav.landmark_aria", "Primary", "Principal"},
	{"nav.latest", "Latest", "Lo último"},
	{"nav.about", "About", "Nosotros"},
	{"footer.tagline", "Independent AI-assisted dispatches on technology, power, markets, and politics.", "Despachos independientes asistidos por IA sobre tecnología, poder, mercados y política."},
	{"footer.disclosure_heading", "Editorial notice", "Aviso editorial"},
	{"footer.disclosure_body_1", "El Censurado Web is an AI-assisted media project. Articles may be generated, summarized, translated, structured, or enriched by automated systems and are published for informational analysis, not as professional advice.", "El Censurado Web es un proyecto de medios asistido por IA. Los artículos pueden ser generados, resumidos, traducidos, estructurados o enriquecidos con sistemas automatizados y se publican para análisis informativo, no como asesoramiento profesional."},
	{"footer.disclosure_body_2", "We aim to review and contextualize the material before publishing, but automated output may contain inaccuracies, omissions, outdated context, or unsupported claims. Readers should verify relevant facts against primary sources before relying on them.", "Procuramos revisar y contextualizar el material antes de su publicación, pero el resultado automatizado puede incluir inexactitudes, omisiones, contexto desactualizado o afirmaciones sin respaldo. Los lectores deben verificar los hechos relevantes con fuentes primarias antes de basarse en ellos."},

	// --- Section labels: ONE source for the section vocabulary. The top nav, the
	// listing headings, the card kickers, and the JSON shards all read these, so a
	// slug reads the same everywhere (fixing the old nav "Internacionales" vs
	// heading "Mundo" split for the world section). ---
	{"section.tech.label", "Technology", "Tecnología"},
	{"section.world.label", "World", "Mundo"},
	{"section.politics.label", "Politics", "Política"},
	{"section.misterio-y-conspiracion.label", "Mystery and conspiracy", "Misterio y conspiración"},
	{"section.economy.label", "Economy", "Economía"},
	{"section.crypto.label", "Crypto", "Cripto"},
	{"section.literatura.label", "Culture and literature", "Cultura y literatura"},

	// --- Listing pages: hero, recomendado rail, pager, month navigator ---
	{"listing.eyebrow", "Home", "Portada"},
	{"listing.facets_aria", "Home", "Portada"},
	{"rail.recomendado_aria", "Recommended", "Recomendado"},
	{"rail.recomendado_heading", "Recommended", "Recomendado"},
	{"pagination.nav_aria", "Pages", "Páginas"},
	{"pagination.months_nav_aria", "Months", "Meses"},

	// --- Author profile listing + Nosotros roster chrome ---
	{"author.profile_kicker", "Profile", "Perfil"},
	{"author.topics_aria", "Topics covered", "Temas que cubre"},
	{"author.topics_label", "Topics", "Temas"},
	{"author.about_aria", "About %s", "Sobre %s"},
	{"author.about_label", "About", "Sobre"},
	{"author.generated_aria", "Generated content", "Contenido generado"},
	{"author.generated_heading", "Latest posts", "Últimas publicaciones"},
	{"empty.author_no_articles", "No articles published yet.", "Todavía no publicó artículos."},

	// --- Article view: share, byline, reactions, related, author-more ---
	{"article.share_nav_aria", "Share", "Compartir"},
	{"share.x_aria", "Share on X", "Compartir en X"},
	{"share.whatsapp_aria", "Share on WhatsApp", "Compartir en WhatsApp"},
	{"share.telegram_aria", "Share on Telegram", "Compartir en Telegram"},
	{"share.linkedin_aria", "Share on LinkedIn", "Compartir en LinkedIn"},
	{"share.facebook_aria", "Share on Facebook", "Compartir en Facebook"},
	{"reactions.prompt", "What did you think of this piece?", "¿Qué te pareció esta nota?"},
	{"reactions.like_aria", "Like", "Me gusta"},
	{"reactions.dislike_aria", "Dislike", "No me gusta"},
	{"author.more_heading", "More from this author", "Más de este autor"},
	{"related.heading", "Related", "Relacionados"},
	{"byline.by", "By ", "Por "},
	{"byline.in", " in ", " en "},

	// --- About page ---
	{"about.heading", "About", "Nosotros"},
	{"about.meta_description", "The first fully synthetic AI news portal: research, cross-validation, and specialized voices without a traditional human newsroom.", "El primer portal de noticias plenamente sintético por IA: investigación, validación cruzada y voces especializadas sin una redacción humana tradicional."},
	{"about.kicker", "The synthetic newsroom", "La redacción sintética"},
	{"about.summary", "The voices, the method, and the sources behind El Censurado Web.", "Las voces, el método y las fuentes detrás de El Censurado Web."},
	{"about.manifesto_1", "El Censurado Web is the first fully synthetic news portal: articles, bylines, and analysis produced by AI, with no human newsroom writing behind them.", "El Censurado Web es el primer portal de noticias plenamente sintético: artículos, firmas y análisis producidos por IA, sin una redacción humana escribiendo detrás."},
	{"about.manifesto_2", "We protect the reader's attention with concentrated information and layered reading. Each piece cross-checks independent sources, researches in depth, and passes two rounds of self-review before publishing; the synthetic personas cover a range of topics in their own voice.", "Cuidamos la atención del lector con información concentrada y lectura por capas. Cada pieza cruza fuentes independientes, investiga en profundidad y atraviesa dos rondas de autorrevisión antes de publicarse; las personas sintéticas cubren diversos temas con voz propia."},
	{"about.manifesto_aria", "Manifesto", "Manifiesto"},
	{"about.people_aria", "People", "Personas"},

	// --- 404 page ---
	{"notfound.eyebrow", "Error 404", "Error 404"},
	{"notfound.heading", "Oops! We couldn't find this page", "¡Ups! No encontramos esta página"},
	{"notfound.meta_description", "The page you are looking for does not exist or was moved. Go back to the homepage to keep reading.", "La página que buscás no existe o se movió. Volvé a la portada para seguir leyendo."},
	{"notfound.home_cta", "Back to the homepage", "Volver a la portada"},
	{"notfound.body_1", "The address you opened does not exist, changed, or went offline.", "La dirección que abriste no existe, cambió o quedó fuera de línea."},
	{"notfound.body_2", "The link may be misspelled or the page may have moved.", "Puede que el enlace esté mal escrito o que la página se haya movido."},

	// --- SEO meta descriptions and page titles (%s = facet label / site name;
	// %d = page number). Verb order is fixed across languages. ---
	{"meta.desc_latest", "The latest articles published on %s.", "Los últimos artículos publicados en %s."},
	{"meta.desc_section", "Articles in the %s section of %s.", "Artículos de la sección %s de %s."},
	{"meta.desc_author", "Articles by %s on %s.", "Artículos de %s en %s."},
	{"meta.desc_topic", "Articles tagged %s on %s.", "Artículos etiquetados como %s en %s."},
	{"meta.desc_month", "Articles published in %s on %s.", "Artículos publicados en %s en %s."},
	{"meta.desc_generic", "%s on %s.", "%s en %s."},
	{"meta.title", "%s | %s", "%s | %s"},
	{"meta.title_paged", "%s (page %d) | %s", "%s (página %d) | %s"},

	// --- Feeds (RSS/Atom/JSON): alternate-link titles and the channel description.
	// The description was English while every sibling field was Spanish; it now
	// tracks the render language like the rest. ---
	{"feed.rss_title", "%s RSS feed", "Canal RSS de %s"},
	{"feed.atom_title", "%s Atom feed", "Canal Atom de %s"},
	{"feed.json_title", "%s JSON feed", "Canal JSON de %s"},
	{"feed.channel_description", "The latest articles published on %s.", "Los últimos artículos publicados en %s."},

	// --- Inline body widgets (video embeds, tweet snapshots, related cards) baked
	// into article bodies at build time. %s = article title / alt. ---
	{"media.video_prefix", "Video: %s", "Vídeo: %s"},
	{"media.video_iframe_title", "Video", "Vídeo"},
	{"media.video_removed_note", "This video was removed from YouTube.", "Este video fue eliminado de YouTube."},
	{"media.view_original_link", "View original link", "Ver enlace original"},
	{"tweet.erased_note", "Post deleted on X. Censurado keeps this copy.", "Publicación eliminada en X. Censurado conserva esta copia."},
	{"tweet.view_on_x", "View on X", "Ver en X"},
	{"tweet.stat_replies", "replies", "respuestas"},
	{"tweet.stat_retweets", "retweets", "reposteos"},
	{"tweet.stat_likes", "likes", "me gusta"},
	{"tweet.stat_bookmarks", "bookmarks", "guardados"},
	{"tweet.stat_views", "views", "vistas"},
	{"related.card_kicker", "View related article", "Ver artículo relacionado"},
}

// defaultText is the compiled render fallback: key -> Spanish value, the shipped
// live strings. A key absent from the frontend_text table resolves here, so an
// unseeded database renders exactly today's bytes.
var defaultText = func() map[string]string {
	m := make(map[string]string, len(frontendSeed))
	for _, e := range frontendSeed {
		m[e.Key] = e.Es
	}
	return m
}()

// FrontendTextSeed returns the canonical catalog for the seedtext command: every
// key with its English base and Spanish translation.
func FrontendTextSeed() []TextSeedEntry {
	out := make([]TextSeedEntry, len(frontendSeed))
	copy(out, frontendSeed)
	return out
}
