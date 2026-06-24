package publish

import "net/http"

// NewServerHandler assembles the publish HTTP surface: an unauthenticated liveness
// probe and the authenticated POST /articles route behind the rate limiter.
//
// Routing uses the Go 1.22 ServeMux. GET /healthz is matched by method so it never
// touches auth or the limiter. /articles is registered for ALL methods (no method
// in the pattern) so the Handler keeps doing its own dispatch: it answers 405 for
// non-POST, 401/403 for auth failures, and so on. A nil limiter disables limiting.
//
// When mediaH is non-nil, the self-hosted image CDN is mounted too: POST /media
// (authenticated upload, rate-limited like a write) and GET /media/{name} (public,
// immutable read, not rate-limited since it is cacheable and keyless). A nil mediaH
// leaves media off entirely.
func NewServerHandler(h *Handler, limiter *RateLimiter, mediaH *MediaHandler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("/articles", limiter.Wrap(h))

	if mediaH != nil {
		mux.Handle("POST /media", limiter.Wrap(http.HandlerFunc(mediaH.ServeUpload)))
		mux.HandleFunc("GET /media/{name}", mediaH.ServeFile)
	}

	return mux
}
