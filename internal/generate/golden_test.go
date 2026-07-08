package generate

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// --- (a) ---------------------------------------------------------------------

func TestGolden_AddArticleChangesExactURLs(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	// Closed-month fixture: tech / ada with topics iac, go in 2026-05.
	seed(t, repo, seedSpec{Title: "May One", Author: "ada", Section: "tech", Topics: []string{"iac", "go"}, Published: date(2026, 5, 10)})
	seed(t, repo, seedSpec{Title: "May Two", Author: "ada", Section: "tech", Topics: []string{"iac"}, Published: date(2026, 5, 12)})
	genInto(t, repo, out, nil)

	// Insert a now-dated (June) article in tech / ada / iac.
	n := seed(t, repo, seedSpec{Title: "June Update", Author: "ada", Section: "tech", Topics: []string{"iac"}, Published: date(2026, 6, 15)})
	res := genInto(t, repo, out, nil)

	listings := []string{
		"/latest/", "/section/tech/", "/author/ada/", "/topic/iac/", "/2026/06/",
		"/section/tech/author/ada/", "/section/tech/topic/iac/", "/section/tech/2026/06/",
	}
	shards := []string{
		"/shards/latest/2026/06.json", "/shards/section/tech/2026/06.json",
		"/shards/author/ada/2026/06.json", "/shards/topic/iac/2026/06.json",
		"/shards/2026/06/2026/06.json", "/shards/section/tech/author/ada/2026/06.json",
		"/shards/section/tech/topic/iac/2026/06.json", "/shards/section/tech/2026/06/2026/06.json",
	}
	// Each affected scope's standalone manifest gains the new June month (or is
	// newly created), so it is purged alongside the listing it backs. The /topic/go/
	// and /2026/05/ scopes are untouched by the June insert, so their manifests stay.
	manifests := []string{
		"/manifest/latest/index.json", "/manifest/section/tech/index.json",
		"/manifest/author/ada/index.json", "/manifest/topic/iac/index.json",
		"/manifest/2026/06/index.json", "/manifest/section/tech/author/ada/index.json",
		"/manifest/section/tech/topic/iac/index.json", "/manifest/section/tech/2026/06/index.json",
	}
	// The listings sitemap gains the two new June scope URLs (and refreshed
	// lastmods); a brand-new per-month article sitemap appears for 2026-06; the
	// sitemap index gains that child. The 2026-05 article sitemap is byte-stable.
	// The version sentinel changes because the new article is the newest, so it is
	// purged too; its fingerprint over the latest window moved.
	meta := []string{
		"/sitemap.xml", "/sitemaps/listings.xml", "/sitemaps/articles-2026-06.xml",
		"/feed.xml", "/atom.xml", "/feed.json", "/latest/version.json",
	}
	want := append(append(append([]string{}, listings...), shards...), manifests...)
	want = append(want, meta...)
	want = append(want, articleURL(n))

	requireSameSet(t, "purge", res.Purge, want)

	for _, u := range res.Purge {
		if matched, _ := regexp.MatchString(`/page/\d+/`, u); matched {
			t.Errorf("purge contains a sealed-page URL: %q", u)
		}
		if matched, _ := regexp.MatchString(`/2026/05\.json$`, u); matched {
			t.Errorf("purge contains a closed-month (2026-05) shard URL: %q", u)
		}
	}
}

// --- (b) ---------------------------------------------------------------------

func TestGolden_ShardCaps(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	for i := 0; i < 7; i++ {
		seed(t, repo, seedSpec{
			Title:     "Caps " + string(rune('A'+i)),
			Author:    "ada",
			Section:   "caps",
			Published: date(2026, 6, 1).Add(time.Duration(i) * time.Hour),
		})
	}
	genInto(t, repo, out, func(o *Options) { o.ShardMaxEntries = 3; o.ShardMaxGzipBytes = 4096 })

	parts := []string{
		"shards/section/caps/2026/06.json",
		"shards/section/caps/2026/06.2.json",
		"shards/section/caps/2026/06.3.json",
	}
	counts := []int{3, 3, 1}
	for i, p := range parts {
		b := readArtifact(t, out, p)
		var entries []ShardEntry
		if err := json.Unmarshal(b, &entries); err != nil {
			t.Fatalf("decode %s: %v", p, err)
		}
		if len(entries) != counts[i] {
			t.Errorf("%s: %d entries, want %d", p, len(entries), counts[i])
		}
		if len(entries) > 3 {
			t.Errorf("%s: over entry cap 3", p)
		}
		if gz := gzipLen(b); gz > 4096 {
			t.Errorf("%s: gzipLen %d > cap 4096", p, gz)
		}
	}
	if exists(out, "shards/section/caps/2026/06.4.json") {
		t.Errorf("unexpected 4th part emitted")
	}

	m := manifestForScope(t, out, "section/caps")
	if len(m.Shards) != 1 {
		t.Fatalf("manifest shards = %d, want 1", len(m.Shards))
	}
	ref := m.Shards[0]
	if ref.Count != 7 {
		t.Errorf("shard count = %d, want 7", ref.Count)
	}
	wantParts := []string{
		"/shards/section/caps/2026/06.3.json",
		"/shards/section/caps/2026/06.2.json",
		"/shards/section/caps/2026/06.json",
	}
	if len(ref.Parts) != 3 {
		t.Fatalf("parts = %v, want 3 newest-first", ref.Parts)
	}
	for i, p := range wantParts {
		if ref.Parts[i] != p {
			t.Errorf("parts[%d] = %q, want %q", i, ref.Parts[i], p)
		}
	}
}

// --- (c) ---------------------------------------------------------------------

var frozenShardKeys = []string{
	"slug", "url", "title", "subtitle", "description", "image", "video", "author", "author_label", "avatar", "section", "section_label", "topics", "published_at", "ts", "cts", "ord", "role",
}

func TestGolden_ShardFieldSetMatchesSchema(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	a := seed(t, repo, seedSpec{Title: "Schema One", Author: "Ada L.", Section: "Tech News", Topics: []string{"IaC", "Go"}, Published: date(2026, 6, 3)})
	// Fully non-ASCII section AND author: both slugify to "" -> ContentHash[:12].
	nonAscii := seed(t, repo, seedSpec{Title: "Schema Two", Author: "日本語", Section: "日本語", Topics: []string{"bio"}, Published: date(2026, 6, 4)})
	genInto(t, repo, out, nil)

	schema := jsonschema.NewCompiler()
	sch, err := schema.Compile("../../contracts/shard.schema.json")
	if err != nil {
		t.Fatalf("compile shard schema: %v", err)
	}

	// Every emitted shard file validates and uses exactly the 18 frozen keys.
	for _, p := range shardFiles(t, out) {
		b := readArtifact(t, out, p)
		validateJSON(t, sch, b, p)
		var rawEntries []map[string]json.RawMessage
		if err := json.Unmarshal(b, &rawEntries); err != nil {
			t.Fatalf("decode %s: %v", p, err)
		}
		for _, e := range rawEntries {
			assertKeySet(t, p, e, frozenShardKeys)
		}
	}

	// Projection field checks for the ASCII article.
	se := ShardEntryOf(a)
	if se.Author != "ada-l" || se.Section != "tech-news" {
		t.Errorf("slug-form author/section = %q/%q, want ada-l/tech-news", se.Author, se.Section)
	}
	if se.AuthorLabel != "Ada L." {
		t.Errorf("author_label = %q, want %q", se.AuthorLabel, "Ada L.")
	}
	// The store returns topics ordered by the stored value, so assert as a set.
	requireSameSet(t, "shard topics", se.Topics, []string{"iac", "go"})
	if se.PublishedAt != a.PublishedAt.UTC().Truncate(time.Second).Format(time.RFC3339) {
		t.Errorf("published_at = %q", se.PublishedAt)
	}
	if se.TS != a.PublishedAt.UTC().Truncate(time.Second).Unix() {
		t.Errorf("ts = %d, want %d", se.TS, a.PublishedAt.Unix())
	}
	if se.CreatedTS != a.CreatedAt.UTC().Truncate(time.Second).Unix() {
		t.Errorf("cts = %d, want %d", se.CreatedTS, a.CreatedAt.UTC().Truncate(time.Second).Unix())
	}

	// Non-ASCII fallback: section/author become ContentHash[:12], still valid.
	ne := ShardEntryOf(nonAscii)
	if ne.Section != nonAscii.ContentHash[:12] || ne.Author != nonAscii.ContentHash[:12] {
		t.Errorf("non-ascii fallback = %q/%q, want %q", ne.Section, ne.Author, nonAscii.ContentHash[:12])
	}
	validJSON, _ := marshalShard([]ShardEntry{ne})
	validateJSON(t, sch, validJSON, "non-ascii single entry")

	// Extra field FAILS additionalProperties.
	extra := withExtraKey(t, se, "body", "leak")
	if err := sch.Validate(mustUnmarshalAny(t, extra)); err == nil {
		t.Errorf("schema accepted an entry with an extra 'body' field")
	}
	// Missing required field FAILS.
	missing := withoutKey(t, se, "ts")
	if err := sch.Validate(mustUnmarshalAny(t, missing)); err == nil {
		t.Errorf("schema accepted an entry missing required 'ts'")
	}
}

// --- (d) ---------------------------------------------------------------------

func TestGolden_BoundednessFormula(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	seedMatrix(t, repo)
	res := genInto(t, repo, out, nil)

	// 1 + S(2) + A(3) + T(4) + M(2) + tech(2+2+2) + science(3+2+2) = 25.
	if res.ScopeCount != 25 {
		t.Errorf("ScopeCount = %d, want 25", res.ScopeCount)
	}
	// All scopes hold <= PageSize, so each contributes exactly one landing page.
	if res.TierAURLs != 25 {
		t.Errorf("TierAURLs = %d, want 25", res.TierAURLs)
	}

	plan, err := BuildPlan(context.Background(), repo, 20)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var gotURLs []string
	for _, pg := range plan.Pages {
		gotURLs = append(gotURLs, pg.Scope.PageURL(pg.Number))
	}
	want := []string{
		"/latest/",
		"/section/tech/", "/section/science/",
		"/author/ada/", "/author/lin/", "/author/bob/",
		"/topic/ai/", "/topic/go/", "/topic/bio/", "/topic/space/",
		"/2026/05/", "/2026/06/",
		"/section/tech/author/ada/", "/section/tech/author/lin/",
		"/section/tech/topic/ai/", "/section/tech/topic/go/",
		"/section/tech/2026/05/", "/section/tech/2026/06/",
		"/section/science/author/ada/", "/section/science/author/bob/", "/section/science/author/lin/",
		"/section/science/topic/bio/", "/section/science/topic/space/",
		"/section/science/2026/05/", "/section/science/2026/06/",
	}
	requireSameSet(t, "page-1 listing URLs", gotURLs, want)

	// A never-co-occurring (section, topic) pair produces no URL.
	if exists(out, "section/tech/topic/bio/index.html") {
		t.Errorf("emitted /section/tech/topic/bio/ for a non-co-occurring pair")
	}
	// No bare month page for an empty month.
	if exists(out, "2026/04/index.html") {
		t.Errorf("emitted a bare /2026/04/ for an empty month")
	}
}

// --- (e) ---------------------------------------------------------------------

func TestGolden_ClientOrderParity(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	// Single scope-month with 5 distinct times plus 1 tie (same time, new id).
	tie := date(2026, 6, 10)
	times := []time.Time{
		date(2026, 6, 2), date(2026, 6, 5), tie, tie, date(2026, 6, 15), date(2026, 6, 20),
	}
	for i, ts := range times {
		seed(t, repo, seedSpec{Title: "Parity " + string(rune('A'+i)), Author: "ada", Section: "parity", Published: ts})
	}
	genInto(t, repo, out, nil)

	htmlOrder := permalinksIn(readArtifact(t, out, "section/parity/index.html"))
	shardOrder := shardURLs(t, readArtifact(t, out, "shards/section/parity/2026/06.json"))
	if len(htmlOrder) != 6 || len(shardOrder) != 6 {
		t.Fatalf("orders: html=%d shard=%d, want 6 each", len(htmlOrder), len(shardOrder))
	}
	for i := range htmlOrder {
		if htmlOrder[i] != shardOrder[i] {
			t.Errorf("order mismatch at %d: html=%q shard=%q", i, htmlOrder[i], shardOrder[i])
		}
	}
}

func TestGolden_ClientOrderParity_MultiMonth(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()

	// Pages paginate by publication date (Created is scrambled to prove insert order is
	// ignored). PageSize=4: the landing holds the newest 2 (both June) and page/1 the
	// older 4 (one June, three May), so the reading order landing -> page/1 is globally
	// newest-first and matches the per-month shards.
	specs := []struct {
		pub time.Time
	}{
		{date(2026, 6, 20)}, {date(2026, 6, 10)}, {date(2026, 6, 1)},
		{date(2026, 5, 25)}, {date(2026, 5, 15)}, {date(2026, 5, 5)},
	}
	monthOf := map[string]int{}
	for i, s := range specs {
		a := seed(t, repo, seedSpec{
			Title:     "Straddle " + string(rune('A'+i)),
			Author:    "ada",
			Section:   "straddle",
			Published: s.pub,
			Created:   pin.Add(time.Duration(i+1) * time.Minute),
		})
		monthOf[articleURL(a)] = int(a.PublishedAt.Month())
	}
	genInto(t, repo, out, func(o *Options) { o.PageSize = 4 })

	// Reading order is newest-first: the landing (newest published) then the older page.
	var stream []string
	stream = append(stream, permalinksIn(readArtifact(t, out, "section/straddle/index.html"))...)
	stream = append(stream, permalinksIn(readArtifact(t, out, "section/straddle/page/1/index.html"))...)

	for _, mm := range []struct {
		file  string
		month int
	}{{"shards/section/straddle/2026/06.json", 6}, {"shards/section/straddle/2026/05.json", 5}} {
		shardOrder := shardURLs(t, readArtifact(t, out, mm.file))
		var filtered []string
		for _, u := range stream {
			if monthOf[u] == mm.month {
				filtered = append(filtered, u)
			}
		}
		if len(shardOrder) != len(filtered) {
			t.Fatalf("month %d: shard=%d stream=%d", mm.month, len(shardOrder), len(filtered))
		}
		for i := range shardOrder {
			if shardOrder[i] != filtered[i] {
				t.Errorf("month %d order mismatch at %d: shard=%q stream=%q", mm.month, i, shardOrder[i], filtered[i])
			}
		}
	}
}

// --- shared helpers ----------------------------------------------------------

func date(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 9, 0, 0, 0, time.UTC)
}

var manifestRe = regexp.MustCompile(`(?s)id="cnz-manifest">(.*?)</script>`)

// manifestForScope extracts the embedded manifest from a scope's landing page.
func manifestForScope(t *testing.T, out, scopePath string) PageManifest {
	t.Helper()
	b := readArtifact(t, out, scopePath+"/index.html")
	m := manifestRe.FindSubmatch(b)
	if m == nil {
		t.Fatalf("no manifest in %s landing", scopePath)
	}
	var pm PageManifest
	if err := json.Unmarshal(m[1], &pm); err != nil {
		t.Fatalf("decode manifest for %s: %v", scopePath, err)
	}
	return pm
}

// shardURLs decodes a shard file and returns its entries' urls in order.
func shardURLs(t *testing.T, b []byte) []string {
	t.Helper()
	var entries []ShardEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		t.Fatalf("decode shard: %v", err)
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.URL
	}
	return out
}

// shardFiles walks OutDir for every .json under shards/.
func shardFiles(t *testing.T, out string) []string {
	t.Helper()
	var got []string
	walkRel(t, out, "shards", &got)
	return got
}

func validateJSON(t *testing.T, sch *jsonschema.Schema, b []byte, what string) {
	t.Helper()
	inst := mustUnmarshalAny(t, b)
	if err := sch.Validate(inst); err != nil {
		t.Errorf("%s failed schema validation: %v", what, err)
	}
}

func mustUnmarshalAny(t *testing.T, b []byte) any {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytesReader(b))
	if err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	return v
}

func assertKeySet(t *testing.T, what string, obj map[string]json.RawMessage, want []string) {
	t.Helper()
	if len(obj) != len(want) {
		t.Errorf("%s: %d keys, want %d (%v)", what, len(obj), len(want), keysOf(obj))
	}
	for _, k := range want {
		if _, ok := obj[k]; !ok {
			t.Errorf("%s: missing frozen key %q", what, k)
		}
	}
}
