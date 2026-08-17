package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
)

var digestPattern = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9./:_-]*@sha256:[0-9a-f]{64}$")
var volumeKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var environmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
var secretKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)
var dependencyKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var serviceSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type VolumeMount struct {
	Key       string  `json:"name"`
	MountPath string  `json:"mountPath"`
	SizeGiB   float64 `json:"sizeGiB"`
}

type Dependency struct {
	Key         string `json:"key"`
	ProductID   string `json:"productId"`
	ServiceSlug string `json:"serviceSlug"`
	Required    bool   `json:"required"`
}

const (
	DefaultRuntimeOwner  = "cloudmeter"
	DefaultBackupVolume  = "cloudmeter_backup_data"
	DefaultCPUCores      = 1.0
	DefaultMemoryMiB     = 512.0
	MinCPUCores          = 0.1
	MaxCPUCores          = 64.0
	MinMemoryMiB         = 64.0
	MaxMemoryMiB         = 262144.0
	DefaultSystemDiskGiB = 5.0
	DefaultVolumeGiB     = 10.0
	MinSystemDiskGiB     = 1.0
	MaxSystemDiskGiB     = 1024.0
	MinVolumeGiB         = 1.0
	MaxVolumeGiB         = 16384.0
)

type Resources struct {
	CPUCores  float64
	MemoryMiB float64
}

type StorageResources struct {
	SystemDiskGiB float64
	DataDiskGiB   float64
}

func ValidateImageDigest(value string) error {
	if !digestPattern.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("image must be pinned to name@sha256:<64 hex>")
	}
	return nil
}

func ValidateRuntimeSpec(spec map[string]any) error {
	if _, err := RuntimeResources(spec, true); err != nil {
		return err
	}
	if truthy(spec["privileged"]) {
		return fmt.Errorf("privileged containers are not allowed")
	}
	if value, ok := spec["networkMode"].(string); ok && strings.EqualFold(value, "host") {
		return fmt.Errorf("host network is not allowed")
	}
	if truthy(spec["hostNetwork"]) {
		return fmt.Errorf("host network is not allowed")
	}
	if truthy(spec["dockerSocket"]) {
		return fmt.Errorf("docker socket access is not allowed")
	}
	if _, err := RuntimeStorage(spec, true); err != nil {
		return err
	}
	if _, err := RuntimeCommand(spec); err != nil {
		return err
	}
	if raw, exists := spec["env"]; exists {
		values, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("env must be an object")
		}
		if len(values) > 128 {
			return fmt.Errorf("env may contain at most 128 variables")
		}
		total := 0
		for key, rawValue := range values {
			if !environmentKeyPattern.MatchString(key) {
				return fmt.Errorf("environment variable name %q is invalid", key)
			}
			value, ok := rawValue.(string)
			if !ok {
				return fmt.Errorf("environment variable %q must be a string", key)
			}
			if len(value) > 4096 {
				return fmt.Errorf("environment variable %q is too long", key)
			}
			total += len(key) + len(value)
		}
		if total > 32768 {
			return fmt.Errorf("environment variables exceed the 32 KiB limit")
		}
	}
	if raw, exists := spec["editableEnvKeys"]; exists {
		values, ok := raw.([]any)
		if !ok { return fmt.Errorf("editableEnvKeys must be an array") }
		if len(values) > 128 { return fmt.Errorf("editableEnvKeys may contain at most 128 entries") }
		seen := map[string]bool{}
		environment, _ := spec["env"].(map[string]any)
		for _, rawKey := range values {
			key, ok := rawKey.(string)
			if !ok || !environmentKeyPattern.MatchString(key) { return fmt.Errorf("editable environment variable name is invalid") }
			if seen[key] { return fmt.Errorf("editable environment variable %q is duplicated", key) }
			if _, exists := environment[key]; !exists { return fmt.Errorf("editable environment variable %q has no default value", key) }
			seen[key] = true
		}
	}
	secretKeys, err := RuntimeSecretKeys(spec)
	if err != nil {
		return err
	}
	if environment, ok := spec["env"].(map[string]any); ok {
		for _, key := range secretKeys {
			if _, exists := environment[key]; exists {
				return fmt.Errorf("%s cannot be both an environment value and a secret", key)
			}
		}
	}
	if raw, exists := spec["volumes"]; exists {
		value, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("volumes must be an array")
		}
		seenKeys := map[string]bool{}
		seenPaths := map[string]bool{}
		for _, item := range value {
			if text, ok := item.(string); ok && (strings.HasPrefix(text, "/") || strings.Contains(text, ":\\")) {
				return fmt.Errorf("host paths are not allowed")
			}
			entry, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("volumes must use name and mountPath")
			}
			key, _ := entry["name"].(string)
			mountPath, _ := entry["mountPath"].(string)
			if !volumeKeyPattern.MatchString(key) {
				return fmt.Errorf("volume name is invalid")
			}
			if !strings.HasPrefix(mountPath, "/") || mountPath == "/" || strings.Contains(mountPath, "..") {
				return fmt.Errorf("volume mountPath is invalid")
			}
			if seenKeys[key] || seenPaths[mountPath] {
				return fmt.Errorf("volume names and mount paths must be unique")
			}
			seenKeys[key], seenPaths[mountPath] = true, true
		}
	}
	if _, err := RuntimeDependencies(spec); err != nil {
		return err
	}
	return nil
}

func RuntimeDependencies(spec map[string]any) ([]Dependency, error) {
	raw, exists := spec["dependencies"]
	if !exists {
		return nil, nil
	}
	if raw == nil {
		return nil, fmt.Errorf("dependencies must be an array")
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("dependencies must be an array")
	}
	if len(values) > 32 {
		return nil, fmt.Errorf("dependencies may contain at most 32 entries")
	}
	dependencies := make([]Dependency, 0, len(values))
	keys, services := map[string]bool{}, map[string]bool{}
	for _, rawValue := range values {
		entry, ok := rawValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("dependencies must use key, productId, serviceSlug and required")
		}
		if len(entry) != 4 {
			return nil, fmt.Errorf("dependencies may only contain key, productId, serviceSlug and required")
		}
		key, keyOK := entry["key"].(string)
		productID, productOK := entry["productId"].(string)
		serviceSlug, serviceOK := entry["serviceSlug"].(string)
		required, requiredOK := entry["required"].(bool)
		if !keyOK || !dependencyKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("dependency key %q is invalid", key)
		}
		if !productOK || !uuidPattern.MatchString(productID) {
			return nil, fmt.Errorf("dependency %q productId must be a UUID", key)
		}
		if !serviceOK || !serviceSlugPattern.MatchString(serviceSlug) {
			return nil, fmt.Errorf("dependency %q serviceSlug is invalid", key)
		}
		if !requiredOK {
			return nil, fmt.Errorf("dependency %q required must be a boolean", key)
		}
		if keys[key] {
			return nil, fmt.Errorf("dependency key %q is duplicated", key)
		}
		if services[serviceSlug] {
			return nil, fmt.Errorf("dependency serviceSlug %q is duplicated", serviceSlug)
		}
		keys[key], services[serviceSlug] = true, true
		dependencies = append(dependencies, Dependency{Key: key, ProductID: productID, ServiceSlug: serviceSlug, Required: required})
	}
	return dependencies, nil
}

func RuntimeSecretKeys(spec map[string]any) ([]string, error) {
	raw, exists := spec["secretKeys"]
	if !exists || raw == nil {
		return nil, nil
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("secretKeys must be an array")
	}
	if len(values) > 64 {
		return nil, fmt.Errorf("secretKeys may contain at most 64 entries")
	}
	keys := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, rawValue := range values {
		key, ok := rawValue.(string)
		if !ok || !secretKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("secret keys must use uppercase letters, digits, and underscores")
		}
		if seen[key] {
			return nil, fmt.Errorf("secret key %q is duplicated", key)
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys, nil
}

func RuntimeCommand(spec map[string]any) ([]string, error) {
	raw, exists := spec["command"]
	if !exists || raw == nil {
		return nil, nil
	}
	values, ok := raw.([]any)
	if !ok {
		if strings, stringsOK := raw.([]string); stringsOK {
			if len(strings) == 0 {
				return nil, nil
			}
			values = make([]any, len(strings))
			for i := range strings {
				values[i] = strings[i]
			}
		} else {
			return nil, fmt.Errorf("command must be an array of arguments")
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > 64 {
		return nil, fmt.Errorf("command may contain at most 64 arguments")
	}
	command := make([]string, 0, len(values))
	total := 0
	for _, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("command arguments must be non-empty strings")
		}
		if len(value) > 4096 {
			return nil, fmt.Errorf("command argument is too long")
		}
		total += len(value)
		command = append(command, value)
	}
	if total > 16384 {
		return nil, fmt.Errorf("command exceeds the 16 KiB limit")
	}
	return command, nil
}

func RuntimeStorage(spec map[string]any, required bool) (StorageResources, error) {
	systemDisk, systemOK := numeric(spec["systemDiskGiB"])
	if required && !systemOK {
		return StorageResources{}, fmt.Errorf("systemDiskGiB is required")
	}
	if !systemOK {
		systemDisk = DefaultSystemDiskGiB
	}
	if math.IsNaN(systemDisk) || math.IsInf(systemDisk, 0) || systemDisk < MinSystemDiskGiB || systemDisk > MaxSystemDiskGiB {
		return StorageResources{}, fmt.Errorf("systemDiskGiB must be between %.0f and %.0f", MinSystemDiskGiB, MaxSystemDiskGiB)
	}
	dataDisk := 0.0
	if values, ok := spec["volumes"].([]any); ok {
		for _, item := range values {
			entry, ok := item.(map[string]any)
			if !ok {
				return StorageResources{}, fmt.Errorf("volumes must use name, mountPath and sizeGiB")
			}
			size, sizeOK := numeric(entry["sizeGiB"])
			if required && !sizeOK {
				return StorageResources{}, fmt.Errorf("every volume must declare sizeGiB")
			}
			if !sizeOK {
				size = DefaultVolumeGiB
			}
			if math.IsNaN(size) || math.IsInf(size, 0) || size < MinVolumeGiB || size > MaxVolumeGiB {
				return StorageResources{}, fmt.Errorf("volume sizeGiB must be between %.0f and %.0f", MinVolumeGiB, MaxVolumeGiB)
			}
			dataDisk += size
		}
	}
	return StorageResources{SystemDiskGiB: systemDisk, DataDiskGiB: dataDisk}, nil
}

// RuntimeResources parses resource reservations. Legacy immutable release
// snapshots may omit them, so callers executing old releases can opt into
// bounded defaults while new product versions require explicit values.
func RuntimeResources(spec map[string]any, required bool) (Resources, error) {
	cpu, cpuOK := numeric(spec["cpuCores"])
	memory, memoryOK := numeric(spec["memoryMiB"])
	if required && (!cpuOK || !memoryOK) {
		return Resources{}, fmt.Errorf("cpuCores and memoryMiB are required")
	}
	if !cpuOK {
		cpu = DefaultCPUCores
	}
	if !memoryOK {
		memory = DefaultMemoryMiB
	}
	if math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu < MinCPUCores || cpu > MaxCPUCores {
		return Resources{}, fmt.Errorf("cpuCores must be between %.1f and %.0f", MinCPUCores, MaxCPUCores)
	}
	if math.IsNaN(memory) || math.IsInf(memory, 0) || memory < MinMemoryMiB || memory > MaxMemoryMiB {
		return Resources{}, fmt.Errorf("memoryMiB must be between %.0f and %.0f", MinMemoryMiB, MaxMemoryMiB)
	}
	return Resources{CPUCores: cpu, MemoryMiB: memory}, nil
}

func VolumeMounts(spec map[string]any) []VolumeMount {
	result := []VolumeMount{}
	values, ok := spec["volumes"].([]any)
	if !ok {
		return result
	}
	for _, item := range values {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key, _ := entry["name"].(string)
		path, _ := entry["mountPath"].(string)
		size, ok := numeric(entry["sizeGiB"])
		if !ok {
			size = DefaultVolumeGiB
		}
		if key != "" && path != "" {
			result = append(result, VolumeMount{Key: key, MountPath: path, SizeGiB: size})
		}
	}
	return result
}

func AppVolumeName(appID, key string) string {
	compact := strings.ReplaceAll(appID, "-", "")
	if len(compact) > 20 {
		compact = compact[:20]
	}
	return "cmv-" + compact + "-" + key
}

// ResourceScopeToken gives every non-production Compose project a short,
// stable Engine namespace without exposing the project name in resource names.
func ResourceScopeToken(owner string) string {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = DefaultRuntimeOwner
	}
	sum := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(sum[:5])
}

// UsesLegacyResourceNames preserves the production volume and network names
// that predate owner scoping. A blank owner is treated as production so small
// in-process callers keep the same conservative defaults.
func UsesLegacyResourceNames(owner string) bool {
	owner = strings.TrimSpace(owner)
	return owner == "" || strings.EqualFold(owner, DefaultRuntimeOwner)
}

func UserNetworkName(owner, userID string) string {
	if UsesLegacyResourceNames(owner) {
		return "user_net_" + userID
	}
	return "user_net_" + ResourceScopeToken(owner) + "-" + userID
}

// AppVolumeNameForOwner keeps production mounts compatible while preventing a
// verification or disaster-recovery stack from attaching a production volume
// when both databases contain the same application UUID.
func AppVolumeNameForOwner(owner, appID, key string) string {
	if UsesLegacyResourceNames(owner) {
		return AppVolumeName(appID, key)
	}
	compact := strings.ReplaceAll(appID, "-", "")
	if len(compact) > 20 {
		compact = compact[:20]
	}
	return "cmv-" + ResourceScopeToken(owner) + "-" + compact + "-" + key
}

// BackupVolumeName scopes the default backup store outside the production
// owner. Explicit custom names retain their base name but still gain the scope
// so two Compose projects cannot silently share backup archives.
func BackupVolumeName(owner, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		configured = DefaultBackupVolume
	}
	if UsesLegacyResourceNames(owner) {
		return configured
	}
	scope := ResourceScopeToken(owner)
	if configured == DefaultBackupVolume {
		return "cloudmeter_backup_" + scope
	}
	if strings.HasSuffix(configured, "-"+scope) || strings.HasSuffix(configured, "_"+scope) {
		return configured
	}
	return configured + "-" + scope
}

// HelperContainerName isolates short-lived backup and restore containers as
// well. The empty-owner form remains useful for narrowly scoped unit tests.
func HelperContainerName(owner, kind, jobID string) string {
	if strings.TrimSpace(owner) == "" {
		return "cm-" + kind + "-" + jobID
	}
	return "cm-" + kind + "-" + ResourceScopeToken(owner) + "-" + jobID
}

func truthy(value any) bool { b, ok := value.(bool); return ok && b }
