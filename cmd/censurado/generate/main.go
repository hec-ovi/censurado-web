// Command censurado-generate materializes the complete static site from the
// article store. It opens the sqlite store read-only, calls generate.Generate,
// and prints a one-line per-run summary. Wipe OutDir when changing PageSize,
// BaseURL, or the shard caps for a fully clean tree: the fingerprint forces a
// rewrite and purges orphaned URLs, but stray files untracked by prior state are
// not removed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hec-ovi/censurado-web/internal/generate"
	"github.com/hec-ovi/censurado-web/internal/store/sqlite"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("censurado-generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db := fs.String("db", os.Getenv("CENSURADO_DB"), "sqlite database path (or CENSURADO_DB)")
	out := fs.String("out", "./public", "output artifact root")
	baseURL := fs.String("base-url", os.Getenv("CENSURADO_BASE_URL"), "absolute site origin (or CENSURADO_BASE_URL)")
	pageSize := fs.Int("page-size", 20, "articles per full page (P); locked before first publish")
	siteName := fs.String("site-name", "Censurado", "site/feed title")
	quiet := fs.Bool("quiet", false, "suppress the per-run summary on stdout")
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

	res, err := generate.Generate(context.Background(), repo, generate.Options{
		OutDir:   *out,
		BaseURL:  *baseURL,
		PageSize: *pageSize,
		SiteName: *siteName,
		Now:      time.Now().UTC(),
	})
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
