package publish

import "net/http"

// NewServerHandler assembles the publish HTTP surface: an unauthenticated liveness
// probe and the authenticated POST /articles route behind the rate limiter.
//
// Routing uses the Go 1.22 ServeMux. GET /healthz is matched by method so it never
// touches auth or the limiter. /articles is registered for ALL methods (no method
// in the pattern) so the Handler keeps doing its own dispatch: it answers 405 for
// non-POST, 401/403 for auth failures, and so on. A nil limiter disables limiting.
func NewServerHandler(h *Handler, limiter *RateLimiter) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("/articles", limiter.Wrap(h))

	return mux
}
