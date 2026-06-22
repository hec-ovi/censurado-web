package generate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The load-bearing invariant after D1-D5: with the new shared shell and head tags
// in place, a previously-sealed listing page AND a sealed (non-highest) shard part
// keep their FULL bytes when the archive's tail grows. If any of the manifest link,
// asset links, SEO/social meta, or JSON-LD leaked tail-derived data onto a sealed
// artifact, these byte comparisons would fail.
func TestImmutability_SealedArtifactsStableAcrossGrowth(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	opts := func(o *Options) { o.PageSize = 3; o.ShardMaxEntries = 2 }
	for i := 0; i < 7; i++ {
		seed(t, repo, seedSpec{Title: "IM " + string(rune('a'+i)), Author: "ada", Section: "tech", Published: pin.Add(-time.Duration(i) * time.Hour)})
	}
	genInto(t, repo, out, opts)

	const (
		sealedPage  = "latest/page/1/index.html"
		sealedShard = "shards/latest/2026/06.json" // part 1 (lowest), sealed once higher parts exist
	)
	if !exists(out, sealedPage) || !exists(out, sealedShard) || !exists(out, "shards/latest/2026/06.2.json") {
		t.Fatalf("setup expected a sealed page and a multi-part (sealed) shard month")
	}
	pageBefore := string(readArtifact(t, out, sealedPage))
	shardBefore := string(readArtifact(t, out, sealedShard))

	// The new shell and head tags really are on the sealed page (so this test is
	// proving stability of the exact markup the next workflow will style).
	for _, frag := range []string{
		`<link rel="manifest" href="/manifest/latest/index.json">`,
		`<link rel="stylesheet" href="/assets/style.css">`,
		`<meta property="og:type" content="website">`,
		`<script type="application/ld+json">`,
		`<header class="site-header"`,
		`<main class="site-main" id="main">`,
		`<footer class="site-footer"`,
		`<script src="/assets/app.js" defer></script>`,
	} {
		if !strings.Contains(pageBefore, frag) {
			t.Fatalf("sealed page missing expected shell fragment %q", frag)
		}
	}

	// Grow the tail: newer-published, newest-inserted articles in the same month.
	for i := 0; i < 4; i++ {
		seed(t, repo, seedSpec{Title: "IM new " + string(rune('a'+i)), Author: "ada", Section: "tech", Published: pin.Add(time.Duration(i+1) * time.Hour)})
	}
	res := genInto(t, repo, out, opts)

	if string(readArtifact(t, out, sealedPage)) != pageBefore {
		t.Errorf("sealed listing page bytes changed after the tail grew")
	}
	if string(readArtifact(t, out, sealedShard)) != shardBefore {
		t.Errorf("sealed shard part bytes changed after the tail grew")
	}
	for _, u := range res.Purge {
		if u == "/latest/page/1/" || u == "/shards/latest/2026/06.json" {
			t.Errorf("sealed artifact appeared in purge: %q", u)
		}
	}
}

// TestImmutability_SealedPageSurvivesNewMonth proves the cross-month case the
// single-month growth test above does not exercise: when a brand-new, strictly
// later month is added to a scope, the scope's manifest genuinely grows (more
// shard-month entries and a larger total), yet every already-sealed listing page
// in that scope stays byte-identical. That is the whole point of never embedding
// the manifest on sealed pages: a sealed page carries only the scope-derived
// <link rel="manifest"> href (a function of the scope, not the tail), while the
// growing manifest payload lives inline on the LANDING and in the standalone
// /manifest/<ShardKey>/index.json artifact. The manifest-growth assertions keep
// the byte-stability check honest: they prove the index really changed, so the
// sealed page surviving it is meaningful and not a vacuous no-op pass.
func TestImmutability_SealedPageSurvivesNewMonth(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	opts := func(o *Options) { o.PageSize = 3 }

	// Seven articles in an EARLY month, one section, so the section seals page 1.
	for i := 0; i < 7; i++ {
		seed(t, repo, seedSpec{Title: "NM mar " + string(rune('a'+i)), Author: "ada", Section: "imnm", Published: date(2026, 3, i+1)})
	}
	genInto(t, repo, out, opts)

	const (
		sealedPage    = "section/imnm/page/1/index.html"
		landingPage   = "section/imnm/index.html"
		scopeManifest = "manifest/section/imnm/index.json"
	)
	if !exists(out, sealedPage) {
		t.Fatalf("setup expected a sealed section page %q", sealedPage)
	}

	sealedBefore := string(readArtifact(t, out, sealedPage))
	landingBefore := string(readArtifact(t, out, landingPage))
	manifestBefore := readArtifact(t, out, scopeManifest)

	var pmBefore PageManifest
	if err := json.Unmarshal(manifestBefore, &pmBefore); err != nil {
		t.Fatalf("decode manifest before: %v", err)
	}

	// The sealed page must carry ONLY the scope-derived link, never the inline
	// manifest payload: that is the invariant the new-month growth below stresses.
	if strings.Contains(sealedBefore, `id="cnz-manifest"`) {
		t.Fatalf("sealed page already embeds the inline manifest; precondition broken")
	}
	if !strings.Contains(sealedBefore, `<link rel="manifest" href="/manifest/section/imnm/index.json">`) {
		t.Fatalf("sealed page missing its scope manifest link")
	}
	if hasMonth(pmBefore.Shards, "2026-06") {
		t.Fatalf("manifest unexpectedly already carried the later month before it was seeded")
	}

	// Add articles in a strictly LATER month, same section. The scope's manifest
	// gains a new shard-month entry and its total grows; the tail (landing) moves.
	for i := 0; i < 4; i++ {
		seed(t, repo, seedSpec{Title: "NM jun " + string(rune('a'+i)), Author: "ada", Section: "imnm", Published: date(2026, 6, i+1)})
	}
	genInto(t, repo, out, opts)

	sealedAfter := string(readArtifact(t, out, sealedPage))
	landingAfter := string(readArtifact(t, out, landingPage))
	manifestAfter := readArtifact(t, out, scopeManifest)

	var pmAfter PageManifest
	if err := json.Unmarshal(manifestAfter, &pmAfter); err != nil {
		t.Fatalf("decode manifest after: %v", err)
	}

	// Load-bearing: the sealed listing page is byte-identical across the new month.
	if sealedAfter != sealedBefore {
		t.Errorf("sealed listing page bytes changed after a new month was added")
	}

	// Make that meaningful: the standalone manifest really did grow.
	if string(manifestAfter) == string(manifestBefore) {
		t.Errorf("scope manifest bytes did not change; the new-month growth never happened")
	}
	if len(pmAfter.Shards) <= len(pmBefore.Shards) {
		t.Errorf("manifest shard-month entries did not grow: before=%d after=%d", len(pmBefore.Shards), len(pmAfter.Shards))
	}
	if pmAfter.Total <= pmBefore.Total {
		t.Errorf("manifest total did not grow: before=%d after=%d", pmBefore.Total, pmAfter.Total)
	}
	if !hasMonth(pmAfter.Shards, "2026-06") {
		t.Errorf("manifest missing the newly added later month 2026-06")
	}

	// The tail did move: the landing page bytes changed.
	if landingAfter == landingBefore {
		t.Errorf("landing page bytes did not change after the tail grew")
	}
}

// hasMonth reports whether refs holds a shard for the given "yyyy-mm" month.
func hasMonth(refs []ShardRef, month string) bool {
	for _, r := range refs {
		if r.Month == month {
			return true
		}
	}
	return false
}
