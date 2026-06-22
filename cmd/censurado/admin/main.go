// Command censurado-admin serves the private operator console (internal/adminweb)
// over the sqlite store. It binds to localhost by default because the admin is
// meant to sit off the public internet behind an SSH tunnel or private network.
//
// Run once with -gen-credentials to mint an operator token and session key; the
// cleartext token is printed only that one time. Put the printed TOKEN_HASH and
// SESSION_KEY in the server's environment and hand the operator the TOKEN.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

	"github.com/hec-ovi/censurado-web/internal/adminweb"
	"github.com/hec-ovi/censurado-web/internal/generate"
	"github.com/hec-ovi/censurado-web/internal/store"
	"github.com/hec-ovi/censurado-web/internal/store/sqlite"
)

// Exit codes are distinct per failure class so an operator (or a supervisor) can
// tell a config mistake from a runtime fault.
const (
	exitOK         = 0
	exitUsage      = 2 // flag parse error
	exitConfig     = 3 // missing/invalid env credentials
	exitDB         = 4 // store open failure
	exitServe      = 5 // listen/serve/shutdown failure
	exitGenCredErr = 1 // entropy failure while generating credentials
)

func main() { os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr)) }

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("censurado-admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", envOr(getenv, "CENSURADO_ADMIN_ADDR", "127.0.0.1:8081"), "listen address (or CENSURADO_ADMIN_ADDR); localhost by default")
	db := fs.String("db", envOr(getenv, "CENSURADO_DB", "./censurado.db"), "sqlite database path (or CENSURADO_DB)")
	out := fs.String("out", envOr(getenv, "CENSURADO_ADMIN_OUT", ""), "static-site output dir for the regenerate action (or CENSURADO_ADMIN_OUT); empty disables regenerate")
	baseURL := fs.String("base-url", envOr(getenv, "CENSURADO_BASE_URL", ""), "absolute base URL for generated links (or CENSURADO_BASE_URL); required with -out to enable regenerate")
	pageSize := fs.Int("page-size", 20, "articles per generated page for the regenerate action (generator default)")
	genCreds := fs.Bool("gen-credentials", false, "generate a fresh operator token + session key, print them, and exit without serving")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	if *genCreds {
		if err := genCredentials(stdout); err != nil {
			fmt.Fprintln(stderr, "gen-credentials:", err)
			return exitGenCredErr
		}
		return exitOK
	}

	cfg, err := buildConfig(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return exitConfig
	}

	repo, err := sqlite.Open(*db)
	if err != nil {
		fmt.Fprintf(stderr, "open db: %v\n", err)
		return exitDB
	}
	defer repo.Close()
	cfg.Repo = repo
	// The sqlite store also implements store.SubmissionLog, so it backs the audit
	// view directly.
	cfg.Log = repo

	// The regenerate action is enabled only when both an output dir and a base URL
	// are supplied; buildRegenerate returns nil otherwise, leaving the route in its
	// disabled state. It keeps internal/generate out of adminweb: the binary imports
	// the generator and adapts its Result to adminweb.RegenResult.
	cfg.Regenerate = buildRegenerate(repo, *out, *baseURL, *pageSize)

	handler, err := adminweb.New(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "handler:", err)
		return exitConfig
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(stdout, "censurado-admin listening on %s\n", *addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		stop() // restore default signal handling so a second Ctrl-C is forceful
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

// buildConfig assembles the admin Config from the environment, without the Repo
// (the caller opens the store and sets it). It is the unit the tests exercise so
// the credential rules are verified without binding a socket.
func buildConfig(getenv func(string) string) (adminweb.Config, error) {
	var cfg adminweb.Config

	tokenHash := strings.TrimSpace(getenv("CENSURADO_ADMIN_TOKEN_HASH"))
	if tokenHash == "" {
		token := getenv("CENSURADO_ADMIN_TOKEN")
		if strings.TrimSpace(token) == "" {
			return cfg, errors.New("set CENSURADO_ADMIN_TOKEN_HASH (hex sha256 of the operator token) or CENSURADO_ADMIN_TOKEN")
		}
		tokenHash = hashTokenHex(token)
	}
	cfg.TokenHash = tokenHash

	keyRaw := strings.TrimSpace(getenv("CENSURADO_ADMIN_SESSION_KEY"))
	if keyRaw == "" {
		return cfg, errors.New("set CENSURADO_ADMIN_SESSION_KEY (hex or base64, at least 32 bytes)")
	}
	key, err := decodeKey(keyRaw)
	if err != nil {
		return cfg, fmt.Errorf("CENSURADO_ADMIN_SESSION_KEY: %w", err)
	}
	if len(key) < 32 {
		return cfg, fmt.Errorf("CENSURADO_ADMIN_SESSION_KEY decodes to %d bytes, need at least 32", len(key))
	}
	cfg.SessionKey = key

	cfg.SecureCookies = true
	if v := strings.TrimSpace(getenv("CENSURADO_ADMIN_SECURE_COOKIES")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return cfg, fmt.Errorf("CENSURADO_ADMIN_SECURE_COOKIES: %w", err)
		}
		cfg.SecureCookies = b
	}
	return cfg, nil
}

// buildRegenerate returns the closure the admin handler runs from
// POST /admin/regenerate, or nil when the action is disabled. It is the seam that
// keeps internal/generate out of adminweb: the binary imports the generator here
// and adapts generate.Result to adminweb.RegenResult (Purge passed through whole,
// never truncated). The action is enabled only when both an output dir and a base
// URL are supplied; either empty yields nil, so adminweb shows the disabled state
// and the POST returns a friendly "not configured" response instead of panicking.
func buildRegenerate(repo store.Repository, out, baseURL string, pageSize int) func(context.Context) (adminweb.RegenResult, error) {
	outDir, base := strings.TrimSpace(out), strings.TrimSpace(baseURL)
	if outDir == "" || base == "" {
		return nil
	}
	return func(ctx context.Context) (adminweb.RegenResult, error) {
		res, err := generate.Generate(ctx, repo, generate.Options{OutDir: outDir, BaseURL: base, PageSize: pageSize})
		if err != nil {
			return adminweb.RegenResult{}, err
		}
		return adminweb.RegenResult{
			Written:    res.Written,
			Unchanged:  res.Unchanged,
			Deleted:    res.Deleted,
			ScopeCount: res.ScopeCount,
			Purge:      res.Purge,
		}, nil
	}
}

// genCredentials mints a fresh operator token and session key, printing the token
// in cleartext exactly once along with its hash and the key for the server env.
func genCredentials(w io.Writer) error {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	// RawURLEncoding of 32 random bytes is a 43-char token (>=32 bytes), URL-safe.
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	hash := hashTokenHex(token)

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return err
	}
	sessionKey := hex.EncodeToString(keyBytes)

	fmt.Fprintln(w, "# Censurado admin credentials. The cleartext token below is shown ONLY now.")
	fmt.Fprintln(w, "# Give CENSURADO_ADMIN_TOKEN to the operator; put the HASH and SESSION_KEY in the server env.")
	fmt.Fprintln(w, "CENSURADO_ADMIN_TOKEN="+token)
	fmt.Fprintln(w, "CENSURADO_ADMIN_TOKEN_HASH="+hash)
	fmt.Fprintln(w, "CENSURADO_ADMIN_SESSION_KEY="+sessionKey)
	return nil
}

// hashTokenHex returns the hex SHA-256 of a token. It mirrors adminweb's internal
// hashToken; duplicated here so the command does not import an unexported helper.
func hashTokenHex(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// decodeKey accepts the session key as hex or any common base64 alphabet.
func decodeKey(s string) ([]byte, error) {
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not valid hex or base64")
}

func envOr(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
}
