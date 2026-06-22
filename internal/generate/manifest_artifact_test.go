package generate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// inlineManifestBytes returns the raw JSON payload of a scope landing's inline
// #cnz-manifest script (the bytes between the wrapper tags).
func inlineManifestBytes(t *testing.T, out, scopePath string) []byte {
	t.Helper()
	b := readArtifact(t, out, scopePath+"/index.html")
	m := manifestRe.FindSubmatch(b)
	if m == nil {
		t.Fatalf("no inline manifest in %s landing", scopePath)
	}
	return m[1]
}

// D1: standalone per-scope manifest artifacts exist, validate against the schema,
// unmarshal into PageManifest, and are byte-identical to the inline landing payload.
func TestStandaloneManifest_BytesAndSchema(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "SM May", Author: "ada", Section: "smsec", Published: date(2026, 5, 4)})
	seed(t, repo, seedSpec{Title: "SM Jun A", Author: "ada", Section: "smsec", Published: date(2026, 6, 4)})
	seed(t, repo, seedSpec{Title: "SM Jun B", Author: "ada", Section: "smsec", Published: date(2026, 6, 6)})
	genInto(t, repo, out, nil)

	sch, err := jsonschema.NewCompiler().Compile("../../contracts/manifest.schema.json")
	if err != nil {
		t.Fatalf("compile manifest schema: %v", err)
	}

	for _, scopePath := range []string{"latest", "section/smsec"} {
		rel := "manifest/" + scopePath + "/index.json"
		if !exists(out, rel) {
			t.Fatalf("missing standalone manifest %s", rel)
		}
		b := readArtifact(t, out, rel)
		validateJSON(t, sch, b, rel)

		var pm PageManifest
		if err := json.Unmarshal(b, &pm); err != nil {
			t.Fatalf("decode %s: %v", rel, err)
		}

		// Byte-identical to the inline #cnz-manifest payload on the landing.
		if inline := inlineManifestBytes(t, out, scopePath); string(b) != string(inline) {
			t.Errorf("%s bytes differ from inline payload:\n file=%s\n inline=%s", rel, b, inline)
		}
		if pm.Scope != manifestForScope(t, out, scopePath).Scope {
			t.Errorf("%s scope mismatch with inline manifest", rel)
		}
	}
}

// D1: <link rel=manifest> with the correct scope href is present on BOTH a landing
// and a sealed page, and a no-op re-run leaves the manifests Unchanged (empty Purge).
func TestStandaloneManifest_LinkAndIdempotency(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	for i := 0; i < 7; i++ {
		seed(t, repo, seedSpec{Title: "LM " + string(rune('a'+i)), Author: "ada", Section: "tech", Published: pin.Add(-time.Duration(i) * time.Hour)})
	}
	genInto(t, repo, out, func(o *Options) { o.PageSize = 3 })

	latestHref := `<link rel="manifest" href="/manifest/latest/index.json">`
	landing := string(readArtifact(t, out, "latest/index.html"))
	sealed := string(readArtifact(t, out, "latest/page/1/index.html"))
	if !strings.Contains(landing, latestHref) {
		t.Errorf("landing missing manifest link %q", latestHref)
	}
	if !strings.Contains(sealed, latestHref) {
		t.Errorf("sealed page missing manifest link %q", latestHref)
	}
	secSealed := string(readArtifact(t, out, "section/tech/page/1/index.html"))
	if !strings.Contains(secSealed, `<link rel="manifest" href="/manifest/section/tech/index.json">`) {
		t.Errorf("section sealed page missing its own scope manifest link")
	}

	res := genInto(t, repo, out, func(o *Options) { o.PageSize = 3 })
	if res.Written != 0 || len(res.Purge) != 0 {
		t.Errorf("manifest re-run not idempotent: written=%d purge=%v", res.Written, res.Purge)
	}
}
