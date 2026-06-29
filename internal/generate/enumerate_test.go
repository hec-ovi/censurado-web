package generate

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// E1
func TestEnumerateBoundedness(t *testing.T) {
	repo := newStore(t)
	seedMatrix(t, repo)
	plan, err := BuildPlan(context.Background(), repo, 20)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got := len(plan.Index.Scopes()); got != 25 {
		t.Errorf("ScopeCount = %d, want 25", got)
	}
	// Every scope holds <= PageSize, so one landing page each: 25 total.
	if got := len(plan.Pages); got != 25 {
		t.Errorf("page count = %d, want 25", got)
	}
}

// E2
func TestEmptySectionMonthRule(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// tech only in May, science only in June: the cross (tech,June) never occurs.
	seed(t, repo, seedSpec{Title: "Tech May", Author: "ada", Section: "tech", Published: date(2026, 5, 4)})
	seed(t, repo, seedSpec{Title: "Sci June", Author: "lin", Section: "science", Published: date(2026, 6, 4)})
	genInto(t, repo, out, nil)

	if exists(out, "section/tech/2026/06/index.html") {
		t.Errorf("emitted /section/tech/2026/06/ with no co-occurrence")
	}
	if exists(out, "section/science/2026/05/index.html") {
		t.Errorf("emitted /section/science/2026/05/ with no co-occurrence")
	}
	if !exists(out, "section/tech/2026/05/index.html") {
		t.Errorf("missing /section/tech/2026/05/")
	}
	// No bare month page for an empty month.
	if exists(out, "2026/04/index.html") {
		t.Errorf("emitted a bare /2026/04/ for an empty month")
	}

	// Bridge: a tech article in June now makes (tech,June) co-occur.
	repo2 := newStore(t)
	out2 := t.TempDir()
	seed(t, repo2, seedSpec{Title: "Tech May", Author: "ada", Section: "tech", Published: date(2026, 5, 4)})
	seed(t, repo2, seedSpec{Title: "Tech June", Author: "ada", Section: "tech", Published: date(2026, 6, 4)})
	genInto(t, repo2, out2, nil)
	if !exists(out2, "section/tech/2026/06/index.html") {
		t.Errorf("bridge article did not create /section/tech/2026/06/")
	}
}

// E3
func TestSlugifyFolding(t *testing.T) {
	repo := newStore(t)
	seed(t, repo, seedSpec{Title: "Folding One", Author: "Ada", Section: "Tech", Topics: []string{"Go!", "go"}, Published: date(2026, 6, 1)})
	seed(t, repo, seedSpec{Title: "Folding Two", Author: "ada", Section: "tech", Topics: []string{"go"}, Published: date(2026, 6, 2)})

	idx, err := BuildIndex(context.Background(), repo)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(idx.sections["tech"]) != 2 {
		t.Errorf("section tech members = %d, want 2", len(idx.sections["tech"]))
	}
	if idx.sectionLabel["tech"] != "Tech" {
		t.Errorf("section label = %q, want earliest-inserted %q", idx.sectionLabel["tech"], "Tech")
	}
	if len(idx.authors["ada"]) != 2 {
		t.Errorf("author ada members = %d, want 2", len(idx.authors["ada"]))
	}
	if idx.authorLabel["ada"] != "Ada" {
		t.Errorf("author label = %q, want %q", idx.authorLabel["ada"], "Ada")
	}
	// Two topics colliding to "go" count the article once.
	if got := len(idx.topics["go"]); got != 2 {
		t.Errorf("topic go members = %d, want 2 (one per article, no double count)", got)
	}
	// Only one /section/tech/ scope and one /topic/go/ scope.
	sectionScopes := 0
	for _, s := range idx.Scopes() {
		if s.isSingle() && s.Section.Kind == AxisSection && s.Section.Value == "tech" {
			sectionScopes++
		}
	}
	if sectionScopes != 1 {
		t.Errorf("section/tech scope count = %d, want 1", sectionScopes)
	}
}

// E5
func TestAppendOnlyOnInsert(t *testing.T) {
	scopePaths := []string{
		"latest", "section/s", "author/a", "topic/t", "2026/06",
		"section/s/author/a", "section/s/topic/t", "section/s/2026/06",
	}
	run := func(t *testing.T, count int, expectSeal bool, backdate bool) {
		repo := newStore(t)
		out := t.TempDir()
		for i := 0; i < count; i++ {
			seed(t, repo, seedSpec{
				Title:     "Shared " + string(rune('A'+i)),
				Author:    "a",
				Section:   "s",
				Topics:    []string{"t"},
				Published: date(2026, 6, 1).Add(time.Duration(i) * time.Hour),
			})
		}
		genInto(t, repo, out, func(o *Options) { o.PageSize = 3 })

		before := map[string][]byte{}
		for _, sp := range scopePaths {
			for _, n := range []int{1, 2} {
				rel := sp + "/page/" + strconv.Itoa(n) + "/index.html"
				if exists(out, rel) {
					before[rel] = readArtifact(t, out, rel)
				}
			}
		}
		if len(before) == 0 {
			t.Fatalf("no sealed pages snapshotted for count=%d", count)
		}

		pub := date(2026, 6, 1).Add(time.Duration(count+5) * time.Hour)
		if backdate {
			pub = date(2026, 6, 1) // oldest published, but still the newest insert
		}
		seed(t, repo, seedSpec{Title: "Inserted", Author: "a", Section: "s", Topics: []string{"t"}, Published: pub})
		genInto(t, repo, out, func(o *Options) { o.PageSize = 3 })

		for rel, b := range before {
			if !exists(out, rel) {
				t.Errorf("sealed page %s vanished after insert", rel)
				continue
			}
			// Appending a NEWEST-published article leaves older pages byte-identical; a
			// back-dated insert legitimately re-cuts pages (publication-order pagination),
			// so immutability is asserted only for the append case.
			if !backdate && string(readArtifact(t, out, rel)) != string(b) {
				t.Errorf("page %s changed after appending a newest article (count=%d)", rel, count)
			}
		}

		newPage3 := exists(out, "latest/page/3/index.html")
		if expectSeal && !newPage3 {
			t.Errorf("count=%d: expected a new sealed page/3", count)
		}
		if !expectSeal && newPage3 {
			t.Errorf("count=%d: unexpected new sealed page/3", count)
		}
	}

	t.Run("rem_before<P-1 now", func(t *testing.T) { run(t, 7, false, false) })
	t.Run("rem_before<P-1 backdated", func(t *testing.T) { run(t, 7, false, true) })
	t.Run("rem_before==P-1 now", func(t *testing.T) { run(t, 8, true, false) })
	t.Run("rem_before==P-1 backdated", func(t *testing.T) { run(t, 8, true, true) })
}

// E7
func TestFilterForBuildability(t *testing.T) {
	repo := newStore(t)
	seedMatrix(t, repo)
	idx, err := BuildIndex(context.Background(), repo)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	for _, sc := range idx.Scopes() {
		wantSlugs := map[string]bool{}
		for _, i := range idx.scopeIndices(sc) {
			wantSlugs[idx.All[i].Slug] = true
		}
		got, err := repo.Find(context.Background(), sc.FilterFor())
		if err != nil {
			t.Fatalf("Find(%s): %v", sc.PathPrefix(), err)
		}
		gotSlugs := map[string]bool{}
		for _, a := range got {
			gotSlugs[a.Slug] = true
		}
		if len(gotSlugs) != len(wantSlugs) {
			t.Errorf("scope %s: Find returned %d, scopeIndices %d", sc.PathPrefix(), len(gotSlugs), len(wantSlugs))
		}
		for s := range wantSlugs {
			if !gotSlugs[s] {
				t.Errorf("scope %s: Find missing slug %q", sc.PathPrefix(), s)
			}
		}
	}
}

// E8
func TestEmptyArchive(t *testing.T) {
	repo := newStore(t)
	plan, err := BuildPlan(context.Background(), repo, 20)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Index.Scopes()) != 0 {
		t.Errorf("empty archive scopes = %d, want 0", len(plan.Index.Scopes()))
	}
	if len(plan.Pages) != 0 {
		t.Errorf("empty archive pages = %d, want 0", len(plan.Pages))
	}
	for _, s := range plan.Index.Scopes() {
		if isLatest(s) {
			t.Errorf("empty archive emitted a /latest/ scope")
		}
	}
}

// E9
func TestAuthorSlugCollisionMembership(t *testing.T) {
	repo := newStore(t)
	a1 := seed(t, repo, seedSpec{Title: "Collide One", Author: "A.B.", Section: "tech", Published: date(2026, 6, 1)})
	a2 := seed(t, repo, seedSpec{Title: "Collide Two", Author: "a b", Section: "tech", Published: date(2026, 6, 2)})

	idx, err := BuildIndex(context.Background(), repo)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(idx.authors["a-b"]) != 2 {
		t.Fatalf("author a-b members = %d, want 2 (both distinct authors fold)", len(idx.authors["a-b"]))
	}
	authorScopes := 0
	for _, s := range idx.Scopes() {
		if s.isSingle() && s.Section.Kind == AxisAuthor && s.Section.Value == "a-b" {
			authorScopes++
		}
	}
	if authorScopes != 1 {
		t.Errorf("author a-b scopes = %d, want 1", authorScopes)
	}
	if e := ShardEntryOf(a1); e.Author != "a-b" {
		t.Errorf("a1 shard author = %q, want a-b", e.Author)
	}
	if e := ShardEntryOf(a2); e.Author != "a-b" {
		t.Errorf("a2 shard author = %q, want a-b", e.Author)
	}
	if idx.authorLabel["a-b"] != "A.B." {
		t.Errorf("author label = %q, want earliest-inserted %q", idx.authorLabel["a-b"], "A.B.")
	}
}
