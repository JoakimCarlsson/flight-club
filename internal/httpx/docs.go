package httpx

import (
	"net/http"

	"github.com/joakimcarlsson/minmux/scalar"
)

// specPath is where the generated OpenAPI document is served.
const specPath = "/api/openapi.json"

// docsPath is where the Scalar UI over the OpenAPI document is served.
const docsPath = "/api/docs"

// registerDocs serves the generated OpenAPI document and the Scalar UI over
// it.
func (s *Server) registerDocs() {
	s.router.HandleFunc(
		http.MethodGet,
		specPath,
		s.docs.Handler(s.router),
	)
	s.router.HandleFunc(
		http.MethodGet,
		docsPath,
		scalar.Handler(specPath),
	)
}
