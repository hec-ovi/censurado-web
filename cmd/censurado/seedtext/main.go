// Command censurado-seedtext populates the frontend_text catalog with the site's
// canonical UI strings: the English base row and the Spanish translation for every
// key the generator renders (the same catalog the generator falls back to when a
// row is absent). It is the one-time bootstrap that turns the compiled defaults
// into real, operator- and translator-editable database rows, and the English base
// the translate skill reads to add further languages.
//
// By default it only INSERTS rows that are missing, so re-running it fills gaps
// without clobbering values an operator later edited. With -force it overwrites
// every base row back to the shipped catalog.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hec-ovi/censurado-web-backend/store"
	"github.com/hec-ovi/censurado-web-backend/store/sqlite"
	"github.com/hec-ovi/censurado-web/internal/generate"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("censurado-seedtext", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db := fs.String("db", os.Getenv("CENSURADO_DB"), "sqlite database path (or CENSURADO_DB)")
	force := fs.Bool("force", false, "overwrite existing rows back to the shipped catalog (default: only insert missing rows)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *db == "" {
		fmt.Fprintln(stderr, "missing -db (or CENSURADO_DB)")
		return 2
	}

	repo, err := sqlite.Open(*db)
	if err != nil {
		fmt.Fprintf(stderr, "open db: %v\n", err)
		return 1
	}
	defer repo.Close()

	n, skipped, err := seedFrontendText(context.Background(), repo, *force)
	if err != nil {
		fmt.Fprintf(stderr, "seed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "seeded %d row(s), skipped %d existing (keys=%d, langs=en,es)\n",
		n, skipped, len(generate.FrontendTextSeed()))
	return 0
}

// seedFrontendText upserts the English base and Spanish rows for every catalog key.
// Without force it first reads the existing (key,lang) pairs and inserts only the
// missing ones, so operator edits survive a re-run. It returns the count written
// and the count skipped.
func seedFrontendText(ctx context.Context, repo store.TextStore, force bool) (written, skipped int, err error) {
	existing := map[[2]string]bool{}
	if !force {
		rows, lerr := repo.ListText(ctx, store.ScopeFrontend, "", true) // include tombstoned: never resurrect a deleted row implicitly
		if lerr != nil {
			return 0, 0, lerr
		}
		for _, r := range rows {
			existing[[2]string{r.Key, r.Lang}] = true
		}
	}
	for _, e := range generate.FrontendTextSeed() {
		for _, lv := range []struct{ lang, val string }{{"en", e.En}, {"es", e.Es}} {
			if !force && existing[[2]string{e.Key, lv.lang}] {
				skipped++
				continue
			}
			if _, uerr := repo.UpsertText(ctx, store.ScopeFrontend, store.TextEntry{
				Key: e.Key, Lang: lv.lang, Value: lv.val,
			}); uerr != nil {
				return written, skipped, fmt.Errorf("upsert %s/%s: %w", lv.lang, e.Key, uerr)
			}
			written++
		}
	}
	return written, skipped, nil
}
