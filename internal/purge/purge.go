// Package purge invalidates exactly the URLs the static generator changed in a
// batch. After every generate run the generator writes <out>/.generated/purge.json
// (a small version+timestamp+urls document; see internal/generate/state.go, purgeFile).
// ParseManifest reads that file and a Purger invalidates precisely those
// root-relative public paths at the CDN, never more and never a wildcard. Article
// permalinks use content-hash URLs with a long TTL and are never in the list, so
// they are never purged.
//
// DryRunPurger is the default: it makes no network calls and just lists what it
// would invalidate, so an operator cannot accidentally hit a live CDN. HTTPPurger
// is a vendor-neutral HTTP adapter (batch or per-url, relative or absolute URLs,
// configurable method/endpoint/field/auth) with bounded retry on transient errors.
package purge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Manifest is the decoded purge.json contract emitted by the generator. It mirrors
// internal/generate.purgeFile exactly: a schema version, an RFC3339 timestamp, and
// the root-relative public paths that changed or were removed this batch.
type Manifest struct {
	Version     int      `json:"version"`
	GeneratedAt string   `json:"generated_at"`
	URLs        []string `json:"urls"`
}

// ParseManifest strictly decodes a purge.json document. Unknown fields and trailing
// data are rejected so a malformed or wrong-schema file fails loudly. An empty URLs
// list is valid: it is a no-op batch (nothing changed). The decoded URLs slice is
// never nil so callers can range over it safely.
func ParseManifest(r io.Reader) (Manifest, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if dec.More() {
		return Manifest{}, fmt.Errorf("parse manifest: unexpected trailing data after JSON object")
	}
	if m.Version != 1 {
		return Manifest{}, fmt.Errorf("parse manifest: unsupported version %d (want 1)", m.Version)
	}
	// Every URL must be a root-relative path. The generator guarantees this, but the
	// consumer validates it so a drifted or hand-edited manifest can never join into
	// an absolute URL or an empty path that would purge the site root (an over-broad
	// purge is the one catastrophic failure mode here).
	for i, u := range m.URLs {
		if !strings.HasPrefix(u, "/") || strings.Contains(u, "://") {
			return Manifest{}, fmt.Errorf("parse manifest: urls[%d] = %q is not a root-relative path (must begin with %q and carry no scheme)", i, u, "/")
		}
	}
	if m.URLs == nil {
		m.URLs = []string{}
	}
	return m, nil
}

// FailedURL records a single URL (or, for a batch request, the batch) that failed
// to purge along with a redacted error string. The error never carries the auth
// secret.
type FailedURL struct {
	URL string
	Err string
}

// Result reports the outcome of a purge run: how many URLs were requested, how many
// succeeded, the per-URL or per-batch failures, and (for DryRunPurger) the exact
// list it would have purged.
type Result struct {
	Requested int
	Succeeded int
	Planned   []string // DryRunPurger: the URLs it would purge; nil for live purges
	Errors    []FailedURL
}

// Failed is the count of URLs not successfully purged.
func (r Result) Failed() int { return r.Requested - r.Succeeded }

// Purger invalidates a set of root-relative public paths at a CDN. Implementations
// must purge exactly the given URLs, never a wildcard.
type Purger interface {
	Purge(ctx context.Context, urls []string) (Result, error)
}

// DryRunPurger performs no network calls. It is the default Purger so an operator
// never hits a live CDN by accident. It writes the URLs it would purge, one per
// line, to Out (when set) and returns them in Result.Planned. Because it holds no
// http.Client there is no code path that can reach the network.
type DryRunPurger struct {
	Out io.Writer // where the would-purge list is written; nil writes nothing
}

// Purge records and lists the URLs without contacting any CDN. It always succeeds.
func (d DryRunPurger) Purge(_ context.Context, urls []string) (Result, error) {
	planned := append([]string(nil), urls...)
	if d.Out != nil {
		for _, u := range urls {
			fmt.Fprintln(d.Out, u)
		}
	}
	return Result{Requested: len(urls), Succeeded: len(urls), Planned: planned}, nil
}
