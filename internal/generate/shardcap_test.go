package generate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web-backend/domain"
)

func mkEntry(i int, ts int64, title string) ShardEntry {
	return ShardEntry{
		Slug:        fmt.Sprintf("a%d", i),
		URL:         fmt.Sprintf("/a/a%d-%08x/", i, i),
		Title:       title,
		Author:      "ada",
		AuthorLabel: "ada",
		Section:     "tech",
		Topics:      []string{},
		PublishedAt: time.Unix(ts, 0).UTC().Format(time.RFC3339),
		TS:          ts,
		id:          strconv.Itoa(i),
	}
}

func entries(n int) []ShardEntry {
	out := make([]ShardEntry, n)
	for i := 0; i < n; i++ {
		out[i] = mkEntry(i, int64(1_700_000_000+i), "Title "+strconv.Itoa(i))
	}
	return out
}

func incompressible(seed, n int) string {
	var sb strings.Builder
	h := sha256.Sum256([]byte{byte(seed), byte(seed >> 8)})
	for sb.Len() < n {
		h = sha256.Sum256(h[:])
		sb.WriteString(hex.EncodeToString(h[:]))
	}
	return sb.String()[:n]
}

// S1
func TestShardEntryOf_Projection(t *testing.T) {
	now := pin
	mk := func(in domain.PublishInput, pub time.Time) domain.Article {
		in.PublishedAt = &pub
		a, err := domain.NewArticle(in, now)
		if err != nil {
			t.Fatalf("NewArticle: %v", err)
		}
		a.ID = "1"
		a.PublishedAt = a.PublishedAt.UTC().Truncate(time.Second)
		return a
	}

	a := mk(domain.PublishInput{Title: "Hello World", Body: "b", Author: "Ada L.", Section: "Tech News", Topics: []string{"IaC", "Go", "go", "  "}}, date(2026, 6, 3))
	e := ShardEntryOf(a)
	wantKeys := frozenShardKeys
	b, _ := json.Marshal(e)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertKeySet(t, "S1 entry", m, wantKeys)
	if e.Author != "ada-l" || e.Section != "tech-news" {
		t.Errorf("slug author/section = %q/%q", e.Author, e.Section)
	}
	if e.AuthorLabel != "Ada L." {
		t.Errorf("author_label = %q", e.AuthorLabel)
	}
	if e.URL != "/a/"+a.Slug+"-"+hash8(a.ContentHash)+"/" {
		t.Errorf("url = %q", e.URL)
	}
	// "Go" and "go" collapse to one slug; the blank topic is dropped.
	if len(e.Topics) != 2 || e.Topics[0] != "iac" || e.Topics[1] != "go" {
		t.Errorf("topics = %v, want [iac go]", e.Topics)
	}
	if e.PublishedAt != a.PublishedAt.UTC().Format(time.RFC3339) {
		t.Errorf("published_at = %q", e.PublishedAt)
	}
	if e.TS != a.PublishedAt.Unix() {
		t.Errorf("ts = %d", e.TS)
	}

	// Empty topics -> [] (never nil).
	noTopics := mk(domain.PublishInput{Title: "No Topics", Body: "b", Author: "ada", Section: "tech"}, date(2026, 6, 3))
	if ts := ShardEntryOf(noTopics).Topics; ts == nil || len(ts) != 0 {
		t.Errorf("empty topics = %v, want []", ts)
	}
	jb, _ := marshalShard([]ShardEntry{ShardEntryOf(noTopics)})
	if !strings.Contains(string(jb), `"topics":[]`) {
		t.Errorf("empty topics not emitted as []: %s", jb)
	}

	// Empty section/author slug -> ContentHash[:12] fallback.
	fallback := mk(domain.PublishInput{Title: "Fallback", Body: "b", Author: "日本語", Section: "日本語", Topics: []string{"中文"}}, date(2026, 6, 3))
	fe := ShardEntryOf(fallback)
	if fe.Section != fallback.ContentHash[:12] || fe.Author != fallback.ContentHash[:12] {
		t.Errorf("fallback section/author = %q/%q, want %q", fe.Section, fe.Author, fallback.ContentHash[:12])
	}
	if len(fe.Topics) != 0 {
		t.Errorf("non-ascii topic should be dropped, got %v", fe.Topics)
	}
}

// S2
func TestEmitShards_AppendOnly(t *testing.T) {
	in := entries(4)
	before, err := EmitShardsForScopeMonth("latest", 2026, 6, in, 2, 1<<20)
	if err != nil {
		t.Fatalf("emit before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("before parts = %d, want 2", len(before))
	}

	newer := append(append([]ShardEntry{}, in...), mkEntry(99, int64(1_700_000_099), "Newest"))
	after, err := EmitShardsForScopeMonth("latest", 2026, 6, newer, 2, 1<<20)
	if err != nil {
		t.Fatalf("emit after: %v", err)
	}
	if len(after) != 3 {
		t.Fatalf("after parts = %d, want 3", len(after))
	}
	// Every part except the new last one is byte-identical.
	for i := 0; i < len(before); i++ {
		if string(before[i].Bytes) != string(after[i].Bytes) {
			t.Errorf("part %d changed after appending a newer entry", i+1)
		}
	}
	// The newest entry is in the new last part.
	if !strings.Contains(string(after[2].Bytes), "/a/a99-") {
		t.Errorf("new entry not in last part: %s", after[2].Bytes)
	}
}

// S3
func TestEmitShards_Pure(t *testing.T) {
	in := entries(7)
	a, err := EmitShardsForScopeMonth("latest", 2026, 6, in, 3, 1<<20)
	if err != nil {
		t.Fatalf("emit a: %v", err)
	}
	b, err := EmitShardsForScopeMonth("latest", 2026, 6, in, 3, 1<<20)
	if err != nil {
		t.Fatalf("emit b: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("part counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if string(a[i].Bytes) != string(b[i].Bytes) || a[i].Path != b[i].Path {
			t.Errorf("part %d not byte-identical across runs", i+1)
		}
	}
}

// S4
func TestEmitShards_GzipByteCap(t *testing.T) {
	const capB = 1024
	in := make([]ShardEntry, 12)
	for i := range in {
		in[i] = mkEntry(i, int64(1_700_000_000+i), incompressible(i, 200))
	}
	parts, err := EmitShardsForScopeMonth("latest", 2026, 6, in, 1000, capB)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if len(parts) < 2 {
		t.Fatalf("expected a byte-cap split, got %d parts", len(parts))
	}
	for _, p := range parts {
		// A part may exceed the cap only when it is a single (lone over-cap) entry.
		if p.Count > 1 && gzipLen(p.Bytes) > capB {
			t.Errorf("multi-entry part %d gzipLen %d > cap %d", p.Part, gzipLen(p.Bytes), capB)
		}
	}

	// A single entry larger than the cap is emitted as its own part (no loop).
	huge := []ShardEntry{mkEntry(0, 1_700_000_000, incompressible(0, 5000))}
	lone, err := EmitShardsForScopeMonth("latest", 2026, 6, huge, 1000, 256)
	if err != nil {
		t.Fatalf("emit lone: %v", err)
	}
	if len(lone) != 1 || lone[0].Count != 1 {
		t.Fatalf("lone over-cap entry: %d parts", len(lone))
	}
	if gzipLen(lone[0].Bytes) <= 256 {
		t.Errorf("expected the lone entry to exceed the cap (exercising the guard)")
	}
}

// S5
func TestEmitShards_WholeSecondPrecision(t *testing.T) {
	repo := newStore(t)
	out := t.TempDir()
	// Two articles in the same wall-clock second, sub-second apart at ingress.
	a1 := seed(t, repo, seedSpec{Title: "Precision A", Author: "ada", Section: "tech", Published: time.Date(2026, 6, 3, 9, 0, 30, 200_000_000, time.UTC)})
	a2 := seed(t, repo, seedSpec{Title: "Precision B", Author: "ada", Section: "tech", Published: time.Date(2026, 6, 3, 9, 0, 30, 800_000_000, time.UTC)})
	genInto(t, repo, out, nil)

	var es []ShardEntry
	if err := json.Unmarshal(readArtifact(t, out, "shards/section/tech/2026/06.json"), &es); err != nil {
		t.Fatalf("decode shard: %v", err)
	}
	if len(es) != 2 {
		t.Fatalf("entries = %d, want 2", len(es))
	}
	if es[0].TS != es[1].TS {
		t.Errorf("ts differ after truncation: %d vs %d", es[0].TS, es[1].TS)
	}
	// Tie broken by id desc: the later-inserted (higher id) comes first.
	if es[0].Slug != a2.Slug || es[1].Slug != a1.Slug {
		t.Errorf("tie order = [%q %q], want [%q %q]", es[0].Slug, es[1].Slug, a2.Slug, a1.Slug)
	}

	// Direct projection: sub-second times truncate to the same second and bytes.
	mk := func(ns int) domain.Article {
		return domain.Article{
			ID: "1", Slug: "x", Title: "t", Author: "ada", Section: "tech",
			ContentHash: strings.Repeat("a", 64),
			PublishedAt: time.Date(2026, 6, 3, 9, 0, 30, ns, time.UTC).UTC().Truncate(time.Second),
		}
	}
	if ShardEntryOf(mk(200_000_000)).PublishedAt != ShardEntryOf(mk(800_000_000)).PublishedAt {
		t.Errorf("sub-second projections differ after truncation")
	}
}
