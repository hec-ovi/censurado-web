package purge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// validManifest mirrors what internal/generate.writePurge emits: version 1, an
// RFC3339 timestamp, and root-relative public paths (never article permalinks).
const validManifest = `{
  "version": 1,
  "generated_at": "2026-06-22T12:00:00Z",
  "urls": [
    "/latest/",
    "/topic/ai/",
    "/topic/ai/2026/06.json",
    "/index.json"
  ]
}`

func TestParseManifest(t *testing.T) {
	t.Run("valid file decodes exactly", func(t *testing.T) {
		m, err := ParseManifest(strings.NewReader(validManifest))
		if err != nil {
			t.Fatalf("ParseManifest: %v", err)
		}
		if m.Version != 1 {
			t.Errorf("version = %d, want 1", m.Version)
		}
		if m.GeneratedAt != "2026-06-22T12:00:00Z" {
			t.Errorf("generated_at = %q", m.GeneratedAt)
		}
		want := []string{"/latest/", "/topic/ai/", "/topic/ai/2026/06.json", "/index.json"}
		if !equalStrings(m.URLs, want) {
			t.Errorf("urls = %v, want %v", m.URLs, want)
		}
	})

	t.Run("empty urls list is a valid no-op", func(t *testing.T) {
		m, err := ParseManifest(strings.NewReader(`{"version":1,"generated_at":"2026-06-22T12:00:00Z","urls":[]}`))
		if err != nil {
			t.Fatalf("ParseManifest: %v", err)
		}
		if len(m.URLs) != 0 {
			t.Errorf("urls = %v, want empty", m.URLs)
		}
		if m.URLs == nil {
			t.Errorf("urls is nil, want non-nil empty slice")
		}
	})

	t.Run("malformed JSON errors", func(t *testing.T) {
		if _, err := ParseManifest(strings.NewReader(`{"version":1,"urls":[`)); err == nil {
			t.Fatal("want error for malformed JSON, got nil")
		}
	})

	t.Run("unknown field is rejected (strict)", func(t *testing.T) {
		in := `{"version":1,"generated_at":"x","urls":[],"wildcard":true}`
		if _, err := ParseManifest(strings.NewReader(in)); err == nil {
			t.Fatal("want error for unknown field, got nil")
		}
	})

	t.Run("trailing data is rejected", func(t *testing.T) {
		in := `{"version":1,"generated_at":"x","urls":[]}{"version":1}`
		if _, err := ParseManifest(strings.NewReader(in)); err == nil {
			t.Fatal("want error for trailing data, got nil")
		}
	})

	t.Run("wrong version is rejected", func(t *testing.T) {
		in := `{"version":2,"generated_at":"x","urls":[]}`
		if _, err := ParseManifest(strings.NewReader(in)); err == nil {
			t.Fatal("want error for version 2, got nil")
		}
	})

	t.Run("non-root-relative urls are rejected (over-purge guard)", func(t *testing.T) {
		for _, in := range []string{
			`{"version":1,"generated_at":"x","urls":["/ok/","https://evil.example/x"]}`, // absolute URL with a scheme
			`{"version":1,"generated_at":"x","urls":["relative/no/slash"]}`,             // missing leading slash
			`{"version":1,"generated_at":"x","urls":[""]}`,                              // empty path would join to the site root
		} {
			if _, err := ParseManifest(strings.NewReader(in)); err == nil {
				t.Fatalf("want error for non-root-relative manifest %q, got nil", in)
			}
		}
	})
}

func TestDryRunPurger_NoNetworkExactURLs(t *testing.T) {
	m, err := ParseManifest(strings.NewReader(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	var out bytes.Buffer
	// DryRunPurger holds no http.Client, so there is no code path to the network;
	// it can only ever record and list. We assert it returns and writes EXACTLY the
	// manifest URLs.
	d := DryRunPurger{Out: &out}
	res, err := d.Purge(context.Background(), m.URLs)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if res.Requested != 4 || res.Succeeded != 4 || res.Failed() != 0 {
		t.Errorf("result = %+v, want 4/4/0", res)
	}
	if !equalStrings(res.Planned, m.URLs) {
		t.Errorf("planned = %v, want %v", res.Planned, m.URLs)
	}
	gotLines := nonEmptyLines(out.String())
	if !equalStrings(gotLines, m.URLs) {
		t.Errorf("written lines = %v, want %v", gotLines, m.URLs)
	}
}

func TestHTTPPurger_BatchMode(t *testing.T) {
	m, _ := ParseManifest(strings.NewReader(validManifest))
	const token = "super-secret-token-value"

	var (
		mu       sync.Mutex
		requests int
		gotMeth  string
		gotAuth  string
		gotBody  map[string][]string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		gotMeth = r.Method
		gotAuth = r.Header.Get("X-Auth-Token")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := NewHTTPPurger(HTTPConfig{
		Endpoint:   srv.URL + "/purge",
		Mode:       ModeBatch,
		Field:      "files",
		BaseURL:    "https://cdn.example",
		URLForm:    URLFull,
		AuthHeader: "X-Auth-Token",
		AuthValue:  token,
		Client:     srv.Client(),
		Sleep:      func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("NewHTTPPurger: %v", err)
	}
	res, err := p.Purge(context.Background(), m.URLs)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if res.Succeeded != 4 || res.Failed() != 0 {
		t.Errorf("result = %+v, want 4 succeeded", res)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want exactly 1 (batch)", requests)
	}
	if gotMeth != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMeth)
	}
	if gotAuth != token {
		t.Errorf("auth header = %q, want the token", gotAuth)
	}
	wantBody := []string{
		"https://cdn.example/latest/",
		"https://cdn.example/topic/ai/",
		"https://cdn.example/topic/ai/2026/06.json",
		"https://cdn.example/index.json",
	}
	if !equalStrings(gotBody["files"], wantBody) {
		t.Errorf("body files = %v, want %v", gotBody["files"], wantBody)
	}
	for _, u := range gotBody["files"] {
		if strings.Contains(strings.TrimPrefix(u, "https://"), "//") {
			t.Errorf("double slash in joined URL: %q", u)
		}
	}
	// No-leak on the SUCCESS path: nothing the tool returns (Result, its failures,
	// or any stringified summary) may carry the auth secret, even when the purge
	// succeeded. The auth header was sent (asserted above); the token must not echo
	// back into anything renderable.
	assertNoToken(t, token, fmt.Sprintf("%+v", res))
	for _, e := range res.Errors {
		assertNoToken(t, token, e.URL, e.Err)
	}
}

func TestHTTPPurger_PerURLMode(t *testing.T) {
	m, _ := ParseManifest(strings.NewReader(validManifest))
	const token = "per-url-secret-0xABCD1234"

	var (
		mu      sync.Mutex
		hits    []string
		auths   = map[string]bool{}
		methods = map[string]bool{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		hits = append(hits, r.URL.Path)
		auths[r.Header.Get("X-Auth-Token")] = true
		methods[r.Method] = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// per_url against the absolute site URL (Fastly-style PURGE). BaseURL is the
	// test server so every request lands on it, but the path proves the join.
	p, err := NewHTTPPurger(HTTPConfig{
		Method:     "PURGE",
		Mode:       ModePerURL,
		BaseURL:    srv.URL,
		URLForm:    URLFull,
		AuthHeader: "X-Auth-Token",
		AuthValue:  token,
		Client:     srv.Client(),
		Sleep:      func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("NewHTTPPurger: %v", err)
	}
	res, err := p.Purge(context.Background(), m.URLs)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if res.Succeeded != 4 {
		t.Errorf("succeeded = %d, want 4", res.Succeeded)
	}
	if len(hits) != 4 {
		t.Fatalf("got %d requests, want exactly 4 (one per url)", len(hits))
	}
	gotPaths := append([]string(nil), hits...)
	if !equalStringsUnordered(gotPaths, m.URLs) {
		t.Errorf("hit paths = %v, want exactly the manifest urls %v", gotPaths, m.URLs)
	}
	if len(methods) != 1 || !methods["PURGE"] {
		t.Errorf("methods = %v, want only PURGE", methods)
	}
	// The auth header was sent on every request...
	if len(auths) != 1 || !auths[token] {
		t.Errorf("auth headers seen = %v, want only the token", auths)
	}
	// ...yet the SUCCESS-path Result must not echo the token back anywhere.
	assertNoToken(t, token, fmt.Sprintf("%+v", res))
	for _, e := range res.Errors {
		assertNoToken(t, token, e.URL, e.Err)
	}
}

func TestHTTPPurger_Retry500ThenFail(t *testing.T) {
	const token = "leak-me-not-0xDEADBEEF"
	var calls int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p, err := NewHTTPPurger(HTTPConfig{
		Endpoint:    srv.URL,
		Mode:        ModeBatch,
		URLForm:     URLRelative,
		AuthHeader:  "Authorization",
		AuthValue:   token,
		MaxAttempts: 3,
		Client:      srv.Client(),
		Sleep:       func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("NewHTTPPurger: %v", err)
	}
	res, err := p.Purge(context.Background(), []string{"/latest/"})
	if err == nil {
		t.Fatal("want error after exhausting retries, got nil")
	}
	if res.Succeeded != 0 || res.Failed() != 1 {
		t.Errorf("result = %+v, want 0 succeeded / 1 failed", res)
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 3 {
		t.Errorf("server calls = %d, want 3 (bounded retry)", gotCalls)
	}
	// The auth secret must never surface in the error or per-url failure detail.
	assertNoToken(t, token, err.Error())
	for _, e := range res.Errors {
		assertNoToken(t, token, e.URL, e.Err)
	}
}

// countingErrTransport is a RoundTripper that always fails (no server reached) and
// counts how many times it was invoked, so a test can assert the retry loop tried
// exactly MaxAttempts times on a transport/network error.
type countingErrTransport struct {
	mu    sync.Mutex
	calls int
}

func (t *countingErrTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return nil, errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
}

func (t *countingErrTransport) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

// TestHTTPPurger_RetryNetworkErrorThenFail exercises the OTHER retryable branch in
// doRequest: a transport/network error (err != nil), distinct from a 5xx status.
// The injected RoundTripper always errors, so the loop must retry up to MaxAttempts
// and then fail the request. Without this, only the 5xx branch was covered and a
// regression that stopped retrying network errors would pass every other test.
func TestHTTPPurger_RetryNetworkErrorThenFail(t *testing.T) {
	const token = "net-secret-0xFEEDFACE"
	rt := &countingErrTransport{}
	p, err := NewHTTPPurger(HTTPConfig{
		Endpoint:    "http://127.0.0.1:1/purge", // never reached; transport always errors
		Mode:        ModeBatch,
		URLForm:     URLRelative,
		AuthHeader:  "Authorization",
		AuthValue:   token,
		MaxAttempts: 3,
		Client:      &http.Client{Transport: rt},
		Sleep:       func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("NewHTTPPurger: %v", err)
	}
	res, perr := p.Purge(context.Background(), []string{"/latest/"})
	if perr == nil {
		t.Fatal("want error after exhausting network-error retries, got nil")
	}
	if res.Succeeded != 0 || res.Failed() != 1 {
		t.Errorf("result = %+v, want 0 succeeded / 1 failed", res)
	}
	if got := rt.count(); got != 3 {
		t.Errorf("transport calls = %d, want 3 (bounded retry on network error)", got)
	}
	// The secret must not leak through a transport-error path either.
	assertNoToken(t, token, perr.Error())
	for _, e := range res.Errors {
		assertNoToken(t, token, e.URL, e.Err)
	}
}

func TestHTTPPurger_4xxNoRetry(t *testing.T) {
	var calls int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	p, err := NewHTTPPurger(HTTPConfig{
		Endpoint:    srv.URL,
		Mode:        ModeBatch,
		URLForm:     URLRelative,
		MaxAttempts: 5,
		Client:      srv.Client(),
		Sleep:       func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("NewHTTPPurger: %v", err)
	}
	res, err := p.Purge(context.Background(), []string{"/latest/"})
	if err == nil {
		t.Fatal("want error on 4xx, got nil")
	}
	if res.Failed() != 1 {
		t.Errorf("failed = %d, want 1", res.Failed())
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Errorf("server calls = %d, want 1 (4xx is not retried)", gotCalls)
	}
}

func TestHTTPPurger_Batching(t *testing.T) {
	urls := []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g"}
	var (
		mu       sync.Mutex
		requests int
		seen     []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string][]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		requests++
		seen = append(seen, body["files"]...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := NewHTTPPurger(HTTPConfig{
		Endpoint:  srv.URL,
		Mode:      ModeBatch,
		URLForm:   URLRelative,
		BatchSize: 3,
		Client:    srv.Client(),
		Sleep:     func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("NewHTTPPurger: %v", err)
	}
	res, err := p.Purge(context.Background(), urls)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if res.Succeeded != 7 {
		t.Errorf("succeeded = %d, want 7", res.Succeeded)
	}
	// 7 urls, batch size 3 -> ceil(7/3) = 3 requests.
	if requests != 3 {
		t.Errorf("requests = %d, want 3 chunks", requests)
	}
	if !equalStringsUnordered(seen, urls) {
		t.Errorf("seen across chunks = %v, want exactly %v", seen, urls)
	}
}

func TestHTTPPurger_EmptyManifestNoRequests(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := NewHTTPPurger(HTTPConfig{
		Endpoint: srv.URL,
		Mode:     ModeBatch,
		URLForm:  URLRelative,
		Client:   srv.Client(),
		Sleep:    func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("NewHTTPPurger: %v", err)
	}
	res, err := p.Purge(context.Background(), []string{})
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if res.Requested != 0 || res.Succeeded != 0 || res.Failed() != 0 {
		t.Errorf("result = %+v, want all zero", res)
	}
	if calls != 0 {
		t.Errorf("server calls = %d, want 0 for empty manifest", calls)
	}
}

func TestNewHTTPPurger_Validation(t *testing.T) {
	cases := []struct {
		name string
		cfg  HTTPConfig
		ok   bool
	}{
		{"batch needs endpoint", HTTPConfig{Mode: ModeBatch, URLForm: URLRelative}, false},
		{"full needs base url", HTTPConfig{Mode: ModeBatch, Endpoint: "https://x", URLForm: URLFull}, false},
		{"bad mode", HTTPConfig{Mode: "wild", Endpoint: "https://x", URLForm: URLRelative}, false},
		{"bad url form", HTTPConfig{Mode: ModeBatch, Endpoint: "https://x", URLForm: "absolute"}, false},
		{"per_url full needs no endpoint", HTTPConfig{Mode: ModePerURL, URLForm: URLFull, BaseURL: "https://x"}, true},
		{"valid batch relative", HTTPConfig{Mode: ModeBatch, Endpoint: "https://x", URLForm: URLRelative}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewHTTPPurger(c.cfg)
			if c.ok && err != nil {
				t.Errorf("want ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

// --- helpers ---

func assertNoToken(t *testing.T, token string, in ...string) {
	t.Helper()
	for _, s := range in {
		if strings.Contains(s, token) {
			t.Errorf("auth token leaked in output: %q", s)
		}
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStringsUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	return equalStrings(ac, bc)
}
