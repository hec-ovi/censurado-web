package generate

import (
	"fmt"
	"sort"
)

// Kind classifies an artifact for diffing and purge handling.
type Kind uint8

const (
	KindPage  Kind = iota // HTML page at a public URL (listing, article, landing); embeds its manifest
	KindShard             // month shard JSON at a public URL
	KindMeta              // sitemap.xml, feed.xml, atom.xml, feed.json: public, purgeable
	KindState             // state.json, purge.json: not public, never self-listed in purge
)

// Artifact is one file the generator produces.
type Artifact struct {
	Path  string // OutDir-relative POSIX path, no leading slash
	URL   string // root-relative public URL with leading slash, or "" for KindState
	Kind  Kind
	Bytes []byte // exact bytes to write; the diff hashes these literally
}

// ArtifactSet accumulates collected artifacts, rejecting duplicate paths.
type ArtifactSet struct {
	byPath map[string]Artifact
}

func newArtifactSet() *ArtifactSet { return &ArtifactSet{byPath: map[string]Artifact{}} }

func (s *ArtifactSet) Add(a Artifact) error {
	if a.Path == "" {
		return fmt.Errorf("generate: empty artifact path")
	}
	if _, dup := s.byPath[a.Path]; dup {
		return fmt.Errorf("generate: duplicate artifact path %q", a.Path)
	}
	s.byPath[a.Path] = a
	return nil
}

func (s *ArtifactSet) sortedPaths() []string {
	out := make([]string, 0, len(s.byPath))
	for p := range s.byPath {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
