package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// Every route the router serves must be documented in openapi.yaml with the
// same method, and the spec must not document routes that do not exist.
func TestOpenAPICoversRoutes(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openAPISpec, &spec); err != nil {
		t.Fatalf("openapi.yaml does not parse: %v", err)
	}
	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			documented[strings.ToUpper(method)+" "+path] = true
		}
	}

	served := map[string]bool{}
	router := newAPI(t, nil).(chi.Routes)
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		served[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for r := range served {
		if !documented[r] {
			t.Errorf("route %s is served but not in openapi.yaml", r)
		}
	}
	for r := range documented {
		if !served[r] {
			t.Errorf("openapi.yaml documents %s but the router does not serve it", r)
		}
	}
}
