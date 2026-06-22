package generate

import (
	"strings"
	"testing"
)

// D2: the CSS/JS assets are emitted at stable, non-hashed URLs, are non-empty,
// reproduce byte-for-byte across runs, and are referenced by every page.
func TestAssets_StableURLsAndReferences(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Asset One", Author: "ada", Section: "tech", Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)

	css := string(readArtifact(t, out, "assets/style.css"))
	js := string(readArtifact(t, out, "assets/app.js"))
	if css == "" || js == "" {
		t.Fatalf("empty asset: css=%d js=%d", len(css), len(js))
	}

	page := string(readArtifact(t, out, "latest/index.html"))
	if !strings.Contains(page, `<link rel="stylesheet" href="/assets/style.css">`) {
		t.Errorf("page missing stylesheet link")
	}
	if !strings.Contains(page, `<script type="module" src="/assets/app.js"></script>`) {
		t.Errorf("page missing module app.js script")
	}

	res := genInto(t, repo, out, nil)
	if string(readArtifact(t, out, "assets/style.css")) != css || string(readArtifact(t, out, "assets/app.js")) != js {
		t.Errorf("asset bytes changed across runs")
	}
	if len(res.Purge) != 0 || res.Written != 0 {
		t.Errorf("no-op re-run wrote/purged: written=%d purge=%v", res.Written, res.Purge)
	}
}
