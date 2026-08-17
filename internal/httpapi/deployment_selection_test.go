package httpapi

import "testing"

func TestSelectedRouteSpecRequiresExplicitPortCapability(t *testing.T) {
	fixed := map[string]any{"containerPort": 8080.0}
	port := 9090
	if _, err := selectedRouteSpec(fixed, selectedResources{ContainerPort: &port}); err == nil {
		t.Fatal("fixed template accepted a port override")
	}
	editable := map[string]any{"containerPort": 8080.0, "portEditable": true, "portEnvVar": "SERVER_PORT"}
	selected, err := selectedRouteSpec(editable, selectedResources{ContainerPort: &port})
	if err != nil {
		t.Fatal(err)
	}
	if selected["containerPort"] != 9090.0 {
		t.Fatalf("selected=%v", selected)
	}
	runtime := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0}
	if err = bindRuntimePort(runtime, selected); err != nil {
		t.Fatal(err)
	}
	if runtime["env"].(map[string]any)["SERVER_PORT"] != "9090" {
		t.Fatalf("runtime=%v", runtime)
	}
}
