package server

import (
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/thanhenti/bepilot/docs"
	"github.com/thanhenti/bepilot/internal/api"
	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/store"
)

// docInfraPaths are routes that intentionally have no OpenAPI annotation
// (the docs plumbing itself).
var docInfraPaths = map[string]bool{
	"GET /openapi.yaml": true,
	"GET /docs":         true,
	"GET /swagger/*any": true,
}

// TestRoutesMatchOpenAPISpec guards against adding or renaming an HTTP endpoint
// without adding swaggo annotations (and running `make swag`). If this fails,
// annotate the handler and regenerate.
func TestRoutesMatchOpenAPISpec(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	// Handlers is only needed so route registration can reference it; the
	// handlers themselves are never invoked here.
	h := &api.Handlers{Store: &store.Store{}}
	hz := New(config.HTTP{Addr: "127.0.0.1:0"}, h, log)

	registered := map[string]bool{}
	for _, r := range hz.Routes() {
		if r.Method == "OPTIONS" || r.Method == "HEAD" {
			continue
		}
		key := r.Method + " " + hertzToOpenAPIPath(r.Path)
		if docInfraPaths[key] {
			continue
		}
		registered[key] = true
	}

	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(docs.SwaggerYAML, &spec); err != nil {
		t.Fatalf("parse generated swagger.yaml: %v", err)
	}
	documented := map[string]bool{}
	for path, methods := range spec.Paths {
		for method := range methods {
			m := strings.ToUpper(method)
			switch m {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
				documented[m+" "+path] = true
			}
		}
	}

	for key := range registered {
		if !documented[key] {
			t.Errorf("route %q is registered but missing from the generated spec (annotate the handler + run make swag)", key)
		}
	}
	for key := range documented {
		if !registered[key] {
			t.Errorf("path %q is in the generated spec but not registered as a route", key)
		}
	}
	if t.Failed() {
		t.Logf("registered: %s", sortedKeys(registered))
		t.Logf("documented: %s", sortedKeys(documented))
	}
}

var hertzParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

func hertzToOpenAPIPath(p string) string {
	return hertzParam.ReplaceAllString(p, `{$1}`)
}

func sortedKeys(m map[string]bool) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}
