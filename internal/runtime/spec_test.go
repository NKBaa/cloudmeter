package runtime

import "testing"

func TestValidateImageDigest(t *testing.T) {
	valid := "ghcr.io/acme/model:latest@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := ValidateImageDigest(valid); err != nil {
		t.Fatal(err)
	}
	if err := ValidateImageDigest("nginx:latest"); err == nil {
		t.Fatal("floating tag accepted")
	}
}

func TestValidateRuntimeSpec(t *testing.T) {
	base := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0}
	if err := ValidateRuntimeSpec(map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "privileged": true}); err == nil {
		t.Fatal("privileged runtime accepted")
	}
	if err := ValidateRuntimeSpec(map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "networkMode": "host"}); err == nil {
		t.Fatal("host network accepted")
	}
	if err := ValidateRuntimeSpec(map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "volumes": []any{"/var/run/docker.sock:/sock"}}); err == nil {
		t.Fatal("host volume accepted")
	}
	if err := ValidateRuntimeSpec(map[string]any{}); err == nil {
		t.Fatal("missing resource limits accepted")
	}
	if err := ValidateRuntimeSpec(map[string]any{"cpuCores": 65.0, "memoryMiB": 512.0}); err == nil {
		t.Fatal("excessive CPU accepted")
	}
	if err := ValidateRuntimeSpec(base); err != nil {
		t.Fatal(err)
	}
}

func TestVolumeSpec(t *testing.T) {
	spec := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "volumes": []any{map[string]any{"name": "data", "mountPath": "/var/lib/app", "sizeGiB": 12.0}}}
	if err := ValidateRuntimeSpec(spec); err != nil {
		t.Fatal(err)
	}
	mounts := VolumeMounts(spec)
	if len(mounts) != 1 || mounts[0].Key != "data" || mounts[0].SizeGiB != 12 {
		t.Fatalf("mounts=%v", mounts)
	}
	if AppVolumeName("12345678-1234-1234-1234-123456789012", "data") != "cmv-12345678123412341234-data" {
		t.Fatal("unexpected volume name")
	}
}

func TestOwnerScopedRuntimeResourceNames(t *testing.T) {
	appID := "12345678-1234-1234-1234-123456789012"
	if got := ResourceScopeToken("cloudmeter-wsl-setup-verify"); got != "5185ef7528" {
		t.Fatalf("scope=%s", got)
	}
	if got := UserNetworkName("cloudmeter", "user-id"); got != "user_net_user-id" {
		t.Fatalf("production network=%s", got)
	}
	if got := UserNetworkName("cloudmeter-wsl-setup-verify", "user-id"); got != "user_net_5185ef7528-user-id" {
		t.Fatalf("scoped network=%s", got)
	}
	if got := AppVolumeNameForOwner("cloudmeter", appID, "data"); got != "cmv-12345678123412341234-data" {
		t.Fatalf("production volume=%s", got)
	}
	if got := AppVolumeNameForOwner("cloudmeter-wsl-setup-verify", appID, "data"); got != "cmv-5185ef7528-12345678123412341234-data" {
		t.Fatalf("scoped volume=%s", got)
	}
	if got := BackupVolumeName("cloudmeter", ""); got != "cloudmeter_backup_data" {
		t.Fatalf("production backup=%s", got)
	}
	if got := BackupVolumeName("cloudmeter-wsl-setup-verify", ""); got != "cloudmeter_backup_5185ef7528" {
		t.Fatalf("scoped backup=%s", got)
	}
	if got := BackupVolumeName("cloudmeter-wsl-setup-verify", "custom-backups"); got != "custom-backups-5185ef7528" {
		t.Fatalf("custom scoped backup=%s", got)
	}
	if got := HelperContainerName("cloudmeter-wsl-setup-verify", "backup", "job-id"); got != "cm-backup-5185ef7528-job-id" {
		t.Fatalf("scoped helper=%s", got)
	}
}

func TestStorageSpecRequiresDeclaredCapacity(t *testing.T) {
	if err := ValidateRuntimeSpec(map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0}); err == nil {
		t.Fatal("missing system disk capacity accepted")
	}
	if err := ValidateRuntimeSpec(map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "volumes": []any{map[string]any{"name": "data", "mountPath": "/data"}}}); err == nil {
		t.Fatal("missing data disk capacity accepted")
	}
}

func TestRuntimeCommandAndEnvironment(t *testing.T) {
	spec := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0,
		"command": []any{"python", "-m", "app"},
		"env":     map[string]any{"APP_MODE": "production"},
	}
	if err := ValidateRuntimeSpec(spec); err != nil {
		t.Fatal(err)
	}
	command, err := RuntimeCommand(spec)
	if err != nil || len(command) != 3 || command[2] != "app" {
		t.Fatalf("command=%v err=%v", command, err)
	}
	for name, value := range map[string]any{
		"invalid env":    map[string]any{"BAD-NAME": "x"},
		"non-string env": map[string]any{"PORT": 8080.0},
	} {
		invalid := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "env": value}
		if err := ValidateRuntimeSpec(invalid); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	invalidCommand := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "command": []any{"python", ""}}
	if err := ValidateRuntimeSpec(invalidCommand); err == nil {
		t.Fatal("empty command argument accepted")
	}
}

func TestRuntimeSecretKeys(t *testing.T) {
	spec := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0,
		"secretKeys": []any{"API_KEY", "MODEL_TOKEN"},
	}
	if err := ValidateRuntimeSpec(spec); err != nil {
		t.Fatal(err)
	}
	keys, err := RuntimeSecretKeys(spec)
	if err != nil || len(keys) != 2 || keys[1] != "MODEL_TOKEN" {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	for name, secretKeys := range map[string][]any{
		"lowercase": {"api_key"},
		"duplicate": {"API_KEY", "API_KEY"},
	} {
		invalid := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "secretKeys": secretKeys}
		if err := ValidateRuntimeSpec(invalid); err == nil {
			t.Fatalf("%s secret keys accepted", name)
		}
	}
	overlap := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "secretKeys": []any{"API_KEY"}, "env": map[string]any{"API_KEY": "plaintext"}}
	if err := ValidateRuntimeSpec(overlap); err == nil {
		t.Fatal("plaintext and secret key overlap accepted")
	}
}

func TestRuntimeDependencies(t *testing.T) {
	productID := "123e4567-e89b-12d3-a456-426614174000"
	spec := map[string]any{
		"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0,
		"dependencies": []any{map[string]any{"key": "model-api", "productId": productID, "serviceSlug": "ollama", "required": true}},
	}
	if err := ValidateRuntimeSpec(spec); err != nil {
		t.Fatal(err)
	}
	dependencies, err := RuntimeDependencies(spec)
	if err != nil || len(dependencies) != 1 || dependencies[0].ServiceSlug != "ollama" || !dependencies[0].Required {
		t.Fatalf("dependencies=%v err=%v", dependencies, err)
	}
	for name, dependencies := range map[string][]any{
		"invalid key":       {map[string]any{"key": "MODEL_API", "productId": productID, "serviceSlug": "ollama", "required": true}},
		"invalid product":   {map[string]any{"key": "model", "productId": "not-a-uuid", "serviceSlug": "ollama", "required": true}},
		"invalid service":   {map[string]any{"key": "model", "productId": productID, "serviceSlug": "Bad_Name", "required": true}},
		"missing required":  {map[string]any{"key": "model", "productId": productID, "serviceSlug": "ollama"}},
		"duplicate service": {map[string]any{"key": "first", "productId": productID, "serviceSlug": "ollama", "required": true}, map[string]any{"key": "second", "productId": productID, "serviceSlug": "ollama", "required": false}},
		"unknown field":     {map[string]any{"key": "model", "productId": productID, "serviceSlug": "ollama", "required": true, "url": "http://example.invalid"}},
	} {
		invalid := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "dependencies": dependencies}
		if err := ValidateRuntimeSpec(invalid); err == nil {
			t.Fatalf("%s dependencies accepted", name)
		}
	}
	nullDependencies := map[string]any{"cpuCores": 1.0, "memoryMiB": 512.0, "systemDiskGiB": 5.0, "dependencies": nil}
	if err := ValidateRuntimeSpec(nullDependencies); err == nil {
		t.Fatal("null dependencies accepted")
	}
}
