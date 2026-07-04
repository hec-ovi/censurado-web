package generate

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// This test freezes Layer 1's dependency boundary on the backend (Seam A). Layer 1
// (the generator) may depend on the backend ONLY through the frozen public contract
// packages (domain, store, content, media) via the go.mod replace dep. Library code
// must program to the store.Repository interface; the concrete store/sqlite may be
// constructed only at a composition root (cmd/) or in test fixtures. Any import of a
// backend internal/ package, store/postgres, store/storetest, or a reach into the
// sqlite implementation from library code breaks the boundary and fails here.
//
// It is stdlib-only and offline (go/parser over the module's own .go files), so it
// runs in the network-less Docker build.

const backendModule = "github.com/hec-ovi/censurado-web-backend"

// classify reports how a backend import path relates to the boundary:
//
//	"contract"  - domain | store | content | media (allowed everywhere)
//	"sqlite"    - store/sqlite (allowed only in cmd/ or _test.go)
//	"forbidden" - any other backend path (internal/*, store/postgres, store/storetest,
//	              the bare module, ...): banned everywhere, including tests
//	""          - not a backend import
func classify(imp string) string {
	if imp == backendModule {
		return "forbidden"
	}
	rel := strings.TrimPrefix(imp, backendModule+"/")
	if rel == imp {
		return "" // not under the backend module
	}
	switch {
	case rel == "domain" || strings.HasPrefix(rel, "domain/"),
		rel == "content" || strings.HasPrefix(rel, "content/"),
		rel == "media" || strings.HasPrefix(rel, "media/"),
		rel == "store":
		return "contract"
	case rel == "store/sqlite" || strings.HasPrefix(rel, "store/sqlite/"):
		return "sqlite"
	default:
		return "forbidden"
	}
}

// moduleRoot walks up from this test file to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	d := filepath.Dir(self)
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			t.Fatal("go.mod not found above the test file")
		}
		d = p
	}
}

func TestBackendImportBoundary(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	var goFiles, backendImports, sqliteHits int

	// Skip dot-dirs (.git, .gocache, .gomodcache, .generated), vendored trees, and
	// testdata: parsing the in-repo module cache would be slow and would false-positive
	// on the vendored backend / pgx / x/tools sources.
	skip := func(name string) bool {
		return name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".")
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skip(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		goFiles++
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		rel, _ := filepath.Rel(root, path)
		slash := filepath.ToSlash(rel)
		isTest := strings.HasSuffix(d.Name(), "_test.go")
		isCmd := strings.HasPrefix(slash, "cmd/")
		for _, s := range f.Imports {
			imp, uerr := strconv.Unquote(s.Path.Value)
			if uerr != nil {
				continue
			}
			switch classify(imp) {
			case "contract":
				backendImports++
			case "sqlite":
				backendImports++
				sqliteHits++
				if !isTest && !isCmd {
					t.Errorf("BOUNDARY: %s imports the concrete store %q; library code must program to store.Repository (construct *sqlite.Store only at the cmd composition root)", slash, imp)
				}
			case "forbidden":
				backendImports++
				t.Errorf("BOUNDARY: %s imports forbidden backend path %q; Layer 1 may depend only on domain/store/content/media (plus store/sqlite at cmd/ and in tests)", slash, imp)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Anti-vacuity: a broken walker/classifier must fail loudly, not pass green.
	if goFiles == 0 {
		t.Fatal("walked zero .go files")
	}
	if backendImports == 0 {
		t.Fatal("saw zero backend imports; walker or classifier is broken")
	}
	if sqliteHits == 0 {
		t.Fatal("expected the cmd composition root (and test fixtures) to import store/sqlite")
	}
}

func TestBackendImportClassify(t *testing.T) {
	cases := []struct {
		imp, want string
	}{
		{backendModule + "/domain", "contract"},
		{backendModule + "/store", "contract"},
		{backendModule + "/content", "contract"},
		{backendModule + "/media", "contract"},
		{backendModule + "/store/sqlite", "sqlite"},
		{backendModule + "/store/postgres", "forbidden"},
		{backendModule + "/store/storetest", "forbidden"},
		{backendModule + "/internal/publish", "forbidden"},
		{backendModule, "forbidden"},
		{"fmt", ""},
		{"github.com/hec-ovi/censurado-web/internal/generate", ""},
	}
	for _, c := range cases {
		if got := classify(c.imp); got != c.want {
			t.Errorf("classify(%q) = %q, want %q", c.imp, got, c.want)
		}
	}
}
