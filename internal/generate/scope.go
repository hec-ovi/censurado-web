package generate

import (
	"fmt"
	"time"

	"github.com/hec-ovi/censurado-web-backend/store"
)

// AxisKind identifies which facet axis a Facet belongs to.
type AxisKind int

const (
	AxisLatest AxisKind = iota
	AxisSection
	AxisAuthor
	AxisTopic
	AxisMonth
)

// Facet is one resolved facet value on an axis. Value/Label are empty for Latest
// and Month; Year/Month are set only for AxisMonth. Label carries the original
// stored casing of the earliest-inserted carrier.
type Facet struct {
	Kind  AxisKind
	Value string // slug form; "" for Latest and Month
	Label string // original casing; "" for Latest and Month
	Year  int    // AxisMonth only
	Month int    // AxisMonth only (1..12)
}

// Scope is a Tier-A listing scope. Single-facet scopes carry the facet in
// Section and leave Sub zero; section-anchored matrix scopes set both.
type Scope struct {
	Section Facet
	Sub     Facet
}

// isSingle reports whether the scope is a single-facet scope (Sub is the zero
// Facet, i.e. AxisLatest with no value/year).
func (s Scope) isSingle() bool {
	return s.Sub.Kind == AxisLatest && s.Sub.Value == "" && s.Sub.Year == 0
}

// PathPrefix returns the scope's public path with leading and trailing slash.
func (s Scope) PathPrefix() string {
	if s.isSingle() {
		switch s.Section.Kind {
		case AxisLatest:
			return "/latest/"
		case AxisSection:
			return "/section/" + s.Section.Value + "/"
		case AxisAuthor:
			return "/author/" + s.Section.Value + "/"
		case AxisTopic:
			return "/topic/" + s.Section.Value + "/"
		case AxisMonth:
			return fmt.Sprintf("/%04d/%02d/", s.Section.Year, s.Section.Month)
		}
	}
	base := "/section/" + s.Section.Value + "/"
	switch s.Sub.Kind {
	case AxisAuthor:
		return base + "author/" + s.Sub.Value + "/"
	case AxisTopic:
		return base + "topic/" + s.Sub.Value + "/"
	case AxisMonth:
		return base + fmt.Sprintf("%04d/%02d/", s.Sub.Year, s.Sub.Month)
	}
	return base
}

// scopePath is PathPrefix without the surrounding slashes.
func (s Scope) scopePath() string {
	p := s.PathPrefix()
	return p[1 : len(p)-1]
}

func (s Scope) PageURL(n int) string { return pageURL(s.scopePath(), n) }
func (s Scope) ShardKey() string     { return s.scopePath() }

// labelOr returns the original-casing label when present, else the slug value.
func (f Facet) labelOr() string {
	if f.Label != "" {
		return f.Label
	}
	return f.Value
}

// FilterFor builds the single-valued store.Filter that selects this scope's
// articles. It passes the original-casing Label (store.Filter matches stored
// casing). It is a fixture-only verifier: a single slug may fold multiple stored
// casings, which production handles in memory but this Filter cannot.
func (s Scope) FilterFor() store.Filter {
	if s.isSingle() {
		switch s.Section.Kind {
		case AxisLatest:
			return store.Filter{Order: store.OldestFirst}
		case AxisSection:
			return store.Filter{Section: s.Section.labelOr(), Order: store.OldestFirst}
		case AxisAuthor:
			return store.Filter{Author: s.Section.labelOr(), Order: store.OldestFirst}
		case AxisTopic:
			return store.Filter{Topic: s.Section.labelOr(), Order: store.OldestFirst}
		case AxisMonth:
			from := firstOfMonth(s.Section.Year, s.Section.Month)
			return store.Filter{From: from, To: from.AddDate(0, 1, 0), Order: store.OldestFirst}
		}
	}
	f := store.Filter{Section: s.Section.labelOr(), Order: store.OldestFirst}
	switch s.Sub.Kind {
	case AxisAuthor:
		f.Author = s.Sub.labelOr()
	case AxisTopic:
		f.Topic = s.Sub.labelOr()
	case AxisMonth:
		from := firstOfMonth(s.Sub.Year, s.Sub.Month)
		f.From = from
		f.To = from.AddDate(0, 1, 0)
	}
	return f
}

func firstOfMonth(y, m int) time.Time {
	return time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
}
