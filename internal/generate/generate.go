package generate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hec-ovi/censurado-web/internal/store"
)

// Options configures one Generate run. Validate fills defaults in place.
type Options struct {
	OutDir            string    // required; artifact root, created if absent
	StateDir          string    // optional; default OutDir/.generated
	BaseURL           string    // required; absolute origin, trailing slash trimmed
	PageSize          int       // P: articles per full immutable page; default 20, >=1
	ShardMaxEntries   int       // entries cap per shard part; default 500, >=1
	ShardMaxGzipBytes int       // gzipped-bytes cap per shard part; default 200*1024, >=1
	Now               time.Time // build clock; default time.Now().UTC(); never used for content identity
	SiteName          string    // feed/site title; default "El Censurado Web"
}

// Validate fills defaults in place, trims the BaseURL trailing slash, and
// rejects empty OutDir/BaseURL, PageSize<1, and either cap<1.
func (o *Options) Validate() error {
	o.OutDir = strings.TrimSpace(o.OutDir)
	if o.OutDir == "" {
		return errors.New("generate: OutDir required")
	}
	o.BaseURL = strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if o.BaseURL == "" {
		return errors.New("generate: BaseURL required")
	}
	if o.StateDir == "" {
		o.StateDir = filepath.Join(o.OutDir, ".generated")
	}
	if o.PageSize == 0 {
		o.PageSize = 20
	}
	if o.PageSize < 1 {
		return errors.New("generate: PageSize must be >= 1")
	}
	if o.ShardMaxEntries == 0 {
		o.ShardMaxEntries = 500
	}
	if o.ShardMaxEntries < 1 {
		return errors.New("generate: ShardMaxEntries must be >= 1")
	}
	if o.ShardMaxGzipBytes == 0 {
		o.ShardMaxGzipBytes = 200 * 1024
	}
	if o.ShardMaxGzipBytes < 1 {
		return errors.New("generate: ShardMaxGzipBytes must be >= 1")
	}
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	if o.SiteName == "" {
		o.SiteName = "El Censurado Web"
	}
	return nil
}

// Result reports the outcome of one Generate run.
type Result struct {
	Written    int      // artifacts whose bytes differ from prior state
	Unchanged  int      // artifacts whose bytes match prior state
	Deleted    int      // artifacts in prior state, absent now
	Purge      []string // sorted, deduped root-relative public URLs to invalidate
	TierAURLs  int      // distinct Tier-A HTML listing URLs this run
	ScopeCount int      // distinct canonical scopes this run
	Errors     []error  // non-fatal per-collector warnings; empty on a clean run
}

// Generate materializes the complete static site for the current store state
// into opts.OutDir, writing only artifacts whose content changed since the prior
// run, deleting artifacts no longer produced, and writing StateDir/purge.json.
//
// It is idempotent (a second call over an unchanged store writes nothing) and
// crash-safe (state.json is written last). repo is read-only.
func Generate(ctx context.Context, repo store.Repository, opts Options) (Result, error) {
	if err := opts.Validate(); err != nil {
		return Result{}, err
	}
	if err := sweepTempFiles(opts.OutDir); err != nil {
		return Result{}, err
	}
	env := buildEnvFrom(opts)

	plan, err := BuildPlan(ctx, repo, opts.PageSize)
	if err != nil {
		return Result{}, err
	}
	env.plan = plan
	if env.bodyHTML, err = renderBodiesOnce(plan); err != nil {
		return Result{}, err
	}

	set := newArtifactSet()
	for _, c := range collectors() {
		if err := c.collect(ctx, env, set); err != nil {
			return Result{}, fmt.Errorf("collect %s: %w", c.name(), err)
		}
	}

	fingerprint := optionsFingerprint(opts)
	prior, forceWrite := loadState(opts.StateDir, fingerprint)
	p := diff(set, prior, forceWrite)

	if err := os.MkdirAll(opts.StateDir, 0o755); err != nil {
		return Result{}, err
	}
	if err := applyPlan(opts.OutDir, p); err != nil {
		return Result{}, err
	}
	if err := writePurge(opts.StateDir, env.now, p.purgeURLs); err != nil {
		return Result{}, err
	}
	if err := saveState(opts.StateDir, buildState(set, env.now, fingerprint)); err != nil {
		return Result{}, err
	}

	purge := p.purgeURLs
	if purge == nil {
		purge = []string{}
	}
	return Result{
		Written:    len(p.write),
		Unchanged:  len(p.unchanged),
		Deleted:    len(p.deleteP),
		Purge:      purge,
		TierAURLs:  plan.TierAURLCount(),
		ScopeCount: len(plan.Index.Scopes()),
	}, nil
}
