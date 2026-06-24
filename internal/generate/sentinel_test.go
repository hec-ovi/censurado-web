package generate

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func decodeSentinel(t *testing.T, b []byte) versionSentinel {
	t.Helper()
	var s versionSentinel
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("decode version.json: %v (%s)", err, b)
	}
	return s
}

// The sentinel is byte-stable over an unchanged store (so the edge can answer 304)
// and changes the moment a new top story lands, at which point it appears in
// purge.json while the unchanged immutable permalinks do not.
func TestVersionSentinel_StableThenChangesOnPublish(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a1 := seed(t, repo, seedSpec{Title: "First story", Author: "ada", Section: "tech", Published: pin.Add(-2 * time.Hour)})
	a2 := seed(t, repo, seedSpec{Title: "Second story", Author: "bo", Section: "world", Published: pin.Add(-1 * time.Hour)})

	genInto(t, repo, out, nil)
	v1 := readArtifact(t, out, "latest/version.json")
	s1 := decodeSentinel(t, v1)
	if s1.V == "" {
		t.Error("v fingerprint is empty")
	}
	if len(s1.LatestIDs) != 2 || s1.LatestIDs[0] != a2.Slug || s1.LatestIDs[1] != a1.Slug {
		t.Errorf("latest_ids = %v, want [%s %s] newest-first", s1.LatestIDs, a2.Slug, a1.Slug)
	}

	// Regenerate over the unchanged store: the sentinel is byte-identical (304-able).
	res := genInto(t, repo, out, nil)
	if len(res.Purge) != 0 {
		t.Errorf("unchanged regenerate purged %v, want nothing", res.Purge)
	}
	if v1b := readArtifact(t, out, "latest/version.json"); !bytes.Equal(v1, v1b) {
		t.Errorf("version.json changed on an idempotent regenerate:\n was %s\n now %s", v1, v1b)
	}

	// Publish a new newest article: the sentinel changes and is purged; the older
	// content-addressed permalinks did not change and are not purged.
	a3 := seed(t, repo, seedSpec{Title: "Breaking news now", Author: "cy", Section: "tech", Published: pin})
	res2 := genInto(t, repo, out, nil)

	v2 := readArtifact(t, out, "latest/version.json")
	s2 := decodeSentinel(t, v2)
	if s2.V == s1.V {
		t.Error("v fingerprint did not change after a new article landed")
	}
	if s2.LatestIDs[0] != a3.Slug {
		t.Errorf("newest latest_id = %q, want %q", s2.LatestIDs[0], a3.Slug)
	}

	purge := strSet(res2.Purge)
	if !purge["/latest/version.json"] {
		t.Errorf("/latest/version.json not in purge set: %v", res2.Purge)
	}
	if !purge[articleURL(a3)] {
		t.Errorf("new article permalink %s not purged", articleURL(a3))
	}
	if purge[articleURL(a1)] {
		t.Errorf("unchanged immutable permalink %s should not be purged", articleURL(a1))
	}
}

// The emitted sentinel validates against its frozen contract.
func TestVersionSentinel_ValidatesAgainstSchema(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Schema check", Author: "ada", Section: "tech"})
	genInto(t, repo, out, nil)

	sch, err := jsonschema.NewCompiler().Compile("../../contracts/version.schema.json")
	if err != nil {
		t.Fatalf("compile version schema: %v", err)
	}
	validateJSON(t, sch, readArtifact(t, out, "latest/version.json"), "latest/version.json")
}

// An empty store still emits a stable, schema-valid sentinel (no articles).
func TestVersionSentinel_EmptyStore(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	genInto(t, repo, out, nil)

	b := readArtifact(t, out, "latest/version.json")
	s := decodeSentinel(t, b)
	if s.V == "" {
		t.Error("empty-store sentinel has no fingerprint")
	}
	if len(s.LatestIDs) != 0 {
		t.Errorf("latest_ids = %v, want empty for an empty store", s.LatestIDs)
	}
	sch, err := jsonschema.NewCompiler().Compile("../../contracts/version.schema.json")
	if err != nil {
		t.Fatalf("compile version schema: %v", err)
	}
	validateJSON(t, sch, b, "latest/version.json (empty store)")
}
