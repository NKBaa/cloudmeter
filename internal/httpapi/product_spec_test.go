package httpapi

import "testing"

func TestNormalizeProductVersionSpecs(t *testing.T) {
	runtimeSpec := map[string]any{"volumes": []any{map[string]any{"name": "data"}}}
	routeSpec := map[string]any{"containerPort": 8080.0, "basePath": "/ui/"}
	healthSpec := map[string]any{"path": "/health"}
	updateSpec := map[string]any{"dataPolicy": "backup_required"}
	if err := normalizeProductVersionSpecs(runtimeSpec, routeSpec, healthSpec, updateSpec); err != nil {
		t.Fatal(err)
	}
	if routeSpec["basePath"] != "/ui" || routeSpec["stripPrefix"] != true || routeSpec["websocket"] != true || routeSpec["sse"] != true {
		t.Fatalf("routeSpec=%v", routeSpec)
	}
	if healthSpec["intervalSeconds"] != 5.0 || updateSpec["dataPolicy"] != "backup_required" {
		t.Fatalf("health=%v update=%v", healthSpec, updateSpec)
	}
}

func TestNormalizeRouteSpecPortConfiguration(t *testing.T) {
	spec := map[string]any{"containerPort": 8080.0, "portEditable": true, "portEnvVar": "SERVER_PORT"}
	if err := normalizeRouteSpec(spec); err != nil {
		t.Fatal(err)
	}
	if _, exists := spec["portEditable"]; exists {
		t.Fatalf("legacy port override retained: %v", spec)
	}
	if _, exists := spec["portEnvVar"]; exists {
		t.Fatalf("legacy port environment variable retained: %v", spec)
	}
}

func TestNormalizeProductVersionSpecsRejectsInvalidCombinations(t *testing.T) {
	tests := []struct {
		name    string
		runtime map[string]any
		route   map[string]any
		health  map[string]any
		update  map[string]any
	}{
		{"invalid port", map[string]any{}, map[string]any{"containerPort": 0.0}, map[string]any{}, map[string]any{}},
		{"base path without stripping", map[string]any{}, map[string]any{"containerPort": 80.0, "basePath": "/ui", "stripPrefix": false}, map[string]any{}, map[string]any{}},
		{"stateless volume", map[string]any{"volumes": []any{map[string]any{"name": "data"}}}, map[string]any{"containerPort": 80.0}, map[string]any{}, map[string]any{"dataPolicy": "stateless"}},
		{"health traversal", map[string]any{}, map[string]any{"containerPort": 80.0}, map[string]any{"path": "/../health"}, map[string]any{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := normalizeProductVersionSpecs(test.runtime, test.route, test.health, test.update); err == nil {
				t.Fatal("invalid product version specs accepted")
			}
		})
	}
}
