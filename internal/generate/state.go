package generate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// stateFile is the persisted prior-run state (.generated/state.json).
type stateFile struct {
	Version     int               `json:"version"`
	Fingerprint string            `json:"fingerprint"`
	GeneratedAt string            `json:"generated_at"`
	Artifacts   map[string]string `json:"artifacts"`
	URLs        map[string]string `json:"urls,omitempty"`
}

// purgeFile lists every changed and removed public URL for the run.
type purgeFile struct {
	Version     int      `json:"version"`
	GeneratedAt string   `json:"generated_at"`
	URLs        []string `json:"urls"`
}

// plan is the diff result: artifacts to write, paths left unchanged, paths to
// delete, and the public URLs to purge.
type plan struct {
	write     []Artifact
	unchanged []string
	deleteP   []string
	purgeURLs []string
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// optionsFingerprint captures the structural options that change every artifact
// path or boundary. A mismatch forces a full rewrite (and purges orphans).
func optionsFingerprint(o Options) string {
	return sha256hex([]byte(fmt.Sprintf("v2|P=%d|capE=%d|capB=%d|base=%s|lang=%s",
		o.PageSize, o.ShardMaxEntries, o.ShardMaxGzipBytes, strings.TrimRight(o.BaseURL, "/"), o.Lang)))
}

// diff compares the collected set against prior state. forceWrite (set on a
// fingerprint mismatch) skips the unchanged short-circuit but still diffs
// deletions so orphaned URLs purge.
func diff(cur *ArtifactSet, prior stateFile, forceWrite bool) plan {
	var p plan
	for _, path := range cur.sortedPaths() {
		a := cur.byPath[path]
		h := sha256hex(a.Bytes)
		if !forceWrite {
			if oldH, ok := prior.Artifacts[path]; ok && oldH == h {
				p.unchanged = append(p.unchanged, path)
				continue
			}
		}
		p.write = append(p.write, a)
		if a.URL != "" {
			p.purgeURLs = append(p.purgeURLs, a.URL)
		}
	}
	var deleted []string
	for path := range prior.Artifacts {
		if _, still := cur.byPath[path]; !still {
			deleted = append(deleted, path)
		}
	}
	sort.Strings(deleted)
	for _, path := range deleted {
		p.deleteP = append(p.deleteP, path)
		if url := prior.URLs[path]; url != "" {
			p.purgeURLs = append(p.purgeURLs, url)
		}
	}
	sort.Strings(p.purgeURLs)
	p.purgeURLs = dedupSorted(p.purgeURLs)
	return p
}

func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// loadState returns (prior, forceWrite). It returns ({}, false) only when the
// file is missing or unreadable/version-mismatched. When the file is readable
// but the fingerprint differs, prior is kept (so deletions still purge) and
// forceWrite is true.
func loadState(stateDir, wantFingerprint string) (stateFile, bool) {
	b, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil {
		return stateFile{}, false
	}
	var s stateFile
	if err := json.Unmarshal(b, &s); err != nil || s.Version != 1 {
		return stateFile{}, false
	}
	if s.Fingerprint == wantFingerprint {
		return s, false
	}
	return s, true
}

// buildState snapshots the collected set into the next persisted state.
func buildState(set *ArtifactSet, now time.Time, fingerprint string) stateFile {
	arts := map[string]string{}
	urls := map[string]string{}
	for _, path := range set.sortedPaths() {
		a := set.byPath[path]
		if a.Kind == KindState {
			continue
		}
		arts[path] = sha256hex(a.Bytes)
		if a.URL != "" {
			urls[path] = a.URL
		}
	}
	return stateFile{
		Version:     1,
		Fingerprint: fingerprint,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Artifacts:   arts,
		URLs:        urls,
	}
}

func saveState(stateDir string, s stateFile) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeFileAtomic(filepath.Join(stateDir, "state.json"), b, 0o644)
}

func writePurge(stateDir string, now time.Time, urls []string) error {
	if urls == nil {
		urls = []string{}
	}
	pf := purgeFile{
		Version:     1,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		URLs:        urls,
	}
	b, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeFileAtomic(filepath.Join(stateDir, "purge.json"), b, 0o644)
}
