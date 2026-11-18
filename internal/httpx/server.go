package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/config"
	"github.com/joakimcarlsson/minmux/openapi"
	"github.com/joakimcarlsson/minmux/outputcache"
	"github.com/joakimcarlsson/minmux/outputcache/inmemory"
	"github.com/joakimcarlsson/minmux/router"
)

// defaultCacheDuration is how long a cached response lives when its route
// opted in to output caching without naming a duration.
const defaultCacheDuration = time.Minute

// Config is how the server is set up: where it listens and what build it
// says it is. It is what the process was started with, and none of it
// changes while it runs.
type Config struct {
	// Addr is the address to listen on.
	Addr string
	// Version is the build this process is, reported by the health check
	// and in the API document.
	Version string
}

// Server owns the minmux router and the underlying http.Server.
type Server struct {
	addr    string
	version string
	router  *router.Router
	// docs generates the OpenAPI document from the registered routes.
	docs *openapi.Generator
	// cache holds the responses of the routes that opted in to output
	// caching, in process memory.
	cache *outputcache.Cache
}

// New constructs a Server from cfg with all routes registered.
func New(cfg Config) *Server {
	s := &Server{
		addr:    cfg.Addr,
		version: cfg.Version,
		router:  router.New(),
	}
	s.docs = openapi.NewGenerator(openapi.Info{
		Title:   "Flight Club",
		Version: cfg.Version,
	})
	s.cache = outputcache.New(s.router, outputcache.Config{
		Storage:         inmemory.New(),
		DefaultDuration: defaultCacheDuration,
	})
	s.router.Use(router.Recover())
	s.router.Use(s.cache.Middleware())
	s.routes()
	return s
}

// routes registers every route on the router.
func (s *Server) routes() {
	s.registerHealth()
	s.registerDocs()
}

// Start runs the server until ctx is cancelled, then drains gracefully and
// stops the output cache's cleanup. It
// returns the error that stopped the server from listening, or the error
// from a shutdown that did not finish within config.ShutdownTimeout.
func (s *Server) Start(ctx context.Context) error {
	defer s.cache.Close()

	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.router,
		ReadHeaderTimeout: config.ReadHeaderTimeout,
	}

	errc := make(chan error, 1)
	go func() {
		slog.InfoContext(ctx, "api listening", "addr", s.addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(
			context.Background(),
			config.ShutdownTimeout,
		)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}

// Handler exposes the router for tests via httptest.
func (s *Server) Handler() http.Handler {
	return s.router
}
