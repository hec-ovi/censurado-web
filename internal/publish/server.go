package publish

import "net/http"

// NewServerHandler assembles the publish HTTP surface: an unauthenticated liveness
// probe, the authenticated POST /articles route, and POST /articles:batch, all
// behind the rate limiter.
//
// Routing uses the Go 1.22 ServeMux. GET /healthz is matched by method so it never
// touches auth or the limiter. /articles is registered for ALL methods (no method
// in the pattern) so the Handler keeps doing its own dispatch: it answers 405 for
// non-POST, 401/403 for auth failures, and so on. POST /articles:batch is a
// distinct static route (the ':' is a literal path byte to ServeMux, so it never
// collides with /articles), bound to the batch handler. Both run through one
// limiter.Wrap, so a batch is charged exactly one token no matter how many articles
// it carries. A nil limiter disables limiting.
//
// When mediaH is non-nil, the self-hosted image CDN is mounted too: POST /media
// (authenticated upload, rate-limited like a write) and GET /media/{name} (public,
// immutable read, not rate-limited since it is cacheable and keyless). A nil mediaH
// leaves media off entirely.
//
// When readH is non-nil, the authenticated JSON read API is mounted: GET /authors,
// GET /topics, GET /articles, and GET /articles/{slug}. These are method-specific
// patterns, so GET /articles takes precedence over the method-less /articles write
// route for GET while POST still reaches the write handler; reads are not rate
// limited (idempotent, cacheable). A nil readH leaves the read API off, so the
// write handler keeps answering 405 for a GET /articles.
func NewServerHandler(h *Handler, limiter *RateLimiter, mediaH *MediaHandler, readH *ReadHandler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("/articles", limiter.Wrap(h))
	mux.Handle("POST /articles:batch", limiter.Wrap(http.HandlerFunc(h.ServeBatch)))

	if mediaH != nil {
		mux.Handle("POST /media", limiter.Wrap(http.HandlerFunc(mediaH.ServeUpload)))
		mux.HandleFunc("GET /media/{name}", mediaH.ServeFile)
	}

	if readH != nil {
		mux.HandleFunc("GET /authors", readH.ServeAuthors)
		mux.HandleFunc("GET /topics", readH.ServeTopics)
		mux.HandleFunc("GET /articles", readH.ServeArticles)
		mux.HandleFunc("GET /articles/{slug}", readH.ServeArticle)
	}

	return mux
}
