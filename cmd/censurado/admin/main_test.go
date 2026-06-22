package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hec-ovi/censurado-web/internal/domain"
	"github.com/hec-ovi/censurado-web/internal/store/sqlite"
)

// envFromMap returns a getenv-shaped lookup over a fixed map, so the config and
// credential logic is testable without touching the process environment.
func envFromMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// parseEnvLines extracts KEY=VALUE pairs from gen-credentials output.
func parseEnvLines(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func TestGenCredentials(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-gen-credentials"}, envFromMap(nil), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, exitOK, stderr.String())
	}
	env := parseEnvLines(stdout.String())

	token := env["CENSURADO_ADMIN_TOKEN"]
	hash := env["CENSURADO_ADMIN_TOKEN_HASH"]
	key := env["CENSURADO_ADMIN_SESSION_KEY"]

	if len(token) < 32 {
		t.Errorf("token %q is %d chars, want >=32", token, len(token))
	}
	// The printed hash must be the sha256 of the printed cleartext token.
	sum := sha256.Sum256([]byte(token))
	wantHash := hex.EncodeToString(sum[:])
	if hash != wantHash {
		t.Errorf("printed hash = %q, want sha256(token) = %q", hash, wantHash)
	}
	// The session key must decode to at least 32 bytes.
	decoded, err := decodeKey(key)
	if err != nil {
		t.Fatalf("session key %q does not decode: %v", key, err)
	}
	if len(decoded) < 32 {
		t.Errorf("session key decodes to %d bytes, want >=32", len(decoded))
	}

	// The minted credentials must satisfy buildConfig as-is (token hash + key).
	cfg, err := buildConfig(envFromMap(map[string]string{
		"CENSURADO_ADMIN_TOKEN_HASH":  hash,
		"CENSURADO_ADMIN_SESSION_KEY": key,
	}))
	if err != nil {
		t.Fatalf("buildConfig with generated creds: %v", err)
	}
	if cfg.TokenHash != hash || len(cfg.SessionKey) < 32 {
		t.Errorf("buildConfig result mismatch: %+v", cfg)
	}
	if !cfg.SecureCookies {
		t.Errorf("SecureCookies default = false, want true")
	}
}

func TestBuildConfig(t *testing.T) {
	validKey := strings.Repeat("ab", 32) // 64 hex chars -> 32 bytes

	t.Run("missing token and hash -> error", func(t *testing.T) {
		_, err := buildConfig(envFromMap(map[string]string{
			"CENSURADO_ADMIN_SESSION_KEY": validKey,
		}))
		if err == nil {
			t.Fatal("want error for missing token/hash, got nil")
		}
	})

	t.Run("missing session key -> error", func(t *testing.T) {
		_, err := buildConfig(envFromMap(map[string]string{
			"CENSURADO_ADMIN_TOKEN_HASH": strings.Repeat("0", 64),
		}))
		if err == nil {
			t.Fatal("want error for missing session key, got nil")
		}
	})

	t.Run("short session key -> error", func(t *testing.T) {
		_, err := buildConfig(envFromMap(map[string]string{
			"CENSURADO_ADMIN_TOKEN_HASH":  strings.Repeat("0", 64),
			"CENSURADO_ADMIN_SESSION_KEY": "abcd", // 2 bytes
		}))
		if err == nil {
			t.Fatal("want error for short session key, got nil")
		}
	})

	t.Run("CENSURADO_ADMIN_TOKEN is hashed at boot", func(t *testing.T) {
		cfg, err := buildConfig(envFromMap(map[string]string{
			"CENSURADO_ADMIN_TOKEN":       "operator-token-fixture",
			"CENSURADO_ADMIN_SESSION_KEY": validKey,
		}))
		if err != nil {
			t.Fatalf("buildConfig: %v", err)
		}
		if cfg.TokenHash != hashTokenHex("operator-token-fixture") {
			t.Errorf("TokenHash = %q, want hash of the cleartext token", cfg.TokenHash)
		}
	})

	t.Run("explicit hash + secure cookies off", func(t *testing.T) {
		cfg, err := buildConfig(envFromMap(map[string]string{
			"CENSURADO_ADMIN_TOKEN_HASH":     strings.Repeat("a", 64),
			"CENSURADO_ADMIN_SESSION_KEY":    validKey,
			"CENSURADO_ADMIN_SECURE_COOKIES": "false",
		}))
		if err != nil {
			t.Fatalf("buildConfig: %v", err)
		}
		if cfg.SecureCookies {
			t.Errorf("SecureCookies = true, want false (env override)")
		}
	})

	t.Run("base64 session key is accepted", func(t *testing.T) {
		// 36 bytes (non-hex content) base64-encoded, to exercise the base64 path.
		raw := []byte("admin-session-key-base64-path-0123456")
		b64 := base64.StdEncoding.EncodeToString(raw)
		cfg, err := buildConfig(envFromMap(map[string]string{
			"CENSURADO_ADMIN_TOKEN_HASH":  strings.Repeat("0", 64),
			"CENSURADO_ADMIN_SESSION_KEY": b64,
		}))
		if err != nil {
			t.Fatalf("buildConfig with base64 key: %v", err)
		}
		if len(cfg.SessionKey) < 32 {
			t.Errorf("decoded key = %d bytes, want >=32", len(cfg.SessionKey))
		}
	})
}

// TestBuildRegenerate_Disabled proves the action is off (nil closure) unless both
// an output dir and a base URL are supplied, so adminweb stays in its disabled
// state and the POST returns a friendly "not configured" response, never a panic.
// A nil repo is safe here because buildRegenerate returns before touching it.
func TestBuildRegenerate_Disabled(t *testing.T) {
	cases := []struct {
		name, out, base string
	}{
		{"empty out", "", "https://news.example"},
		{"empty base", t.TempDir(), ""},
		{"blank out", "   ", "https://news.example"},
		{"blank base", t.TempDir(), "   "},
		{"both blank", "  ", "  "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if buildRegenerate(nil, c.out, c.base, 20) != nil {
				t.Errorf("buildRegenerate(out=%q, base=%q) = non-nil, want nil (disabled)", c.out, c.base)
			}
		})
	}
}

// seedAdminArticle inserts one published article into a fresh sqlite store so the
// generator has real content to materialize.
func seedAdminArticle(t *testing.T, repo *sqlite.Store) {
	t.Helper()
	pub := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	a, err := domain.NewArticle(domain.PublishInput{
		Title:       "Regenerate wiring fixture",
		Body:        "A seeded article so the generator produces real artifacts.",
		Author:      "ada",
		Section:     "tech",
		Topics:      []string{"censorship"},
		PublishedAt: &pub,
	}, pub)
	if err != nil {
		t.Fatalf("NewArticle: %v", err)
	}
	if _, err := repo.Upsert(context.Background(), a); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}

// TestBuildRegenerate_WiresGenerate smoke-covers the binary's generate.Generate ->
// RegenResult adapter end to end: it opens a real sqlite store, seeds an article,
// and asserts the closure threads OutDir/BaseURL/PageSize through and maps the
// Result fields (including the whole, untruncated Purge list) back.
func TestBuildRegenerate_WiresGenerate(t *testing.T) {
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	seedAdminArticle(t, repo)

	outDir := t.TempDir()
	const base = "https://news.example"
	const pageSize = 7

	// Surrounding whitespace on the out dir proves buildRegenerate trims its inputs.
	regen := buildRegenerate(repo, "  "+outDir+"  ", base, pageSize)
	if regen == nil {
		t.Fatal("buildRegenerate returned nil with out dir + base set, want enabled closure")
	}

	res, err := regen(context.Background())
	if err != nil {
		t.Fatalf("regenerate closure: %v", err)
	}

	// Result -> RegenResult mapping: a first run writes artifacts and lists every
	// one to purge, with at least one scope and nothing unchanged/deleted yet.
	if res.Written == 0 {
		t.Errorf("Written = 0, want >0 on a first run")
	}
	if len(res.Purge) == 0 {
		t.Errorf("Purge is empty, want the first-run invalidation list")
	}
	if res.ScopeCount < 1 {
		t.Errorf("ScopeCount = %d, want >=1", res.ScopeCount)
	}
	if res.Unchanged != 0 || res.Deleted != 0 {
		t.Errorf("first run Unchanged=%d Deleted=%d, want 0/0", res.Unchanged, res.Deleted)
	}

	// OutDir + BaseURL plumbing: robots.txt landed under the trimmed dir and points
	// crawlers at the configured base, so both options reached generate.Options.
	robots, err := os.ReadFile(filepath.Join(outDir, "robots.txt"))
	if err != nil {
		t.Fatalf("read robots.txt under out dir: %v", err)
	}
	if !strings.Contains(string(robots), base+"/sitemap.xml") {
		t.Errorf("robots.txt = %q, want it to carry base URL %q", robots, base)
	}

	// A second run over the unchanged store is idempotent, and the mapping reflects
	// it: nothing rewritten or purged, everything counted as unchanged.
	res2, err := regen(context.Background())
	if err != nil {
		t.Fatalf("second regenerate: %v", err)
	}
	if res2.Written != 0 || res2.Deleted != 0 || len(res2.Purge) != 0 {
		t.Errorf("second run not idempotent: written=%d deleted=%d purge=%d", res2.Written, res2.Deleted, len(res2.Purge))
	}
	if res2.Unchanged == 0 {
		t.Errorf("second run Unchanged = 0, want the full artifact count")
	}
}

// TestBuildRegenerate_PropagatesError proves a generate.Generate failure surfaces
// as (zero RegenResult, err) rather than a partial result or panic. PageSize is
// threaded straight into generate.Options, so a negative value fails Validate.
func TestBuildRegenerate_PropagatesError(t *testing.T) {
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	regen := buildRegenerate(repo, t.TempDir(), "https://news.example", -1)
	if regen == nil {
		t.Fatal("buildRegenerate returned nil with out dir + base set, want enabled closure")
	}
	res, err := regen(context.Background())
	if err == nil {
		t.Fatal("want an error from Generate for PageSize=-1, got nil")
	}
	if res.Written != 0 || res.Unchanged != 0 || res.Deleted != 0 || res.ScopeCount != 0 || len(res.Purge) != 0 {
		t.Errorf("on error, want a zero RegenResult, got %+v", res)
	}
}

// TestRun_MissingEnvNoServer proves a config error returns a distinct nonzero
// exit BEFORE any socket is bound (run reaches buildConfig and returns).
func TestRun_MissingEnvNoServer(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, envFromMap(nil), &stdout, &stderr)
	if code != exitConfig {
		t.Fatalf("exit = %d, want %d (config error)", code, exitConfig)
	}
	if !strings.Contains(stderr.String(), "config:") {
		t.Errorf("stderr = %q, want a clear config error", stderr.String())
	}
}
