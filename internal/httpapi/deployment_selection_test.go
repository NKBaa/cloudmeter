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

func TestRuntimeSpecForUpdateKeepsOmittedEditableValues(t *testing.T) {
	template := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "dataVolumeGiB": 10.0,
		"editableOptions": map[string]any{"cpu": true, "memory": true, "dataVolume": true, "command": true, "environment": true},
		"command":         []any{"new-default"},
		"env":             map[string]any{"EDITABLE": "new-default", "FIXED": "new-fixed"},
		"editableEnvKeys": []any{"EDITABLE"},
		"volumes":         []any{map[string]any{"name": "data", "mountPath": "/data", "sizeGiB": 10.0}},
	}
	current := map[string]any{
		"cpuCores": 2.0, "memoryMiB": 1024.0, "systemDiskGiB": 5.0, "dataVolumeGiB": 20.0,
		"command": []any{"old-command", "--serve"},
		"env":     map[string]any{"EDITABLE": "kept", "FIXED": "old-fixed"},
		"volumes": []any{map[string]any{"name": "data", "mountPath": "/data", "sizeGiB": 20.0}},
	}

	updated, err := runtimeSpecForUpdate(template, current, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated["cpuCores"] != 2.0 || updated["memoryMiB"] != 1024.0 || updated["dataVolumeGiB"] != 20.0 {
		t.Fatalf("editable resources were not preserved: %v", updated)
	}
	command := updated["command"].([]string)
	if len(command) != 2 || command[0] != "old-command" || command[1] != "--serve" {
		t.Fatalf("command=%v", command)
	}
	environment := updated["env"].(map[string]any)
	if environment["EDITABLE"] != "kept" || environment["FIXED"] != "new-fixed" {
		t.Fatalf("environment=%v", environment)
	}
	volumes := updated["volumes"].([]any)
	if volumes[0].(map[string]any)["sizeGiB"] != 20.0 {
		t.Fatalf("volumes=%v", volumes)
	}
}

func TestRuntimeSpecForUpdateAppliesOnlyEditableValues(t *testing.T) {
	template := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0,
		"editableOptions": map[string]any{"cpu": true, "memory": false},
	}
	current := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0}
	cpu, memory := 2.0, 1024.0
	if _, err := runtimeSpecForUpdate(template, current, &selectedResources{CPUCores: &cpu, MemoryMiB: &memory}); err == nil {
		t.Fatal("fixed memory override was accepted")
	}
	updated, err := runtimeSpecForUpdate(template, current, &selectedResources{CPUCores: &cpu})
	if err != nil {
		t.Fatal(err)
	}
	if updated["cpuCores"] != 2.0 || updated["memoryMiB"] != 512.0 {
		t.Fatalf("resources=%v", updated)
	}
}

func TestRuntimeSpecForUpdateRejectsVolumeShrinkOrRemoval(t *testing.T) {
	current := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "dataVolumeGiB": 20.0,
		"volumes": []any{map[string]any{"name": "data", "mountPath": "/data", "sizeGiB": 20.0}},
	}
	editableTemplate := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "dataVolumeGiB": 10.0,
		"editableOptions": map[string]any{"dataVolume": true},
		"volumes":         []any{map[string]any{"name": "data", "mountPath": "/data", "sizeGiB": 10.0}},
	}
	shrink := 15.0
	if _, err := runtimeSpecForUpdate(editableTemplate, current, &selectedResources{DataVolumeGiB: &shrink}); err == nil {
		t.Fatal("shared data volume shrink was accepted")
	}
	fixedTemplate := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "dataVolumeGiB": 10.0,
		"editableOptions": map[string]any{"dataVolume": false},
		"volumes":         []any{map[string]any{"name": "data", "mountPath": "/data", "sizeGiB": 10.0}},
	}
	if _, err := runtimeSpecForUpdate(fixedTemplate, current, nil); err == nil {
		t.Fatal("fixed template silently shrank the current shared data volume")
	}
	volumeLessTemplate := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0}
	if _, err := runtimeSpecForUpdate(volumeLessTemplate, current, nil); err == nil {
		t.Fatal("template silently removed the current shared data volume")
	}
}
