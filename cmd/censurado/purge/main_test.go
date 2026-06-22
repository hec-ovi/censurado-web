package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/generate"
	"github.com/hec-ovi/censurado-web/internal/store/sqlite"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "purge.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

const cliManifest = `{"version":1,"generated_at":"2026-06-22T12:00:00Z","urls":["/latest/","/index.json"]}`

func TestCmd_DryRunDefault(t *testing.T) {
	t.Setenv("CENSURADO_PURGE_FILE", "")
	file := writeManifest(t, cliManifest)
	var stdout, stderr bytes.Buffer
	// No -provider flag: dryrun is the safe default.
	code := run([]string{"-file", file}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (%s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"dry-run", "/latest/", "/index.json", "provider=dryrun", "succeeded=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q: %s", want, out)
		}
	}
}

func TestCmd_EmptyManifestNoOp(t *testing.T) {
	file := writeManifest(t, `{"version":1,"generated_at":"x","urls":[]}`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-file", file}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "succeeded=0") {
		t.Errorf("want succeeded=0 no-op: %s", stdout.String())
	}
}

// TestCmd_DefaultFileMatchesGeneratorOutput is the end-to-end wiring check: it runs
// the real generator, then runs the purge CLI with NO -file flag, and asserts the
// default path resolves to where the generator actually wrote the manifest
// (<out>/.generated/purge.json). The generator writes the manifest into its state
// dir, not the out-dir root, so a default of ./public/purge.json (the old value)
// would open-fail and exit 1. This pins the producer/consumer path contract.
func TestCmd_DefaultFileMatchesGeneratorOutput(t *testing.T) {
	pin := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	tmp := t.TempDir()
	t.Chdir(tmp) // default -file is "./public/.generated/purge.json", resolved from here

	repo, err := sqlite.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer repo.Close()
	pub := pin
	art, err := domain.NewArticle(domain.PublishInput{
		Title:       "Go 1.26 ships",
		Body:        "body text",
		Author:      "ada",
		Section:     "tech",
		Topics:      []string{"go"},
		PublishedAt: &pub,
	}, pin)
	if err != nil {
		t.Fatalf("NewArticle: %v", err)
	}
	if _, err := repo.Upsert(context.Background(), art); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Generate into ./public so the manifest lands at ./public/.generated/purge.json.
	genRes, err := generate.Generate(context.Background(), repo, generate.Options{
		OutDir:  "./public",
		BaseURL: "https://site.example",
		Now:     pin,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(genRes.Purge) == 0 {
		t.Fatalf("generator produced an empty purge list; nothing to wire-test")
	}

	t.Setenv("CENSURADO_PURGE_FILE", "") // force the built-in default path
	var stdout, stderr bytes.Buffer
	code := run([]string{"-provider", "dryrun"}, &stdout, &stderr) // no -file: use default
	if code != 0 {
		t.Fatalf("code = %d, want 0 reading the generator's manifest via the default path (%s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, fmt.Sprintf("succeeded=%d", len(genRes.Purge))) {
		t.Errorf("summary missing succeeded=%d (purger did not read the generated manifest): %s", len(genRes.Purge), out)
	}
	for _, u := range genRes.Purge {
		if !strings.Contains(out, u) {
			t.Errorf("dry-run output missing generated URL %q: %s", u, out)
		}
	}
}

func TestCmd_HTTPBatchSuccess(t *testing.T) {
	const token = "cli-secret-token-value"
	var (
		mu      sync.Mutex
		gotAuth string
		count   int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		gotAuth = r.Header.Get("X-Token")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	file := writeManifest(t, cliManifest)
	t.Setenv("CENSURADO_PURGE_TOKEN", token)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-file", file,
		"-provider", "http",
		"-endpoint", srv.URL,
		"-mode", "batch",
		"-base-url", "https://site.example",
		"-auth-header", "X-Token",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (%s)", code, stderr.String())
	}
	if count != 1 {
		t.Errorf("server got %d requests, want 1", count)
	}
	if gotAuth != token {
		t.Errorf("auth header = %q, want token sent", gotAuth)
	}
	if !strings.Contains(stdout.String(), "succeeded=2") {
		t.Errorf("want succeeded=2: %s", stdout.String())
	}
	// The token must never appear in any tool output.
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Errorf("auth token leaked into output")
	}
}

func TestCmd_HTTPFailureExitsNonzero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	file := writeManifest(t, cliManifest)
	t.Setenv("CENSURADO_PURGE_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-file", file,
		"-provider", "http",
		"-endpoint", srv.URL,
		"-mode", "batch",
		"-url-form", "relative",
		"-timeout", "2s",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1 on purge failure (%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "failed=") {
		t.Errorf("summary should report failures: %s", stdout.String())
	}
}

func TestCmd_MissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-file", filepath.Join(t.TempDir(), "nope.json")}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("code = %d, want 1 for missing manifest", code)
	}
}

func TestCmd_MalformedManifest(t *testing.T) {
	file := writeManifest(t, `{"version":1,"urls":[`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-file", file}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("code = %d, want 1 for malformed manifest", code)
	}
}

func TestCmd_UnknownProvider(t *testing.T) {
	file := writeManifest(t, cliManifest)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-file", file, "-provider", "cloudfront"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2 for unknown provider", code)
	}
}

func TestCmd_HTTPAuthHeaderWithoutTokenIsConfigError(t *testing.T) {
	file := writeManifest(t, cliManifest)
	t.Setenv("CENSURADO_PURGE_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-file", file,
		"-provider", "http",
		"-endpoint", "https://cdn.example",
		"-base-url", "https://site.example",
		"-auth-header", "Authorization",
	}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2 when auth-header set but token empty", code)
	}
}
