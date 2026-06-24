package cachepolicy_test

import (
	"testing"

	"github.com/hec-ovi/censurado-web/internal/cachepolicy"
)

func TestCacheControl_Classification(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// Immutable: content-addressed paths.
		{"/a/go-1-26-ships-e920e886/", cachepolicy.Immutable},
		{"/a/go-1-26-ships-e920e886/index.html", cachepolicy.Immutable},
		{"/assets/style.css", cachepolicy.Immutable},
		{"/assets/app.js", cachepolicy.Immutable},
		{"/media/" + repeatHex() + ".jpg", cachepolicy.Immutable},

		// Sentinel: the one tight-TTL exact match, even though it sits under /latest/.
		{"/latest/version.json", cachepolicy.Sentinel},

		// Listings: everything mutable, including the /latest/ landing itself.
		{"/", cachepolicy.Listings},
		{"/latest/", cachepolicy.Listings},
		{"/latest/page/2/", cachepolicy.Listings},
		{"/section/tech/", cachepolicy.Listings},
		{"/topic/go/", cachepolicy.Listings},
		{"/author/ada/", cachepolicy.Listings},
		{"/2026/06/", cachepolicy.Listings},
		{"/shards/latest/2026/06.json", cachepolicy.Listings},
		{"/manifest/latest/index.json", cachepolicy.Listings},
		{"/feed.xml", cachepolicy.Listings},
		{"/atom.xml", cachepolicy.Listings},
		{"/feed.json", cachepolicy.Listings},
		{"/sitemap.xml", cachepolicy.Listings},
		{"/robots.txt", cachepolicy.Listings},
	}
	for _, tc := range cases {
		if got := cachepolicy.CacheControl(tc.path); got != tc.want {
			t.Errorf("CacheControl(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestRules_MatchCacheControl(t *testing.T) {
	rules := cachepolicy.Rules()
	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 3", len(rules))
	}
	// The last rule must be the catch-all default.
	if !rules[len(rules)-1].Default {
		t.Errorf("final rule is not the default: %+v", rules[len(rules)-1])
	}
	// The header values are exactly the three exported classes.
	want := map[string]bool{cachepolicy.Sentinel: true, cachepolicy.Immutable: true, cachepolicy.Listings: true}
	for _, r := range rules {
		if !want[r.CacheControl] {
			t.Errorf("rule %q carries an unknown Cache-Control %q", r.Description, r.CacheControl)
		}
	}
	// Applying the rules by hand to representative paths matches CacheControl.
	if rules[0].Exact != "/latest/version.json" || rules[0].CacheControl != cachepolicy.CacheControl("/latest/version.json") {
		t.Errorf("sentinel rule does not match CacheControl: %+v", rules[0])
	}
}

func repeatHex() string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = "0123456789abcdef"[i%16]
	}
	return string(out)
}
