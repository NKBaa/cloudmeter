package httpapi

import "testing"

func TestNormalizeProductVersionSpecs(t *testing.T) {
	runtimeSpec := map[string]any{"volumes": []any{map[string]any{"name": "data"}}}
	routeSpec := map[string]any{"containerPort": 8080.0, "basePath": "/ui/"}
	healthSpec := map[string]any{"path": "/health", "acceptedStatusCodes": []any{401.0, 401.0, 403.0}}
	updateSpec := map[string]any{"dataPolicy": "backup_required"}
	if err := normalizeProductVersionSpecs(runtimeSpec, routeSpec, healthSpec, updateSpec); err != nil {
		t.Fatal(err)
	}
	if routeSpec["basePath"] != "/ui" || routeSpec["websocket"] != true || routeSpec["sse"] != true {
		t.Fatalf("routeSpec=%v", routeSpec)
	}
	if healthSpec["intervalSeconds"] != 5.0 || updateSpec["dataPolicy"] != "backup_required" {
		t.Fatalf("health=%v update=%v", healthSpec, updateSpec)
	}
	accepted, ok := healthSpec["acceptedStatusCodes"].([]int)
	if !ok || len(accepted) != 2 || accepted[0] != 401 || accepted[1] != 403 {
		t.Fatalf("acceptedStatusCodes=%#v", healthSpec["acceptedStatusCodes"])
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

func TestNormalizeRouteSpecRejectsDuplicateListenerPorts(t *testing.T) {
	spec := map[string]any{"containerPort": 8080.0, "listeners": []any{
		map[string]any{"key": "web", "containerPort": 8080.0, "primary": true},
		map[string]any{"key": "api", "containerPort": 8080.0},
	}}
	if err := normalizeRouteSpec(spec); err == nil {
		t.Fatal("duplicate listener port accepted")
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
		{"stateless volume", map[string]any{"volumes": []any{map[string]any{"name": "data"}}}, map[string]any{"containerPort": 80.0}, map[string]any{}, map[string]any{"dataPolicy": "stateless"}},
		{"health traversal", map[string]any{}, map[string]any{"containerPort": 80.0}, map[string]any{"path": "/../health"}, map[string]any{}},
		{"health statuses not array", map[string]any{}, map[string]any{"containerPort": 80.0}, map[string]any{"acceptedStatusCodes": "401"}, map[string]any{}},
		{"health status out of range", map[string]any{}, map[string]any{"containerPort": 80.0}, map[string]any{"acceptedStatusCodes": []any{99.0}}, map[string]any{}},
		{"health status not integer", map[string]any{}, map[string]any{"containerPort": 80.0}, map[string]any{"acceptedStatusCodes": []any{401.5}}, map[string]any{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := normalizeProductVersionSpecs(test.runtime, test.route, test.health, test.update); err == nil {
				t.Fatal("invalid product version specs accepted")
			}
		})
	}
}
