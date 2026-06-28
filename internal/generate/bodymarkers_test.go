package generate

import (
	"strings"
	"testing"
)

// Inline body markers: {{video:...}} renders a responsive embed (Feature 3) and
// {{relacionado:slug}} renders the "Ver artículo relacionado" card (Feature 4).
// Both are expanded by the generator after sanitization, so the literal marker
// never survives into the page.

func TestBodyMarker_VideoYouTube(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	for i, body := range []string{
		"Intro.\n\n{{video:dQw4w9WgXcQ}}\n\nOutro.",
		"Intro.\n\n{{video:https://www.youtube.com/watch?v=dQw4w9WgXcQ}}\n\nOutro.",
		"Intro.\n\n{{video: https://youtu.be/dQw4w9WgXcQ }}\n\nOutro.",
	} {
		// Distinct titles -> distinct slugs (the store enforces unique slugs).
		a := seed(t, repo, seedSpec{Title: "Vídeo demo " + string(rune('A'+i)), Author: "vector-omni", Section: "tech", Body: body, Published: date(2026, 6, 10)})
		genInto(t, repo, out, nil)
		page := string(readArtifact(t, out, articlePath(a)))
		if strings.Contains(page, "{{video:") {
			t.Errorf("literal video marker survived for body %q", body)
		}
		if !strings.Contains(page, `class="body-embed"`) {
			t.Errorf("video embed not rendered for body %q", body)
		}
		if !strings.Contains(page, "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ") {
			t.Errorf("youtube-nocookie embed URL missing for body %q\n%s", body, page)
		}
		// The surrounding prose is preserved.
		if !strings.Contains(page, "Intro.") || !strings.Contains(page, "Outro.") {
			t.Errorf("surrounding prose lost for body %q", body)
		}
	}
}

func TestBodyMarker_RelatedCard(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// An older target with a subtitle + image, then a newer article that links it.
	target := seed(t, repo, seedSpec{
		Title: "El informe que nadie leyó", Author: "lara-arianna", Section: "politics",
		Published: date(2026, 6, 1),
		Metadata: map[string]any{
			"subtitle": "Una bajada larga con el dato que importa, y despues bastante texto adicional puesto a proposito para superar con holgura el limite de caracteres y forzar el recorte por palabra en la tarjeta, hasta esta marca final del subtitulo.",
			"image":    "/media/" + strings.Repeat("a", 64) + ".png",
		},
	})
	// A DIFFERENT author + section so the target appears ONLY in the inline card,
	// not in the page's bottom "Relacionados" / "Más de este autor" blocks (which
	// render the full untrimmed subtitle and would otherwise mask the trim check).
	newer := seed(t, repo, seedSpec{
		Title: "La secuela", Author: "vector-omni", Section: "tech",
		Body:      "Antes de seguir.\n\n{{relacionado:" + target.Slug + "}}\n\nDespués del recuadro.",
		Published: date(2026, 6, 2),
	})
	genInto(t, repo, out, nil)
	page := string(readArtifact(t, out, articlePath(newer)))

	if strings.Contains(page, "{{relacionado:") {
		t.Fatalf("literal related marker survived:\n%s", page)
	}
	for _, want := range []string{
		`class="related-card"`,
		`<span class="related-card-kicker">Ver artículo relacionado</span>`,
		`href="` + articleURL(target) + `"`,
		`El informe que nadie leyó`,
		`class="related-card-thumb"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("related card missing %q\n%s", want, page)
		}
	}
	// The subtitle is trimmed (the long dek is cut with an ellipsis, never shown whole).
	if strings.Contains(page, "marca final del subtitulo") {
		t.Errorf("related card subtitle was not trimmed")
	}
	if !strings.Contains(page, "…") {
		t.Errorf("trimmed subtitle should end with an ellipsis")
	}
}

func TestBodyMarker_RelatedUnknownAndForwardDropped(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// A marker to a non-existent slug is dropped (no card, no literal).
	a := seed(t, repo, seedSpec{
		Title: "Solitaria", Author: "ada", Section: "tech",
		Body:      "Texto.\n\n{{relacionado:no-existe}}\n\nMás texto.",
		Published: date(2026, 6, 5),
	})
	// A forward reference (to a NEWER article) is also dropped, keeping bodies
	// deterministic. Seed the older one referencing a slug that only a newer article
	// will own.
	older := seed(t, repo, seedSpec{
		Title: "La primera", Author: "ada", Section: "tech",
		Body:      "Cuerpo.\n\n{{relacionado:la-segunda}}\n\nFin.",
		Published: date(2026, 6, 6),
	})
	seed(t, repo, seedSpec{Title: "La segunda", Author: "ada", Section: "tech", Published: date(2026, 6, 7)})
	genInto(t, repo, out, nil)

	pageA := string(readArtifact(t, out, articlePath(a)))
	if strings.Contains(pageA, "{{relacionado:") || strings.Contains(pageA, `class="related-card"`) {
		t.Errorf("unknown-slug marker should drop to nothing:\n%s", pageA)
	}
	pageOlder := string(readArtifact(t, out, articlePath(older)))
	if strings.Contains(pageOlder, "{{relacionado:") || strings.Contains(pageOlder, `class="related-card"`) {
		t.Errorf("forward-reference marker should drop to nothing:\n%s", pageOlder)
	}
}

// A permalink carrying a related card stays byte-stable when a newer article is
// published (the card payload is the immutable target, never the live tail).
func TestBodyMarker_RelatedByteStable(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	target := seed(t, repo, seedSpec{Title: "Base", Author: "ada", Section: "tech", Published: date(2026, 6, 1)})
	ref := seed(t, repo, seedSpec{
		Title: "Referente", Author: "ada", Section: "tech",
		Body:      "Hola.\n\n{{relacionado:" + target.Slug + "}}\n\nChau.",
		Published: date(2026, 6, 2),
	})
	genInto(t, repo, out, nil)
	before := string(readArtifact(t, out, articlePath(ref)))
	if !strings.Contains(before, `class="related-card"`) {
		t.Fatalf("expected a related card on first render")
	}
	seed(t, repo, seedSpec{Title: "Posterior", Author: "ada", Section: "tech", Published: date(2026, 6, 3)})
	genInto(t, repo, out, nil)
	if string(readArtifact(t, out, articlePath(ref))) != before {
		t.Errorf("permalink with a related card changed bytes after a newer publish")
	}
}
