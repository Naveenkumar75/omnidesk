package httpapi

import (
	_ "embed"
	"net/http"
)

// openAPISpec is the hand-written contract for this API. TestOpenAPICoversRoutes
// fails if a route is added to the router without being documented here.
//
//go:embed openapi.yaml
var openAPISpec []byte

func serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(openAPISpec)
}
