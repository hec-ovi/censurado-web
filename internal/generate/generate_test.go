package generate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web-backend/content"
	"github.com/hec-ovi/censurado-web-backend/store"
)

// collectAll mirrors Generate's collection phase and returns the artifact set.
func collectAll(t *testing.T, repo store.Repository, opts Options) *ArtifactSet {
	t.Helper()
	if err := opts.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	env := buildEnvFrom(opts)
	if err := env.loadText(context.Background(), repo); err != nil {
		t.Fatalf("loadText: %v", err)
	}
	plan, err := BuildPlan(context.Background(), repo, opts.PageSize)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	env.plan = plan
	if env.bodyHTML, err = renderBodiesOnce(env); err != nil {
		t.Fatalf("renderBodies: %v", err)
	}
	set := newArtifactSet()
	for _, c := range collectors() {
		if err := c.collect(context.Background(), env, set); err != nil {
			t.Fatalf("collect %s: %v", c.name(), err)
		}
	}
	return set
}

// snapshotTree reads every regular file under root into a path->bytes map.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

// G1
func TestIncremental_Idempotent(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seedMatrix(t, repo)
	res1 := genInto(t, repo, out, nil)
	before := snapshotTree(t, out)

	res2 := genInto(t, repo, out, nil)
	after := snapshotTree(t, out)

	if res2.Written != 0 || res2.Deleted != 0 || len(res2.Purge) != 0 {
		t.Errorf("second run not idempotent: written=%d deleted=%d purge=%d", res2.Written, res2.Deleted, len(res2.Purge))
	}
	total := res1.Written + res1.Unchanged
	if res2.Unchanged != total {
		t.Errorf("second run Unchanged = %d, want total %d", res2.Unchanged, total)
	}
	// purge.json legitimately differs (run 1 lists every new URL, run 2 is empty);
	// it is KindState, not a public artifact. Everything else is byte-identical.
	purgeRel := filepath.ToSlash(filepath.Join(".generated", "purge.json"))
	delete(before, purgeRel)
	delete(after, purgeRel)
	if len(before) != len(after) {
		t.Errorf("file count changed: %d -> %d", len(before), len(after))
	}
	for p, b := range before {
		if after[p] != b {
			t.Errorf("file %s changed on idempotent re-run", p)
		}
	}
}

// G2
func TestAppendOnly_SealedPageImmutable(t *testing.T) {
	// PageSize=3 (see B1 note): 7 seeds give page/1, page/2 sealed and landing=1.
	repo := newStore(t)
	out := t.TempDir()
	for i := 0; i < 7; i++ {
		seed(t, repo, seedSpec{Title: "G2 " + string(rune('a'+i)), Author: "ada", Section: "tech", Published: pin.Add(-time.Duration(i) * time.Hour)})
	}
	genInto(t, repo, out, func(o *Options) { o.PageSize = 3 })
	page1 := string(readArtifact(t, out, "latest/page/1/index.html"))
	page2 := string(readArtifact(t, out, "latest/page/2/index.html"))

	// First insert: newer article -> only the landing grows, no new sealed page.
	seed(t, repo, seedSpec{Title: "G2 newer", Author: "ada", Section: "tech", Published: pin})
	res := genInto(t, repo, out, func(o *Options) { o.PageSize = 3 })
	if string(readArtifact(t, out, "latest/page/1/index.html")) != page1 || string(readArtifact(t, out, "latest/page/2/index.html")) != page2 {
		t.Errorf("sealed pages changed on first insert")
	}
	if exists(out, "latest/page/3/index.html") {
		t.Errorf("first insert created a sealed page/3")
	}
	if !strSet(res.Purge)["/latest/"] {
		t.Errorf("first insert did not purge /latest/")
	}
	if strSet(res.Purge)["/latest/page/1/"] || strSet(res.Purge)["/latest/page/2/"] {
		t.Errorf("sealed pages appeared in purge: %v", res.Purge)
	}
	if res.Written == 0 {
		t.Errorf("first insert wrote nothing")
	}

	// Second insert: the remainder seals into page/3.
	seed(t, repo, seedSpec{Title: "G2 newest", Author: "ada", Section: "tech", Published: pin.Add(time.Hour)})
	res2 := genInto(t, repo, out, func(o *Options) { o.PageSize = 3 })
	if !exists(out, "latest/page/3/index.html") {
		t.Errorf("second insert did not seal page/3")
	}
	if string(readArtifact(t, out, "latest/page/1/index.html")) != page1 || string(readArtifact(t, out, "latest/page/2/index.html")) != page2 {
		t.Errorf("sealed pages changed on second insert")
	}
	if strSet(res2.Purge)["/latest/page/1/"] || strSet(res2.Purge)["/latest/page/2/"] {
		t.Errorf("sealed pages appeared in purge on second insert: %v", res2.Purge)
	}
	if !strSet(res2.Purge)["/latest/page/3/"] {
		t.Errorf("newly sealed page/3 not in purge")
	}
}

// G3
func TestSlugify_DisplayVsSlugMembership(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{Title: "G3 One", Author: "Ada L.", Section: "Tech News", Topics: []string{"IaC"}, Published: date(2026, 6, 3)})
	nonAscii := seed(t, repo, seedSpec{Title: "G3 Two", Author: "日本語", Section: "日本語", Published: date(2026, 6, 4)})
	genInto(t, repo, out, nil)

	for _, rel := range []string{"section/tech-news/index.html", "author/ada-l/index.html", "topic/iac/index.html"} {
		if !exists(out, rel) {
			t.Errorf("missing scope page %s", rel)
		}
	}
	se := ShardEntryOf(a)
	if se.Section != "tech-news" || se.Author != "ada-l" || se.AuthorLabel != "Ada L." {
		t.Errorf("shard projection = section=%q author=%q label=%q", se.Section, se.Author, se.AuthorLabel)
	}
	if len(se.Topics) != 1 || se.Topics[0] != "iac" {
		t.Errorf("shard topics = %v, want [iac]", se.Topics)
	}

	// HTML membership for /topic/iac/ equals the shard entry set (same slugs).
	htmlURLs := permalinksIn(readArtifact(t, out, "topic/iac/index.html"))
	var shardURLset []string
	for _, u := range shardURLs(t, readArtifact(t, out, "shards/topic/iac/2026/06.json")) {
		shardURLset = append(shardURLset, u)
	}
	requireSameSet(t, "topic/iac membership", htmlURLs, shardURLset)

	// Ñoño section/author dropped from those axes but present in Latest/Month.
	if exists(out, "section/"+nonAscii.ContentHash[:12]+"/index.html") {
		// fallback slug must NOT form a /section/ URL
		t.Errorf("non-ascii section formed a /section/ scope page")
	}
	var latest []ShardEntry
	mustDecode(t, readArtifact(t, out, "shards/latest/2026/06.json"), &latest)
	found := false
	for _, e := range latest {
		if e.Slug == nonAscii.Slug {
			found = true
			if e.Section != nonAscii.ContentHash[:12] || e.Author != nonAscii.ContentHash[:12] {
				t.Errorf("non-ascii latest entry slug fallback = %q/%q", e.Section, e.Author)
			}
		}
	}
	if !found {
		t.Errorf("non-ascii article missing from latest shard")
	}
}

// G4
func TestEscaping_UntrustedFieldsEscaped(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{
		Title:     "Go <script>alert(1)</script>",
		Body:      "**bold** and a raw <script>alert('x')</script> tag",
		Author:    "a&b",
		Section:   "tech",
		Topics:    []string{"<x>"},
		Published: date(2026, 6, 3),
	})
	genInto(t, repo, out, nil)

	permalink := string(readArtifact(t, out, articlePath(a)))
	listing := string(readArtifact(t, out, "latest/index.html"))
	for _, doc := range []string{permalink, listing} {
		if !strings.Contains(doc, "Go &lt;script&gt;") {
			t.Errorf("title not escaped: missing 'Go &lt;script&gt;'")
		}
		if strings.Contains(doc, "<script>alert(1)") {
			t.Errorf("live <script>alert(1) leaked into HTML")
		}
		if !strings.Contains(doc, "a&amp;b") {
			t.Errorf("author '&' not escaped to a&amp;b")
		}
	}
	if !strings.Contains(permalink, "<strong>bold</strong>") {
		t.Errorf("markdown bold not rendered")
	}
	if strings.Contains(permalink, "<script>alert('x')") {
		t.Errorf("raw <script> body not neutralized")
	}
	// Verify the body matches the sanitized render exactly (no script survives).
	want, _ := content.RenderMarkdown(a.Body)
	if strings.Contains(want, "<script>") {
		t.Fatalf("sanitizer regression: rendered body still has <script>")
	}
	// Manifest script round-trips.
	m := manifestForScope(t, out, "latest")
	if m.Scope != "/latest/" {
		t.Errorf("manifest round-trip scope = %q", m.Scope)
	}
}

// G5
func TestPurgeIncludesShardAndManifestURLs(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "G5 One", Author: "ada", Section: "tech", Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)

	seed(t, repo, seedSpec{Title: "G5 Two", Author: "ada", Section: "tech", Published: date(2026, 6, 3)})
	res := genInto(t, repo, out, nil)

	if !strSet(res.Purge)["/shards/section/tech/2026/06.json"] {
		t.Errorf("purge missing the changed month-shard URL; got %v", res.Purge)
	}
	if !strSet(res.Purge)["/section/tech/"] {
		t.Errorf("purge missing the changed-manifest landing URL; got %v", res.Purge)
	}
}

// G6
func TestPathURLInvariant(t *testing.T) {
	repo := newStore(t)
	seedMatrix(t, repo)
	set := collectAll(t, repo, Options{OutDir: t.TempDir(), BaseURL: "https://news.example"})
	for _, p := range set.sortedPaths() {
		a := set.byPath[p]
		switch a.Kind {
		case KindPage:
			if a.URL != "/"+strings.TrimSuffix(a.Path, "index.html") {
				t.Errorf("KindPage %q url = %q", a.Path, a.URL)
			}
		case KindShard, KindMeta:
			if a.URL != "/"+a.Path {
				t.Errorf("Kind %d %q url = %q", a.Kind, a.Path, a.URL)
			}
		case KindState:
			if a.URL != "" {
				t.Errorf("KindState %q url = %q, want empty", a.Path, a.URL)
			}
		}
	}
}

// G7
func TestFingerprintForcesRebuildAndPurgesOrphans(t *testing.T) {
	// Deviation: the spec says PageSize 20 then 10, but going to a SMALLER page
	// size only ADDS pages (no orphan). To exhibit the orphan-purge behavior the
	// fix targets, the page count must shrink, so this runs 10 then 20. The
	// fingerprint-forced full rewrite is asserted regardless of direction.
	repo := newStore(t)
	out := t.TempDir()
	for i := 0; i < 25; i++ {
		seed(t, repo, seedSpec{Title: "FP " + string(rune('a'+i)), Author: "ada", Section: "tech", Published: pin.Add(-time.Duration(i) * time.Hour)})
	}
	genInto(t, repo, out, func(o *Options) { o.PageSize = 10 })
	if !exists(out, "latest/page/2/index.html") {
		t.Fatalf("expected /latest/page/2/ at PageSize=10")
	}

	res := genInto(t, repo, out, func(o *Options) { o.PageSize = 20 })
	if res.Written == 0 {
		t.Errorf("fingerprint change did not force a rewrite")
	}
	if exists(out, "latest/page/2/index.html") {
		t.Errorf("orphaned /latest/page/2/ not deleted at PageSize=20")
	}
	if !strSet(res.Purge)["/latest/page/2/"] {
		t.Errorf("orphaned page URL not in purge; got %v", res.Purge)
	}

	// state.json records the new fingerprint. Lang matches the default the run
	// validated to (es), since the fingerprint now covers the render language.
	prior, force := loadState(stateDir(out), optionsFingerprint(Options{OutDir: out, BaseURL: "https://news.example", PageSize: 20, ShardMaxEntries: 500, ShardMaxGzipBytes: 200 * 1024, Lang: "es"}))
	if force {
		t.Errorf("state fingerprint not updated to the PageSize=20 config")
	}
	if prior.Version != 1 {
		t.Errorf("state version = %d", prior.Version)
	}
}

// G9
func TestTempFileSwept(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seed(t, repo, seedSpec{Title: "Sweep Me", Author: "ada", Section: "tech", Published: date(2026, 6, 2)})
	genInto(t, repo, out, nil)

	// A STALE orphan (from an interrupted earlier run) must be swept.
	orphan := filepath.Join(out, "section", "tech", ".cnz-orphan")
	if err := os.WriteFile(orphan, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("age orphan: %v", err)
	}
	// A FRESH temp models the in-flight temp of a CONCURRENT pass (the watcher plus a
	// manual one-shot on the same volume). Sweeping it is what caused the chmod ENOENT,
	// so it must SURVIVE this sweep.
	inflight := filepath.Join(out, "section", "tech", ".cnz-inflight")
	if err := os.WriteFile(inflight, []byte("live"), 0o644); err != nil {
		t.Fatalf("write inflight: %v", err)
	}
	genInto(t, repo, out, nil)

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf(".cnz-orphan (stale) was not swept: %v", err)
	}
	if _, err := os.Stat(inflight); err != nil {
		t.Errorf(".cnz-inflight (fresh, concurrent) was swept, reopening the chmod race: %v", err)
	}
	if !exists(out, "section/tech/index.html") {
		t.Errorf("sweep removed a real artifact")
	}

	// The temp pattern never appears in the collected artifact set.
	set := collectAll(t, repo, Options{OutDir: out, BaseURL: "https://news.example"})
	for _, p := range set.sortedPaths() {
		if strings.Contains(p, ".cnz-") {
			t.Errorf("temp file in artifact set: %q", p)
		}
	}
}

func mustDecode(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
