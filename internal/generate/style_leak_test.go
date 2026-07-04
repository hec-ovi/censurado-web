package generate

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hec-ovi/censurado-web-backend/store"
)

// The author "style"/voice field is the private prompt the brain writes in an
// author's voice; the public site must never render it. store.Author.Style is a
// real column that sqlite.ListAuthors populates, so it physically enters
// overlayRegistry in memory during Generate. These guards prove it is dropped before
// disk. Bio and About are intentionally public (Bio is rendered; About is currently
// rendered nowhere) and are NOT treated as leaks.

// styleSentinel is lowercase-alnum so it survives HTML-escaping and Slugify
// unchanged: a leak via any emit path (raw, escaped, or slugified) still contains it.
const styleSentinel = "voicesentineldonotrender"

// bioPublic is a positive control: Bio is public and MUST render, so the byte walk
// cannot pass vacuously (e.g. if the author rendered nothing at all).
const bioPublic = "biopublicacontrol"

func TestStyleFieldNeverReachesPublicSite(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	// An article whose Author matches the registered handle, so the author flows
	// through Scopes(), gets an /author/<slug>/ page, and appears on /about/.
	seed(t, repo, seedSpec{
		Title:     "Nota de Lara",
		Author:    "lara",
		Section:   "politics",
		Topics:    []string{"elecciones"},
		Published: pin,
		Metadata:  map[string]any{"author_name": "Lara"},
	})
	// Register the author carrying the private Style sentinel plus real public fields
	// and the Metadata-driven overlay paths (gender, profile_topics), widening the
	// surface the walk must clear. About carries a distinct non-sentinel value so a
	// future wiring of About cannot be mistaken for a Style leak.
	upsertAuthor(t, repo, store.Author{
		Handle: "lara",
		Name:   "Lara",
		Bio:    bioPublic,
		About:  "descripcion publica larga que hoy no se renderiza",
		Avatar: "/media/lara.png",
		Style:  styleSentinel, // the field under test
		Metadata: map[string]any{
			"gender":         "femenino",
			"profile_topics": []string{"elecciones"},
		},
	})

	genInto(t, repo, out, nil)

	// Positive control: Bio (intentionally public) really rendered, so a leak would be
	// caught rather than the walk passing over an empty site.
	about := string(readArtifact(t, out, "about/index.html"))
	if !strings.Contains(about, bioPublic) {
		t.Fatalf("about page did not render the public Bio; the leak walk would be vacuous")
	}

	// Walk EVERY generated file (public artifacts + .generated state) and assert the
	// Style sentinel is absent. state.json stores sha256 hashes, not content, so it
	// cannot false-positive on the ASCII sentinel.
	needle := []byte(styleSentinel)
	err := filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(b, needle) {
			rel, _ := filepath.Rel(out, p)
			t.Errorf("STYLE LEAK: sentinel %q found in generated file %s", styleSentinel, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk out dir: %v", err)
	}
}

// TestAuthorViewStructsHaveNoStyleField catches a future edit that adds a Style-ish
// field to an author-facing view struct, before it is ever wired to a template. It
// fires at the struct definition, independent of whether a value happens to render.
func TestAuthorViewStructsHaveNoStyleField(t *testing.T) {
	for _, v := range []any{
		pageView{}, articleView{}, itemView{}, authorCardView{}, aboutView{},
	} {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			if strings.Contains(strings.ToLower(rt.Field(i).Name), "style") {
				t.Errorf("%s has a Style-ish field %q; the private voice prompt must never enter a view", rt.Name(), rt.Field(i).Name)
			}
		}
	}
}
