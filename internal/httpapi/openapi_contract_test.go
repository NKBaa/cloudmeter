package httpapi

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestOpenAPICoversRegisteredRoutes(t *testing.T) {
	serverSource, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	routePattern := regexp.MustCompile(`s\.mux\.Handle(?:Func)?\(\"([A-Z]+) (/api/[^\" ]+)`)
	for _, match := range routePattern.FindAllStringSubmatch(string(serverSource), -1) {
		method := strings.ToLower(match[1]) + ":"
		path := strings.TrimPrefix(match[2], "/api")
		pathMarker := "  " + path + ":"
		start := strings.Index(string(contract), pathMarker)
		if start < 0 {
			t.Errorf("OpenAPI is missing path %s", path)
			continue
		}
		rest := string(contract)[start+len(pathMarker):]
		if end := strings.Index(rest, "\n  /"); end >= 0 {
			rest = rest[:end]
		}
		if !strings.Contains(rest, "\n    "+method) {
			t.Errorf("OpenAPI path %s is missing method %s", path, match[1])
		}
	}
}
