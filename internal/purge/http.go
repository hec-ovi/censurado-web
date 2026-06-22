package purge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Mode selects the request shape an HTTPPurger sends.
type Mode string

const (
	// ModeBatch sends ONE request whose JSON body carries the URL list under a
	// configurable field (default "files", Cloudflare-style {"files":[...]}).
	ModeBatch Mode = "batch"
	// ModePerURL sends ONE request per URL (configurable method, e.g. PURGE
	// Fastly-style) targeting the joined endpoint+path or the absolute URL.
	ModePerURL Mode = "per_url"
)

// URLForm selects whether URLs are sent as absolute (BaseURL+path) or root-relative.
type URLForm string

const (
	// URLFull joins each path onto BaseURL to send absolute URLs. This is the
	// default because most CDNs purge by absolute URL.
	URLFull URLForm = "full"
	// URLRelative sends the root-relative paths from the manifest unchanged.
	URLRelative URLForm = "relative"
)

// HTTPConfig configures a vendor-neutral HTTP purge adapter. Everything is
// injectable (Client, Sleep, BackoffBase) so tests are deterministic. The auth
// value comes from the caller (env, never a flag) and is never logged.
type HTTPConfig struct {
	Endpoint   string  // CDN purge endpoint (batch) or join base for per_url relative form
	Method     string  // HTTP method; default POST
	Mode       Mode    // batch (default) or per_url
	Field      string  // batch JSON field for the URL list; default "files"
	BaseURL    string  // origin for full-URL join (required when URLForm is full)
	URLForm    URLForm // full (default) or relative
	AuthHeader string  // auth header name (e.g. Authorization); empty disables auth
	AuthValue  string  // auth header value (secret; never logged)
	BatchSize  int     // max URLs per batch request (<=0 means one request for all)

	MaxAttempts int                 // bounded retry cap for transient errors; default 3
	BackoffBase time.Duration       // base backoff between retries; default 200ms
	Timeout     time.Duration       // per-request timeout; default 10s
	Client      *http.Client        // injected client; default &http.Client{}
	Sleep       func(time.Duration) // injected sleeper for backoff; default time.Sleep
}

// HTTPPurger is the configured HTTP CDN adapter. Build it with NewHTTPPurger.
type HTTPPurger struct {
	endpoint   string
	method     string
	mode       Mode
	field      string
	baseURL    string
	fullURLs   bool
	authHeader string
	authValue  string
	batchSize  int

	maxAttempts int
	backoffBase time.Duration
	timeout     time.Duration
	client      *http.Client
	sleep       func(time.Duration)
}

// NewHTTPPurger validates the config, fills defaults, and returns a ready purger.
func NewHTTPPurger(cfg HTTPConfig) (*HTTPPurger, error) {
	p := &HTTPPurger{
		endpoint:    cfg.Endpoint,
		method:      cfg.Method,
		mode:        cfg.Mode,
		field:       cfg.Field,
		baseURL:     cfg.BaseURL,
		authHeader:  cfg.AuthHeader,
		authValue:   cfg.AuthValue,
		batchSize:   cfg.BatchSize,
		maxAttempts: cfg.MaxAttempts,
		backoffBase: cfg.BackoffBase,
		timeout:     cfg.Timeout,
		client:      cfg.Client,
		sleep:       cfg.Sleep,
	}
	if p.mode == "" {
		p.mode = ModeBatch
	}
	if p.mode != ModeBatch && p.mode != ModePerURL {
		return nil, fmt.Errorf("purge: unknown mode %q (want batch|per_url)", p.mode)
	}
	switch cfg.URLForm {
	case "", URLFull:
		p.fullURLs = true
	case URLRelative:
		p.fullURLs = false
	default:
		return nil, fmt.Errorf("purge: unknown url form %q (want full|relative)", cfg.URLForm)
	}
	if p.method == "" {
		p.method = http.MethodPost
	}
	if p.field == "" {
		p.field = "files"
	}
	if p.maxAttempts <= 0 {
		p.maxAttempts = 3
	}
	if p.backoffBase <= 0 {
		p.backoffBase = 200 * time.Millisecond
	}
	if p.timeout <= 0 {
		p.timeout = 10 * time.Second
	}
	if p.client == nil {
		p.client = &http.Client{}
	}
	if p.sleep == nil {
		p.sleep = time.Sleep
	}
	if p.fullURLs && strings.TrimSpace(p.baseURL) == "" {
		return nil, fmt.Errorf("purge: full url form needs a base url")
	}
	// In every shape except per_url against absolute site URLs, we need an endpoint.
	if strings.TrimSpace(p.endpoint) == "" && !(p.mode == ModePerURL && p.fullURLs) {
		return nil, fmt.Errorf("purge: endpoint required")
	}
	return p, nil
}

// Purge invalidates exactly the given URLs. In batch mode it chunks the list by
// BatchSize and sends one request per chunk; in per_url mode it sends one request
// per URL. A non-2xx or network error fails that request (its URLs count as not
// succeeded); when any fail, Purge returns a non-nil error so a deploy step can gate
// on it. An empty list is a successful no-op (no requests).
func (p *HTTPPurger) Purge(ctx context.Context, urls []string) (Result, error) {
	res := Result{Requested: len(urls)}
	if len(urls) == 0 {
		return res, nil
	}

	switch p.mode {
	case ModePerURL:
		for _, u := range urls {
			target := p.targetForPath(u)
			if err := p.doRequest(ctx, p.method, target, nil); err != nil {
				res.Errors = append(res.Errors, FailedURL{URL: u, Err: err.Error()})
				continue
			}
			res.Succeeded++
		}
	default: // ModeBatch
		for _, c := range chunk(urls, p.batchSize) {
			body, err := json.Marshal(map[string][]string{p.field: p.bodyURLs(c)})
			if err != nil {
				res.Errors = append(res.Errors, FailedURL{URL: "(batch)", Err: err.Error()})
				continue
			}
			if err := p.doRequest(ctx, p.method, p.endpoint, body); err != nil {
				res.Errors = append(res.Errors, FailedURL{URL: "(batch)", Err: err.Error()})
				continue
			}
			res.Succeeded += len(c)
		}
	}

	if len(res.Errors) > 0 {
		return res, fmt.Errorf("purge: %d of %d url(s) failed", res.Failed(), res.Requested)
	}
	return res, nil
}

// targetForPath resolves the request URL for a per_url request: the absolute site
// URL (full form) or the endpoint joined with the path (relative form).
func (p *HTTPPurger) targetForPath(path string) string {
	if p.fullURLs {
		return joinURL(p.baseURL, path)
	}
	return joinURL(p.endpoint, path)
}

// bodyURLs maps a chunk of paths to the URLs carried in a batch body: absolute URLs
// joined from BaseURL (full form) or the root-relative paths unchanged.
func (p *HTTPPurger) bodyURLs(paths []string) []string {
	if !p.fullURLs {
		return paths
	}
	out := make([]string, len(paths))
	for i, pth := range paths {
		out[i] = joinURL(p.baseURL, pth)
	}
	return out
}

// doRequest sends one request with bounded retry. It retries transient failures
// (network errors and 5xx) up to maxAttempts with exponential backoff and never
// retries a 4xx. A 2xx is success. The returned error names only the target URL and
// status, never the auth secret.
func (p *HTTPPurger) doRequest(ctx context.Context, method, target string, body []byte) error {
	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		status, err := p.attempt(ctx, method, target, body)
		switch {
		case err != nil:
			lastErr = err // transport/network error: retryable
		case status >= 200 && status < 300:
			return nil
		case status >= 500:
			lastErr = &statusError{status: status, target: target} // retryable
		default:
			return &statusError{status: status, target: target} // 4xx etc: no retry
		}
		if attempt < p.maxAttempts {
			p.sleep(p.backoffBase << (attempt - 1))
		}
	}
	return lastErr
}

// attempt performs exactly one HTTP request under a per-request timeout. cancel is
// deferred so go vet sees no context leak.
func (p *HTTPPurger) attempt(ctx context.Context, method, target string, body []byte) (int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, target, rdr)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.authHeader != "" && p.authValue != "" {
		req.Header.Set(p.authHeader, p.authValue)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// statusError reports a non-2xx response. It carries only the target URL and status
// code so an auth secret can never appear in a logged error.
type statusError struct {
	status int
	target string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("purge %s: unexpected status %d", e.target, e.status)
}

// joinURL joins a base origin (or endpoint) and a root-relative path without a
// double slash. An empty base returns the path unchanged.
func joinURL(base, path string) string {
	if base == "" {
		return path
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// chunk splits in into slices of at most size. A size <= 0 (or >= len) yields a
// single chunk, so a batch purge is one request unless a cap is configured.
func chunk(in []string, size int) [][]string {
	if size <= 0 || size >= len(in) {
		return [][]string{in}
	}
	out := make([][]string, 0, (len(in)+size-1)/size)
	for i := 0; i < len(in); i += size {
		end := i + size
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[i:end])
	}
	return out
}
