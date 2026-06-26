package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web-backend/domain"
	"github.com/hec-ovi/censurado-web-backend/store"
	"github.com/hec-ovi/censurado-web-backend/store/sqlite"
)

// pin is the frozen build clock used by every test: 2026-06-22T12:00:00Z.
var pin = time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

// seedSpec describes one article to seed. Created defaults to pin, so insertion
// order (CreatedAt ASC, id ASC) follows seed order via the autoincrement id.
type seedSpec struct {
	Title     string
	Author    string
	Section   string
	Topics    []string
	Body      string
	Published time.Time
	Created   time.Time
	Metadata  map[string]any
}

// newStore opens a fresh sqlite store in a temp dir, closed on cleanup.
func newStore(t *testing.T) store.Repository {
	t.Helper()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

// seed inserts one article via domain.NewArticle + repo.Upsert and returns the
// stored article (with its assigned id, slug, and content hash).
func seed(t *testing.T, repo store.Repository, s seedSpec) domain.Article {
	t.Helper()
	created := s.Created
	if created.IsZero() {
		created = pin
	}
	body := s.Body
	if body == "" {
		body = "Body of " + s.Title + "."
	}
	pub := s.Published
	in := domain.PublishInput{
		Title:    s.Title,
		Body:     body,
		Author:   s.Author,
		Section:  s.Section,
		Topics:   s.Topics,
		Metadata: s.Metadata,
	}
	if !pub.IsZero() {
		in.PublishedAt = &pub
	}
	a, err := domain.NewArticle(in, created)
	if err != nil {
		t.Fatalf("NewArticle(%q): %v", s.Title, err)
	}
	res, err := repo.Upsert(context.Background(), a)
	if err != nil {
		t.Fatalf("Upsert(%q): %v", s.Title, err)
	}
	if !res.Created {
		t.Fatalf("Upsert(%q): not created (duplicate content hash?)", s.Title)
	}
	return res.Article
}

// matrixSpecs loads the documented S=2,A=3,T=4,M=2 fixture from testdata.
//
// Co-occurrence table (sections x active sub-facets):
//
//	tech    : authors{ada,lin}=2  topics{ai,go}=2     months{05,06}=2
//	science : authors{bob,ada,lin}=3 topics{bio,space}=2 months{05,06}=2
//
// ScopeCount = 1 + S + A + T + M + sum_s(A_active+T_active+M_active)
//
//	= 1 + 2 + 3 + 4 + 2 + (2+2+2) + (3+2+2) = 25.
func matrixSpecs(t *testing.T) []seedSpec {
	t.Helper()
	b, err := os.ReadFile("testdata/fixture.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var raw []struct {
		Title     string   `json:"title"`
		Author    string   `json:"author"`
		Section   string   `json:"section"`
		Topics    []string `json:"topics"`
		Published string   `json:"published"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	out := make([]seedSpec, 0, len(raw))
	for _, r := range raw {
		pub, err := time.Parse(time.RFC3339, r.Published)
		if err != nil {
			t.Fatalf("parse published %q: %v", r.Published, err)
		}
		out = append(out, seedSpec{
			Title:     r.Title,
			Author:    r.Author,
			Section:   r.Section,
			Topics:    r.Topics,
			Published: pub,
		})
	}
	return out
}

// seedMatrix seeds the documented fixture into repo in file order.
func seedMatrix(t *testing.T, repo store.Repository) {
	t.Helper()
	for _, s := range matrixSpecs(t) {
		seed(t, repo, s)
	}
}

// genInto runs Generate over repo into outDir with sensible test defaults and
// any overrides applied by mutate.
func genInto(t *testing.T, repo store.Repository, outDir string, mutate func(*Options)) Result {
	t.Helper()
	opts := Options{
		OutDir:   outDir,
		BaseURL:  "https://news.example",
		PageSize: 20,
		SiteName: "Censurado",
		Now:      pin,
	}
	if mutate != nil {
		mutate(&opts)
	}
	res, err := Generate(context.Background(), repo, opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return res
}

// stateDir is the default StateDir for an OutDir.
func stateDir(outDir string) string { return filepath.Join(outDir, ".generated") }

// readArtifact reads an OutDir-relative artifact, failing if absent.
func readArtifact(t *testing.T, outDir, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(outDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

// exists reports whether an OutDir-relative path exists.
func exists(outDir, rel string) bool {
	_, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(rel)))
	return err == nil
}

// decodePurge reads and decodes StateDir/purge.json.
func decodePurge(t *testing.T, outDir string) purgeFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(stateDir(outDir), "purge.json"))
	if err != nil {
		t.Fatalf("read purge.json: %v", err)
	}
	var pf purgeFile
	if err := json.Unmarshal(b, &pf); err != nil {
		t.Fatalf("decode purge.json: %v", err)
	}
	return pf
}

var permalinkRe = regexp.MustCompile(`/a/[a-z0-9]+(?:-[a-z0-9]+)*-[0-9a-f]{8}/`)

// articleListRe isolates the main listing's <ol data-articles> ... </ol> block so
// permalinksIn counts only the listed article cards, not the permalinks now also
// carried by the clickable "Lo más leído" rail on the same page.
var articleListRe = regexp.MustCompile(`(?s)<ol class="article-list" data-articles>(.*?)</ol>`)

// permalinksIn returns every /a/<slug>-<hash8>/ URL among the listed article
// cards, in order of appearance. When the page has no article list it falls back
// to scanning the whole document.
func permalinksIn(b []byte) []string {
	s := string(b)
	if m := articleListRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	return permalinkRe.FindAllString(s, -1)
}

// strSet builds a set from a slice.
func strSet(in []string) map[string]bool {
	m := make(map[string]bool, len(in))
	for _, s := range in {
		m[s] = true
	}
	return m
}

// sortedCopy returns a sorted copy of in.
func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// keysOf returns the keys of a raw-message map.
func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// walkRel appends every regular file under outDir/relRoot to got, as an
// OutDir-relative POSIX path.
func walkRel(t *testing.T, outDir, relRoot string, got *[]string) {
	t.Helper()
	root := filepath.Join(outDir, filepath.FromSlash(relRoot))
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(outDir, p)
		if err != nil {
			return err
		}
		*got = append(*got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", relRoot, err)
	}
}

// entryToMap round-trips a ShardEntry through JSON into a key->value map.
func entryToMap(t *testing.T, se ShardEntry) map[string]any {
	t.Helper()
	b, err := json.Marshal(se)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal entry: %v", err)
	}
	return m
}

// withExtraKey returns a one-element shard array whose entry carries an extra key.
func withExtraKey(t *testing.T, se ShardEntry, key, val string) []byte {
	t.Helper()
	m := entryToMap(t, se)
	m[key] = val
	b, err := json.Marshal([]map[string]any{m})
	if err != nil {
		t.Fatalf("marshal extra: %v", err)
	}
	return b
}

// withoutKey returns a one-element shard array whose entry lacks a required key.
func withoutKey(t *testing.T, se ShardEntry, key string) []byte {
	t.Helper()
	m := entryToMap(t, se)
	delete(m, key)
	b, err := json.Marshal([]map[string]any{m})
	if err != nil {
		t.Fatalf("marshal without: %v", err)
	}
	return b
}

// requireSameSet fails unless got and want contain exactly the same elements.
func requireSameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	gs, ws := strSet(got), strSet(want)
	for s := range ws {
		if !gs[s] {
			t.Errorf("%s: missing expected %q", what, s)
		}
	}
	for s := range gs {
		if !ws[s] {
			t.Errorf("%s: unexpected %q", what, s)
		}
	}
	if t.Failed() {
		t.Logf("%s got=%v", what, sortedCopy(got))
		t.Logf("%s want=%v", what, sortedCopy(want))
	}
}
