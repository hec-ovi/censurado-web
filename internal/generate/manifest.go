package generate

import (
	"encoding/json"
	"html/template"
)

// PageManifest is embedded in each Tier-A page as a JSON script element. It
// describes the scope's shard set (newest-month-first) and the facet equalities
// the scope guarantees, so the client-side refiner can fetch and filter shards.
type PageManifest struct {
	Scope  string            `json:"scope"`
	Axis   map[string]string `json:"axis"`
	Shards []ShardRef        `json:"shards"`
	Total  int               `json:"total"`
	Cap    ManifestCaps      `json:"cap"`
}

type ShardRef struct {
	URL   string   `json:"url"`
	Month string   `json:"month"`
	Count int      `json:"count"`
	Parts []string `json:"parts,omitempty"`
}

type ManifestCaps struct {
	Entries int `json:"entries"`
	Bytes   int `json:"bytes"`
}

// BuildManifest assembles a scope's manifest from its newest-first months and
// the parts emitted per month.
func BuildManifest(s Scope, capE, capB int, monthsNewestFirst []ym, partsByMonth map[string][]ShardPart, total int) PageManifest {
	refs := make([]ShardRef, 0, len(monthsNewestFirst))
	for _, m := range monthsNewestFirst {
		key := monthKey(m.Year, m.Month)
		parts := partsByMonth[key]
		ref := ShardRef{URL: "/" + parts[0].Path, Month: key, Count: sumCounts(parts)}
		if len(parts) > 1 {
			ref.Parts = make([]string, 0, len(parts))
			for i := len(parts) - 1; i >= 0; i-- {
				ref.Parts = append(ref.Parts, "/"+parts[i].Path)
			}
		}
		refs = append(refs, ref)
	}
	return PageManifest{
		Scope:  s.PathPrefix(),
		Axis:   axisFor(s),
		Shards: refs,
		Total:  total,
		Cap:    ManifestCaps{Entries: capE, Bytes: capB},
	}
}

func sumCounts(parts []ShardPart) int {
	n := 0
	for _, p := range parts {
		n += p.Count
	}
	return n
}

// axisFor returns the scope's facet equalities (slug form). Latest is {}.
func axisFor(s Scope) map[string]string {
	ax := map[string]string{}
	if s.isSingle() {
		switch s.Section.Kind {
		case AxisSection:
			ax["section"] = s.Section.Value
		case AxisAuthor:
			ax["author"] = s.Section.Value
		case AxisTopic:
			ax["topic"] = s.Section.Value
		case AxisMonth:
			ax["month"] = monthKey(s.Section.Year, s.Section.Month)
		}
		return ax
	}
	ax["section"] = s.Section.Value
	switch s.Sub.Kind {
	case AxisAuthor:
		ax["author"] = s.Sub.Value
	case AxisTopic:
		ax["topic"] = s.Sub.Value
	case AxisMonth:
		ax["month"] = monthKey(s.Sub.Year, s.Sub.Month)
	}
	return ax
}

// ManifestScript is the only embed mechanism. json.Marshal (EscapeHTML on by
// default) \u-escapes <,>,& so there is no </script> breakout. It is rendered
// with a bare {{.Manifest}} as template.HTML.
func ManifestScript(m PageManifest) (template.HTML, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return template.HTML(`<script type="application/json" id="cnz-manifest">` + string(b) + `</script>`), nil
}
