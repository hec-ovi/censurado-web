package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Seam A freeze (the shared side): the Metadata key vocabulary. Article.Metadata is
// untyped JSON at the backend, but the publish layer (Layer 3) writes these keys and
// this generator (Layer 1) reads them, so the key set IS a contract even though
// nothing enforces it at the column. The authoritative list lives in the backend's
// contracts/CONTRACT.md; this guard, sitting with the consumer, fails if the
// generator stops referencing a frozen key (a silent drop of support). Adding a new
// key is forward-compatible and does not trip it; deliberately dropping one means
// updating CONTRACT.md and this list together.
var frozenMetadataVocab = []string{
	"subtitle", "description", "image", "image_alt", "alt",
	"author_name", "author_bio", "author_avatar", "avatar",
	"youtube", "youtube_id", "video", "keywords", "tweets", "media_checks",
	"gender", "beat", "profile_topics",
}

func TestContract_MetadataVocabularyReferenced(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var src strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src.Write(b)
	}
	source := src.String()
	for _, key := range frozenMetadataVocab {
		if !strings.Contains(source, `"`+key+`"`) {
			t.Errorf("the generator no longer references the frozen Metadata key %q; if that drop is intended, update contracts/CONTRACT.md and this list", key)
		}
	}
}
