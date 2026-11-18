package httpx

import (
	"net/http"

	"github.com/joakimcarlsson/minmux/openapi"
	"github.com/joakimcarlsson/minmux/router"
)

// healthResponse is the body of the health check.
type healthResponse struct {
	// Status is "ok" while the process is running.
	Status string `json:"status"`
	// Version is the build this process is.
	Version string `json:"version"`
}

// registerHealth registers the health check endpoint together with its
// OpenAPI description. It is never output cached: a probe has to reach the
// process every time.
func (s *Server) registerHealth() {
	s.router.Get(
		"/api/health",
		s.health,
		openapi.Tags("Health"),
		openapi.Summary("Check that the API is running"),
		openapi.ReturnsBody[healthResponse](
			http.StatusOK,
			"The API is running.",
		),
	)
}

// health handles GET /api/health: it answers 200 with the build version
// while the process is running.
func (s *Server) health(c *router.Context) {
	c.JSON(http.StatusOK, healthResponse{Status: "ok", Version: s.version})
}
