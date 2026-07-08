// Command censurado-generate materializes the complete static site from the
// article store. In one-shot mode (the default) it opens the sqlite database as a
// READER (the backend's publish service is the only writer), materializes the
// static site into the out dir, prints a one-line summary, and exits.
//
// With -watch it stays resident and re-runs the SAME idempotent generation on a
// fixed interval, then invalidates exactly the URLs that changed (the generator's
// purge.json) at the CDN. The backend writes the database; this generator owns
// turning the database into the static site and purging the CDN. Wipe OutDir when
// changing PageSize, BaseURL, or the
// shard caps for a fully clean tree: the fingerprint forces a rewrite and purges
// orphaned URLs, but stray files untracked by prior state are not removed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hec-ovi/censurado-web-backend/store"
	"github.com/hec-ovi/censurado-web-backend/store/sqlite"
	"github.com/hec-ovi/censurado-web/internal/generate"
	"github.com/hec-ovi/censurado-web/internal/purge"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("censurado-generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db := fs.String("db", os.Getenv("CENSURADO_DB"), "sqlite database path (or CENSURADO_DB)")
	out := fs.String("out", "./public", "output artifact root")
	baseURL := fs.String("base-url", os.Getenv("CENSURADO_BASE_URL"), "absolute site origin (or CENSURADO_BASE_URL)")
	pageSize := fs.Int("page-size", 20, "articles per full page (P); locked before first publish")
	siteName := fs.String("site-name", "El Censurado Web", "site/feed title")
	lang := fs.String("lang", envOr("CENSURADO_LANG", "es"), "render language: the frontend_text rows and <html lang> (or CENSURADO_LANG); the live Spanish site uses es")
	quiet := fs.Bool("quiet", false, "suppress the per-run summary on stdout")
	watch := fs.Bool("watch", false, "stay resident and regenerate + purge on a fixed interval, so a publish to the database refreshes the static site without an external trigger")
	watchInterval := fs.Duration("watch-interval", envDuration("CENSURADO_GENERATE_WATCH_INTERVAL", 2*time.Second), "poll interval between regeneration passes in -watch mode (or CENSURADO_GENERATE_WATCH_INTERVAL)")
	purgeEndpoint := fs.String("purge-endpoint", os.Getenv("CENSURADO_PURGE_ENDPOINT"), "CDN purge endpoint used in -watch mode (or CENSURADO_PURGE_ENDPOINT); empty = dry-run purge, no network")
	purgeAuthHeader := fs.String("purge-auth-header", envOr("CENSURADO_PURGE_AUTH_HEADER", "Authorization"), "auth header name for the purge; its value is read only from CENSURADO_PURGE_TOKEN")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *db == "" {
		fmt.Fprintln(stderr, "missing -db (or CENSURADO_DB)")
		return 2
	}
	if *baseURL == "" {
		fmt.Fprintln(stderr, "missing -base-url (or CENSURADO_BASE_URL)")
		return 2
	}

	repo, err := sqlite.Open(*db)
	if err != nil {
		fmt.Fprintf(stderr, "open db: %v\n", err)
		return 1
	}
	defer repo.Close()

	opts := generate.Options{
		OutDir:   *out,
		BaseURL:  *baseURL,
		PageSize: *pageSize,
		SiteName: *siteName,
		Lang:     *lang,
		Now:      time.Now().UTC(),
	}

	if *watch {
		purger, perr := buildPurger(*purgeEndpoint, *purgeAuthHeader, *baseURL)
		if perr != nil {
			fmt.Fprintf(stderr, "purge config: %v\n", perr)
			return 2
		}
		return runWatch(repo, opts, purger, *watchInterval, stdout, stderr)
	}

	res, err := generate.Generate(context.Background(), repo, opts)
	if err != nil {
		fmt.Fprintf(stderr, "generate: %v\n", err)
		return 1
	}
	if !*quiet {
		fmt.Fprintf(stdout, "written=%d unchanged=%d deleted=%d purged=%d scopes=%d tier_a_urls=%d\n",
			res.Written, res.Unchanged, res.Deleted, len(res.Purge), res.ScopeCount, res.TierAURLs)
	}
	return 0
}

// runWatch regenerates on a fixed interval until interrupted. Each pass is the same
// idempotent Generate (it writes nothing when the corpus is unchanged), so polling
// is cheap on a small corpus; only a pass that actually wrote or deleted artifacts
// triggers a CDN purge of exactly the changed URLs. It returns 0 on a clean
// signal-driven exit.
func runWatch(repo store.Repository, opts generate.Options, purger purge.Purger, interval time.Duration, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if interval <= 0 {
		interval = 2 * time.Second
	}
	fmt.Fprintf(stdout, "watching: out=%s base-url=%s interval=%s\n", opts.OutDir, opts.BaseURL, interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := regenOnce(ctx, repo, opts, purger, stdout); err != nil {
			fmt.Fprintf(stderr, "regenerate: %v\n", err)
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "watch stopped")
			return 0
		case <-ticker.C:
		}
	}
}

// regenOnce runs one regenerate-plus-purge pass: regenerate the static site
// incrementally, then invalidate exactly the URLs the generator changed (its
// purge.json). A pass that changed nothing skips the purge entirely.
func regenOnce(ctx context.Context, repo store.Repository, opts generate.Options, purger purge.Purger, stdout io.Writer) error {
	opts.Now = time.Now().UTC()
	res, err := generate.Generate(ctx, repo, opts)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	if res.Written == 0 && res.Deleted == 0 {
		return nil
	}
	manifest := filepath.Join(opts.OutDir, ".generated", "purge.json")
	f, err := os.Open(manifest)
	if err != nil {
		return fmt.Errorf("open purge manifest: %w", err)
	}
	defer f.Close()
	m, err := purge.ParseManifest(f)
	if err != nil {
		return fmt.Errorf("parse purge manifest: %w", err)
	}
	if len(m.URLs) == 0 {
		fmt.Fprintf(stdout, "regenerated: %d written, %d deleted, nothing to purge\n", res.Written, res.Deleted)
		return nil
	}
	pr, err := purger.Purge(ctx, m.URLs)
	if err != nil {
		return fmt.Errorf("purge: %w", err)
	}
	if pr.Failed() > 0 {
		return fmt.Errorf("purge: %d of %d urls failed", pr.Failed(), pr.Requested)
	}
	fmt.Fprintf(stdout, "regenerated: %d written, %d deleted, %d purged\n", res.Written, res.Deleted, pr.Succeeded)
	return nil
}

// buildPurger returns the CDN purge adapter for -watch mode. With no endpoint it is
// a dry run (no network calls), the safe default; with an endpoint it is the
// vendor-neutral HTTP adapter, its secret read only from CENSURADO_PURGE_TOKEN.
func buildPurger(endpoint, authHeader, baseURL string) (purge.Purger, error) {
	if endpoint == "" {
		return purge.DryRunPurger{}, nil
	}
	return purge.NewHTTPPurger(purge.HTTPConfig{
		Endpoint:   endpoint,
		BaseURL:    baseURL,
		AuthHeader: authHeader,
		AuthValue:  os.Getenv("CENSURADO_PURGE_TOKEN"),
	})
}

// envOr returns the environment value for key, or def when unset/blank.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envDuration parses a duration from the environment, or returns def.
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
