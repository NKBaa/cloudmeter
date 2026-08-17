package httpapi

import "testing"

func TestSelectedRouteSpecKeepsInternalPortImmutable(t *testing.T) {
	fixed := map[string]any{"containerPort": 8080.0}
	selected, err := selectedRouteSpec(fixed, selectedResources{})
	if err != nil {
		t.Fatal(err)
	}
	if selected["containerPort"] != 8080.0 {
		t.Fatalf("selected=%v", selected)
	}
	runtime := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0}
	if err = validateInternalRoutePort(runtime, selected); err != nil {
		t.Fatal(err)
	}
}
