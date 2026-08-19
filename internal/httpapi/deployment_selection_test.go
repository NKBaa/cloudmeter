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

func TestSelectedRuntimeSpecUsesOneSharedVolumeCapacity(t *testing.T) {
	template := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "dataVolumeGiB": 10.0,
		"editableOptions": map[string]any{"dataVolume": true},
		"volumes":         []any{map[string]any{"name": "data", "mountPath": "/data", "sizeGiB": 10.0}, map[string]any{"name": "config", "mountPath": "/config", "sizeGiB": 10.0}},
	}
	capacity := 24.0
	selected, err := selectedRuntimeSpec(template, selectedResources{DataVolumeGiB: &capacity})
	if err != nil {
		t.Fatal(err)
	}
	if selected["dataVolumeGiB"] != capacity {
		t.Fatalf("capacity=%v", selected["dataVolumeGiB"])
	}
	volumes := selected["volumes"].([]any)
	for _, raw := range volumes {
		if raw.(map[string]any)["sizeGiB"] != capacity {
			t.Fatalf("volumes=%v", volumes)
		}
	}
}

func TestSelectedRuntimeSpecRejectsDisabledOption(t *testing.T) {
	template := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "editableOptions": map[string]any{"cpu": false}}
	cpu := 2.0
	if _, err := selectedRuntimeSpec(template, selectedResources{CPUCores: &cpu}); err == nil {
		t.Fatal("disabled CPU override accepted")
	}
}

func TestSelectedRuntimeSpecRejectsLegacyVolumeOverrideWhenDisabled(t *testing.T) {
	template := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "dataVolumeGiB": 10.0,
		"editableOptions": map[string]any{"dataVolume": false},
		"volumes":         []any{map[string]any{"name": "data", "mountPath": "/data", "sizeGiB": 10.0}},
	}
	if _, err := selectedRuntimeSpec(template, selectedResources{VolumeSizes: map[string]float64{"data": 20}}); err == nil {
		t.Fatal("legacy volume override bypassed the editable option")
	}
}
