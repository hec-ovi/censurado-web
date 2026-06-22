// Command censurado-publish serves the authenticated publish API: the only write
// path into the system (POST /articles). It binds to localhost by default because
// the publish API is meant to sit off the public internet, reachable only by the
// agents that hold a key (over a private network or an SSH tunnel).
//
// Keys live in a JSON file (-keys / CENSURADO_PUBLISH_KEYS_FILE), an array of
// entries that carry only the SHA-256 of each secret, never the secret itself:
//
//	[ {"prefix":"ak_ada","hash":"<hex sha256 of the secret>","author":"ada","scopes":["articles:write"]} ]
//
// Mint a key with -gen-key -author NAME: it prints the agent TOKEN once (it cannot
// be recovered) and the ready-to-paste keys-file entry (no secret in it). Hand the
// TOKEN to the agent as CENSURADO_PUBLISH_TOKEN and add the entry to the keys file.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hec-ovi/censurado-web/internal/publish"
	"github.com/hec-ovi/censurado-web/internal/store/sqlite"
)

// Exit codes are distinct per failure class so a supervisor can tell a usage
// mistake from a config error, a database fault, or a serve failure.
const (
	exitOK     = 0
	exitUsage  = 2 // flag parse error or bad usage (missing -author for -gen-key)
	exitConfig = 3 // missing/invalid keys file or limiter settings
	exitDB     = 4 // store open failure
	exitServe  = 5 // listen/serve/shutdown failure
	exitGenErr = 1 // entropy failure while minting a key
)

func main() { os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr)) }

// stringSlice collects a repeatable flag (e.g. -scope a -scope b) into a list.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// cliFlags holds the parsed flag pointers so run and the tests share one wiring.
type cliFlags struct {
	addr   *string
	db     *string
	keys   *string
	rate   *float64
	burst  *int
	genKey *bool
	author *string
	scopes *stringSlice
}

// newFlagSet builds the flag set with env-derived defaults. It is factored out so a
// test can parse args and inspect the resolved config without binding a socket.
func newFlagSet(getenv func(string) string, stderr io.Writer) (*flag.FlagSet, *cliFlags) {
	fs := flag.NewFlagSet("censurado-publish", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var scopes stringSlice
	f := &cliFlags{
		addr:   fs.String("addr", envOr(getenv, "CENSURADO_PUBLISH_ADDR", "127.0.0.1:8080"), "listen address (or CENSURADO_PUBLISH_ADDR); localhost by default"),
		db:     fs.String("db", envOr(getenv, "CENSURADO_DB", "./censurado.db"), "sqlite database path (or CENSURADO_DB)"),
		keys:   fs.String("keys", envOr(getenv, "CENSURADO_PUBLISH_KEYS_FILE", ""), "path to the JSON keys file (or CENSURADO_PUBLISH_KEYS_FILE); required to serve"),
		rate:   fs.Float64("rate", envFloat(getenv, "CENSURADO_PUBLISH_RATE", 5), "per-key request rate, tokens/sec (or CENSURADO_PUBLISH_RATE)"),
		burst:  fs.Int("burst", envInt(getenv, "CENSURADO_PUBLISH_BURST", 10), "per-key burst size (or CENSURADO_PUBLISH_BURST)"),
		genKey: fs.Bool("gen-key", false, "mint a fresh key, print the token + keys-file entry, and exit without serving"),
		author: fs.String("author", "", "author the minted key publishes as (required with -gen-key)"),
	}
	fs.Var(&scopes, "scope", "scope to grant the minted key; repeatable (default articles:write)")
	f.scopes = &scopes

	fs.Usage = func() {
		fmt.Fprintln(stderr, "censurado-publish: the authenticated publish API server (POST /articles).")
		fmt.Fprintln(stderr, "\nUsage:")
		fmt.Fprintln(stderr, "  censurado-publish [flags]                  serve the API")
		fmt.Fprintln(stderr, "  censurado-publish -gen-key -author NAME    mint a key and exit")
		fmt.Fprintln(stderr, "\nThe keys file (-keys / CENSURADO_PUBLISH_KEYS_FILE) is a JSON array:")
		fmt.Fprintln(stderr, `  [ {"prefix":"ak_ada","hash":"<hex sha256 of the secret>","author":"ada","scopes":["articles:write"]} ]`)
		fmt.Fprintln(stderr, "It carries only secret hashes, never secrets. Mint entries with -gen-key.")
		fmt.Fprintln(stderr, "\nFlags:")
		fs.PrintDefaults()
	}

	return fs, f
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs, f := newFlagSet(getenv, stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	// -gen-key is mutually exclusive with serving: mint and exit.
	if *f.genKey {
		scopes := append([]string(nil), *f.scopes...)
		if len(scopes) == 0 {
			scopes = []string{publish.ScopeWrite}
		}
		if strings.TrimSpace(*f.author) == "" {
			fmt.Fprintln(stderr, "gen-key: -author is required")
			return exitUsage
		}
		if err := genKey(stdout, *f.author, scopes); err != nil {
			fmt.Fprintln(stderr, "gen-key:", err)
			return exitGenErr
		}
		return exitOK
	}

	if *f.rate <= 0 {
		fmt.Fprintln(stderr, "config: -rate must be > 0")
		return exitConfig
	}
	if *f.burst < 1 {
		fmt.Fprintln(stderr, "config: -burst must be >= 1")
		return exitConfig
	}

	entries, err := loadKeys(*f.keys)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return exitConfig
	}
	auth := buildAuth(entries)

	repo, err := sqlite.Open(*f.db)
	if err != nil {
		fmt.Fprintf(stderr, "open db: %v\n", err)
		return exitDB
	}
	defer repo.Close()

	// The same *sqlite.Store is both the article Repository and the SubmissionLog.
	h := publish.NewHandler(repo, repo, auth, time.Now)
	limiter := publish.NewRateLimiter(*f.rate, *f.burst, time.Now)
	handler := publish.NewServerHandler(h, limiter)

	srv := newServer(*f.addr, handler)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Log the bind address and key count only, never tokens or hashes, then serve
	// until a signal or a listener failure.
	listen := func() error {
		fmt.Fprintf(stdout, "censurado-publish listening on %s (%d key(s) loaded)\n", *f.addr, len(entries))
		return srv.ListenAndServe()
	}
	return serve(ctx, stop, srv, listen, shutdownGrace, stderr)
}

// shutdownGrace bounds how long Shutdown waits for in-flight requests to drain
// before the process exits.
const shutdownGrace = 10 * time.Second

// newServer constructs the publish HTTP server with bounded timeouts. ReadHeaderTimeout
// bounds the header phase; ReadTimeout additionally bounds the BODY phase, which
// ReadHeaderTimeout does NOT, closing the body-trickle slowloris that a header-only
// guard leaves open; WriteTimeout bounds the response phase; IdleTimeout bounds idle
// keep-alive connections. The read budget is generous for the 8 MiB body cap even on a
// slow link (30s tolerates well under 1 KiB/s before a trickle is cut off).
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// serve runs srv until ctx is cancelled (a SIGINT/SIGTERM signal) or the listener
// fails, then shuts down gracefully within shutdownTimeout so in-flight publishes
// drain instead of being dropped. listen is the serving call (srv.ListenAndServe in
// production; a test injects an ephemeral listener so the lifecycle is exercised
// without binding a fixed socket). It returns the process exit code: exitOK on a clean
// shutdown or a benign ErrServerClosed, exitServe on a Shutdown error or a listener
// failure. stop restores default signal handling so a second signal during the drain
// window terminates forcefully.
func serve(ctx context.Context, stop func(), srv *http.Server, listen func() error, shutdownTimeout time.Duration, stderr io.Writer) int {
	errCh := make(chan error, 1)
	go func() { errCh <- listen() }()

	select {
	case <-ctx.Done():
		stop()
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			fmt.Fprintln(stderr, "shutdown:", err)
			return exitServe
		}
		return exitOK
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(stderr, "serve:", err)
			return exitServe
		}
		return exitOK
	}
}

// keyEntry is one record in the keys file. It never holds a secret, only its hash.
type keyEntry struct {
	Prefix string   `json:"prefix"`
	Hash   string   `json:"hash"`
	Author string   `json:"author"`
	Scopes []string `json:"scopes"`
}

// loadKeys reads and validates the keys file. A missing, empty, or malformed file
// is a fatal config error: the server has no usable credentials without it.
func loadKeys(path string) ([]keyEntry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("no keys file configured: set -keys or CENSURADO_PUBLISH_KEYS_FILE")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read keys file: %w", err)
	}

	var entries []keyEntry
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse keys file %s: %w", path, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("keys file %s contains no keys", path)
	}

	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if strings.TrimSpace(e.Prefix) == "" {
			return nil, fmt.Errorf("keys[%d]: empty prefix", i)
		}
		if strings.TrimSpace(e.Hash) == "" {
			return nil, fmt.Errorf("keys[%d] (%s): empty hash", i, e.Prefix)
		}
		if strings.TrimSpace(e.Author) == "" {
			return nil, fmt.Errorf("keys[%d] (%s): empty author", i, e.Prefix)
		}
		if len(e.Scopes) == 0 {
			return nil, fmt.Errorf("keys[%d] (%s): needs at least one scope", i, e.Prefix)
		}
		for _, s := range e.Scopes {
			if strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("keys[%d] (%s): empty scope", i, e.Prefix)
			}
		}
		if seen[e.Prefix] {
			return nil, fmt.Errorf("keys[%d]: duplicate prefix %q", i, e.Prefix)
		}
		seen[e.Prefix] = true
	}
	return entries, nil
}

// buildAuth registers every validated entry with a StaticKeyAuth.
func buildAuth(entries []keyEntry) *publish.StaticKeyAuth {
	auth := publish.NewStaticKeyAuth()
	for _, e := range entries {
		auth.Add(e.Prefix, e.Hash, e.Author, e.Scopes...)
	}
	return auth
}

// genKey mints a fresh secret, derives a prefix, and prints the agent token once
// plus the keys-file entry (which carries the hash, never the secret).
func genKey(stdout io.Writer, author string, scopes []string) error {
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return err
	}
	// RawURLEncoding of 32 bytes is a 43-char, URL-safe secret containing no ".",
	// so it never collides with the "<prefix>.<secret>" token separator.
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)

	prefixBytes := make([]byte, 4)
	if _, err := rand.Read(prefixBytes); err != nil {
		return err
	}
	prefix := "ak_" + hex.EncodeToString(prefixBytes)

	entry := keyEntry{Prefix: prefix, Hash: publish.HashSecret(secret), Author: author, Scopes: scopes}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "# New publish key for author %q. The TOKEN below is shown ONLY now and\n", author)
	fmt.Fprintln(stdout, "# cannot be recovered; give it to the agent as CENSURADO_PUBLISH_TOKEN.")
	fmt.Fprintln(stdout, "# Add the JSON entry to your keys file (it carries no secret, only the hash).")
	fmt.Fprintf(stdout, "TOKEN=%s.%s\n", prefix, secret)
	fmt.Fprintf(stdout, "%s\n", encoded)
	return nil
}

func envOr(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
}

func envFloat(getenv func(string) string, key string, def float64) float64 {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(getenv func(string) string, key string, def int) int {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
