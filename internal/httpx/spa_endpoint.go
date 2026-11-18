package httpx

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/joakimcarlsson/flight-club/web"
)

// registerSPA serves the embedded SPA at "/". More specific routes are matched
// first.
func (s *Server) registerSPA() {
	dist, err := fs.Sub(web.Dist, "dist")
	// A missing embed is a broken build, not a runtime condition: panic
	// rather than serve an API with no UI behind it.
	if err != nil {
		panic(fmt.Sprintf("httpx: embedded SPA assets: %v", err))
	}
	s.router.HandleFunc(http.MethodGet, "/", spaHandler(dist))
}

// spaHandler serves real files from the embedded build output, falling back to
// index.html for unknown client-side routes. Backend prefixes get a clean 404
// instead of the SPA shell.
func spaHandler(dist fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(dist))
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		panic(fmt.Sprintf("httpx: reading embedded index.html: %v", err))
	}
	return func(w http.ResponseWriter, req *http.Request) {
		clean := strings.TrimPrefix(path.Clean(req.URL.Path), "/")
		if isBackendPath(clean) {
			http.NotFound(w, req)
			return
		}
		if clean != "" {
			if f, err := dist.Open(clean); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, req)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	}
}

// isBackendPath reports whether a cleaned path belongs to the API surface, so
// the SPA shell is never returned for it.
func isBackendPath(clean string) bool {
	return clean == "api" || strings.HasPrefix(clean, "api/")
}
