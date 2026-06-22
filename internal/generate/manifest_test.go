package generate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// M1
func TestBuildManifest_Fields(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// June: 3 articles (capE=2 -> 2 parts). May: 1 article (1 part).
	seed(t, repo, seedSpec{Title: "M1 May", Author: "ada", Section: "m1sec", Published: date(2026, 5, 2)})
	seed(t, repo, seedSpec{Title: "M1 June A", Author: "ada", Section: "m1sec", Published: date(2026, 6, 2)})
	seed(t, repo, seedSpec{Title: "M1 June B", Author: "ada", Section: "m1sec", Published: date(2026, 6, 3)})
	seed(t, repo, seedSpec{Title: "M1 June C", Author: "ada", Section: "m1sec", Published: date(2026, 6, 4)})
	genInto(t, repo, out, func(o *Options) { o.ShardMaxEntries = 2 })

	m := manifestForScope(t, out, "section/m1sec")
	if m.Scope != "/section/m1sec/" {
		t.Errorf("scope = %q", m.Scope)
	}
	if len(m.Axis) != 1 || m.Axis["section"] != "m1sec" {
		t.Errorf("axis = %v, want {section:m1sec}", m.Axis)
	}
	if len(m.Shards) != 2 {
		t.Fatalf("shards = %d, want 2", len(m.Shards))
	}
	if m.Shards[0].Month != "2026-06" || m.Shards[1].Month != "2026-05" {
		t.Errorf("months not newest-first: %q then %q", m.Shards[0].Month, m.Shards[1].Month)
	}
	if m.Shards[0].Count != 3 || m.Shards[1].Count != 1 {
		t.Errorf("counts = %d,%d, want 3,1", m.Shards[0].Count, m.Shards[1].Count)
	}
	// June: >1 part -> parts newest-part-first, includes the part-1 URL.
	if len(m.Shards[0].Parts) != 2 {
		t.Fatalf("June parts = %v, want 2", m.Shards[0].Parts)
	}
	if m.Shards[0].Parts[0] != "/shards/section/m1sec/2026/06.2.json" || m.Shards[0].Parts[1] != "/shards/section/m1sec/2026/06.json" {
		t.Errorf("June parts order = %v", m.Shards[0].Parts)
	}
	if m.Shards[0].URL != "/shards/section/m1sec/2026/06.json" {
		t.Errorf("June url = %q, want part-1 url", m.Shards[0].URL)
	}
	// May: single part -> no Parts list.
	if m.Shards[1].Parts != nil {
		t.Errorf("May parts = %v, want nil for single part", m.Shards[1].Parts)
	}
	if m.Total != 4 {
		t.Errorf("total = %d, want 4", m.Total)
	}
	if m.Cap.Entries != 2 || m.Cap.Bytes != 200*1024 {
		t.Errorf("cap = %+v", m.Cap)
	}

	// Latest axis is {} and default caps are (500, 200KB).
	lm := BuildManifest(Scope{}, 500, 200*1024,
		[]ym{{2026, 6}},
		map[string][]ShardPart{"2026-06": {{Path: "shards/latest/2026/06.json", Part: 1, Count: 1}}}, 1)
	if lm.Scope != "/latest/" || len(lm.Axis) != 0 {
		t.Errorf("latest manifest scope/axis = %q/%v", lm.Scope, lm.Axis)
	}
	if lm.Cap.Entries != 500 || lm.Cap.Bytes != 200*1024 {
		t.Errorf("default cap = %+v, want {500, 204800}", lm.Cap)
	}
}

// M2
func TestManifest_MatchesSchema(t *testing.T) {
	sch, err := jsonschema.NewCompiler().Compile("../../contracts/manifest.schema.json")
	if err != nil {
		t.Fatalf("compile manifest schema: %v", err)
	}
	m := BuildManifest(
		Scope{Section: Facet{Kind: AxisSection, Value: "tech"}, Sub: Facet{Kind: AxisTopic, Value: "ai"}},
		500, 200*1024,
		[]ym{{2026, 6}},
		map[string][]ShardPart{"2026-06": {
			{Path: "shards/section/tech/topic/ai/2026/06.json", Part: 1, Count: 2},
			{Path: "shards/section/tech/topic/ai/2026/06.2.json", Part: 2, Count: 1},
		}}, 3)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	validateJSON(t, sch, b, "manifest")

	// Extra top-level field FAILS additionalProperties.
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	raw["extra"] = "leak"
	extra, _ := json.Marshal(raw)
	if err := sch.Validate(mustUnmarshalAny(t, extra)); err == nil {
		t.Errorf("schema accepted a manifest with an extra top-level field")
	}

	// axis with an unknown key FAILS.
	delete(raw, "extra")
	raw["axis"] = map[string]any{"section": "tech", "bogus": "x"}
	badAxis, _ := json.Marshal(raw)
	if err := sch.Validate(mustUnmarshalAny(t, badAxis)); err == nil {
		t.Errorf("schema accepted an axis with an unknown key")
	}
}

// M3
func TestManifestScript_Embed(t *testing.T) {
	m := PageManifest{
		Scope:  "/section/x/</script><tag>&/",
		Axis:   map[string]string{},
		Shards: []ShardRef{},
		Total:  0,
		Cap:    ManifestCaps{Entries: 500, Bytes: 200 * 1024},
	}
	html, err := ManifestScript(m)
	if err != nil {
		t.Fatalf("ManifestScript: %v", err)
	}
	s := string(html)
	if !strings.HasPrefix(s, `<script type="application/json" id="cnz-manifest">`) || !strings.HasSuffix(s, "</script>") {
		t.Errorf("wrapper mismatch: %q", s)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(s, `<script type="application/json" id="cnz-manifest">`), "</script>")
	// No raw breakout: <, >, & never appear literally in the JSON payload.
	for _, raw := range []string{"</script>", "<tag>", "<", ">", "&/"} {
		if strings.Contains(inner, raw) {
			t.Errorf("unescaped %q in payload: %q", raw, inner)
		}
	}
	// They appear as the \uXXXX escapes json.Marshal emits with EscapeHTML on.
	if !strings.Contains(inner, "\\u003c") || !strings.Contains(inner, "\\u003e") || !strings.Contains(inner, "\\u0026") {
		t.Errorf("expected \\u003c \\u003e \\u0026 escapes in payload: %q", inner)
	}
	// Inner text round-trips back to the manifest.
	var back PageManifest
	if err := json.Unmarshal([]byte(inner), &back); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if back.Scope != m.Scope {
		t.Errorf("round-trip scope = %q, want %q", back.Scope, m.Scope)
	}
}

// M4
func TestManifest_NoDanglingShardLink(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// section tech x author ada across May and June.
	seed(t, repo, seedSpec{Title: "M4 May", Author: "ada", Section: "tech", Published: date(2026, 5, 5)})
	seed(t, repo, seedSpec{Title: "M4 June", Author: "ada", Section: "tech", Published: date(2026, 6, 6)})
	genInto(t, repo, out, nil)

	m := manifestForScope(t, out, "section/tech/author/ada")
	if len(m.Shards) != 2 {
		t.Fatalf("shards = %d, want 2 months", len(m.Shards))
	}
	check := func(url string) {
		if !strings.HasPrefix(url, "/") {
			t.Errorf("url not root-relative: %q", url)
			return
		}
		if !exists(out, strings.TrimPrefix(url, "/")) {
			t.Errorf("manifest references missing file: %q", url)
		}
	}
	for _, ref := range m.Shards {
		check(ref.URL)
		for _, p := range ref.Parts {
			check(p)
		}
	}
}
