// Command censurado-purge reads a generate batch's purge manifest
// (<out>/.generated/purge.json, where the generator writes it) and invalidates
// exactly those URLs at the CDN, never more and never a wildcard.
// Article permalinks are content-hash URLs with a long TTL and never appear in the
// manifest, so they are never purged.
//
// The default provider is dryrun: it makes no network calls and just lists the URLs
// it would purge, so the tool is safe to run by accident. Choose -provider http to
// actually invalidate. The auth secret is read only from the CENSURADO_PURGE_TOKEN
// environment variable (never a flag, so it stays out of shell history and ps) and
// is never printed.
//
// Exit codes: 0 on full success (including a dry run and an empty no-op manifest),
// 1 when any purge failed or the manifest could not be read, 2 on usage/config
// error. A deploy step can gate on the exit code.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hec-ovi/censurado-web/internal/purge"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("censurado-purge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", envOr("CENSURADO_PURGE_FILE", "./public/.generated/purge.json"), "purge manifest path (or CENSURADO_PURGE_FILE)")
	provider := fs.String("provider", "dryrun", "purge provider: dryrun|http (default dryrun, makes no network calls)")
	endpoint := fs.String("endpoint", os.Getenv("CENSURADO_PURGE_ENDPOINT"), "CDN purge endpoint (or CENSURADO_PURGE_ENDPOINT)")
	method := fs.String("method", "", "HTTP method (default POST; e.g. PURGE for per_url)")
	mode := fs.String("mode", "batch", "http request shape: batch|per_url")
	field := fs.String("field", "files", "batch JSON field name carrying the URL list")
	baseURL := fs.String("base-url", os.Getenv("CENSURADO_BASE_URL"), "site origin for full-URL join (or CENSURADO_BASE_URL)")
	urlForm := fs.String("url-form", "full", "URL form sent to the CDN: full|relative")
	authHeader := fs.String("auth-header", "", "auth header name; value comes from CENSURADO_PURGE_TOKEN")
	batchSize := fs.Int("batch-size", 0, "max URLs per request, 0 = no chunk cap (chunks batch mode)")
	timeout := fs.Duration("timeout", 10*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	f, err := os.Open(*file)
	if err != nil {
		fmt.Fprintf(stderr, "open manifest: %v\n", err)
		return 1
	}
	defer f.Close()
	m, err := purge.ParseManifest(f)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	var p purge.Purger
	switch *provider {
	case "dryrun":
		fmt.Fprintf(stdout, "dry-run: would purge %d url(s) from %s\n", len(m.URLs), *file)
		p = purge.DryRunPurger{Out: stdout}
	case "http":
		token := os.Getenv("CENSURADO_PURGE_TOKEN")
		if *authHeader != "" && token == "" {
			fmt.Fprintln(stderr, "auth-header is set but CENSURADO_PURGE_TOKEN is empty")
			return 2
		}
		hp, err := purge.NewHTTPPurger(purge.HTTPConfig{
			Endpoint:   *endpoint,
			Method:     *method,
			Mode:       purge.Mode(*mode),
			Field:      *field,
			BaseURL:    *baseURL,
			URLForm:    purge.URLForm(*urlForm),
			AuthHeader: *authHeader,
			AuthValue:  token,
			BatchSize:  *batchSize,
			Timeout:    *timeout,
		})
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
		p = hp
	default:
		fmt.Fprintf(stderr, "unknown -provider %q (want dryrun|http)\n", *provider)
		return 2
	}

	res, perr := p.Purge(context.Background(), m.URLs)
	fmt.Fprintf(stdout, "purge: provider=%s requested=%d succeeded=%d failed=%d\n",
		*provider, res.Requested, res.Succeeded, res.Failed())
	if perr != nil {
		for _, e := range res.Errors {
			fmt.Fprintf(stderr, "  failed %s: %s\n", e.URL, e.Err)
		}
		return 1
	}
	return 0
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
