package generate

import (
	"context"
	"encoding/json"
	"html/template"

	"github.com/hec-ovi/censurado-web-backend/store"
)

// The generator renders one language per run. env.text holds the frontend_text
// rows for that language (loaded once from the store), overlaying the compiled
// Spanish catalog in catalog.go. A key absent from the table falls back to the
// compiled value, so an unseeded database renders today's live Spanish bytes.

// T resolves a UI-string key to the run's language: the frontend_text row when the
// table carries it, else the compiled Spanish default, else the key itself (only if
// a caller references a key with no catalog entry, which the tests guard against).
func (env *buildEnv) T(key string) string {
	if env.text != nil {
		if v, ok := env.text[key]; ok {
			return v
		}
	}
	if v, ok := defaultText[key]; ok {
		return v
	}
	return key
}

// lookup reports whether a key is known (from the table or the compiled catalog)
// and returns its resolved value. Callers that have their own fallback (section
// labels fall back to the stored, Title-cased label) use this to tell "no such
// key" apart from an empty value.
func (env *buildEnv) lookup(key string) (string, bool) {
	if env.text != nil {
		if v, ok := env.text[key]; ok {
			return v, true
		}
	}
	if v, ok := defaultText[key]; ok {
		return v, true
	}
	return "", false
}

// loadText loads the frontend_text rows for env.lang from the store into env.text.
// The store is read through the optional TextStore interface (a type assertion,
// like the author/topic/portada/recomendado overlays), so a repo that does not
// implement it, or an unseeded table, leaves env.text empty and every string
// renders from the compiled catalog. Templates are already bound (buildEnvFrom):
// the "t" func closes over env and reads env.text at render time, so populating
// the map here is enough.
func (env *buildEnv) loadText(ctx context.Context, repo store.Repository) error {
	if ts, ok := repo.(store.TextStore); ok {
		m, err := ts.Text(ctx, store.ScopeFrontend, env.lang)
		if err != nil {
			return err
		}
		env.text = m
	}
	// Build the client blob after env.text is populated, so it reflects DB overrides.
	env.clientI18N = env.buildClientI18N()
	return nil
}

// buildClientI18N returns the <script> that seeds window.__CNZ_I18N__ with the
// resolved values of the client keys (see clientTextKeys), for app.js to read when
// it rebuilds cards. It is a pure function of the run language, so it is identical
// on every page and keeps sealed pages byte-stable. json.Marshal sorts the keys
// (deterministic) and escapes <, >, & to \uXXXX, so a translated value can never
// break out of the <script> element.
func (env *buildEnv) buildClientI18N() template.HTML {
	m := make(map[string]string, len(clientTextKeys))
	for _, k := range clientTextKeys {
		m[k] = env.T(k)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return template.HTML(`<script>window.__CNZ_I18N__=` + string(b) + `;</script>`)
}

// bindTemplates clones the parsed base templates and binds the language-aware "t"
// lookup for this run. Cloning gives each run its own copy, so a concurrent watch
// pass and a manual one-shot never race on the shared FuncMap. The bound "t"
// closes over env, so it sees whatever env.text loadText later populates.
func (env *buildEnv) bindTemplates() {
	funcs := template.FuncMap{
		"t":       env.T,
		"cnzI18N": func() template.HTML { return env.clientI18N },
	}
	env.listingTmpl = template.Must(listingTmplBase.Clone()).Funcs(funcs)
	env.articleTmpl = template.Must(articleTmplBase.Clone()).Funcs(funcs)
	env.aboutTmpl = template.Must(aboutTmplBase.Clone()).Funcs(funcs)
	env.notFoundTmpl = template.Must(notFoundTmplBase.Clone()).Funcs(funcs)
}
