package httpapi

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

const defaultDataPolicy = "volume_compatible"

var productPortEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func normalizeProductVersionSpecs(runtimeSpec, routeSpec, healthSpec, updateSpec map[string]any) error {
	if err := normalizeRouteSpec(routeSpec); err != nil {
		return err
	}
	if err := normalizeHealthSpec(healthSpec); err != nil {
		return err
	}
	policy, err := normalizeUpdateSpec(updateSpec)
	if err != nil {
		return err
	}
	if policy == "stateless" {
		if volumes, ok := runtimeSpec["volumes"].([]any); ok && len(volumes) > 0 {
			return fmt.Errorf("stateless versions cannot declare data volumes")
		}
	}
	return nil
}

func normalizeRouteSpec(spec map[string]any) error {
	port, ok := exactInteger(spec["containerPort"])
	if !ok || port < 1 || port > 65535 {
		return fmt.Errorf("route containerPort must be an integer from 1 to 65535")
	}
	spec["containerPort"] = float64(port)
	portEditable, err := normalizedBoolean(spec, "portEditable", false)
	if err != nil {
		return err
	}
	portEnvVar := ""
	if raw, exists := spec["portEnvVar"]; exists {
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("route portEnvVar must be a string")
		}
		portEnvVar = strings.TrimSpace(value)
	}
	if portEditable && portEnvVar != "" && !productPortEnvKeyPattern.MatchString(portEnvVar) {
		return fmt.Errorf("route portEnvVar must be a valid environment variable name")
	}
	if !portEditable {
		portEnvVar = ""
	}
	spec["portEditable"], spec["portEnvVar"] = portEditable, portEnvVar
	basePath, err := normalizedAbsolutePath(spec["basePath"], "route basePath", false)
	if err != nil {
		return err
	}
	if basePath == "" {
		basePath = "/"
	}
	spec["basePath"] = basePath
	stripPrefix, err := normalizedBoolean(spec, "stripPrefix", true)
	if err != nil {
		return err
	}
	if !stripPrefix && basePath != "/" {
		return fmt.Errorf("route basePath must be / when stripPrefix is disabled")
	}
	if _, err = normalizedBoolean(spec, "websocket", true); err != nil {
		return err
	}
	if _, err = normalizedBoolean(spec, "sse", true); err != nil {
		return err
	}
	cookiePath, err := normalizedAbsolutePath(spec["cookiePath"], "route cookiePath", true)
	if err != nil {
		return err
	}
	spec["cookiePath"] = cookiePath
	return nil
}

func normalizeHealthSpec(spec map[string]any) error {
	path, err := normalizedAbsolutePath(spec["path"], "health path", true)
	if err != nil {
		return err
	}
	spec["path"] = path
	for _, field := range []struct {
		name         string
		defaultValue int
		minimum      int
		maximum      int
	}{
		{"intervalSeconds", 5, 1, 120},
		{"timeoutSeconds", 5, 1, 30},
	} {
		value := field.defaultValue
		if raw, exists := spec[field.name]; exists {
			parsed, ok := exactInteger(raw)
			if !ok || parsed < field.minimum || parsed > field.maximum {
				return fmt.Errorf("health %s must be an integer from %d to %d", field.name, field.minimum, field.maximum)
			}
			value = parsed
		}
		spec[field.name] = float64(value)
	}
	return nil
}

func normalizeUpdateSpec(spec map[string]any) (string, error) {
	policy := defaultDataPolicy
	if raw, exists := spec["dataPolicy"]; exists {
		value, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("update dataPolicy must be a string")
		}
		policy = strings.TrimSpace(value)
	}
	switch policy {
	case "stateless", "volume_compatible", "backup_required":
	default:
		return "", fmt.Errorf("update dataPolicy must be stateless, volume_compatible, or backup_required")
	}
	spec["dataPolicy"] = policy
	return policy, nil
}

func normalizedAbsolutePath(raw any, label string, optional bool) (string, error) {
	if raw == nil {
		if optional {
			return "", nil
		}
		return "/", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be an absolute path", label)
	}
	value = strings.TrimSpace(value)
	if value == "" && optional {
		return "", nil
	}
	if value == "" || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \r\n\t?#") || strings.Contains(value, "..") || len(value) > 512 {
		return "", fmt.Errorf("%s must be an absolute path without whitespace, query, fragment, or parent traversal", label)
	}
	if value != "/" {
		value = strings.TrimRight(value, "/")
	}
	return value, nil
}

func normalizedBoolean(spec map[string]any, key string, fallback bool) (bool, error) {
	value := fallback
	if raw, exists := spec[key]; exists {
		parsed, ok := raw.(bool)
		if !ok {
			return false, fmt.Errorf("route %s must be a boolean", key)
		}
		value = parsed
	}
	spec[key] = value
	return value, nil
}

func exactInteger(raw any) (int, bool) {
	value, ok := raw.(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) {
		return 0, false
	}
	return int(value), true
}

func snapshotDataPolicy(snapshot map[string]any) string {
	if updateSpec, ok := snapshot["update_spec"].(map[string]any); ok {
		if policy, ok := updateSpec["dataPolicy"].(string); ok {
			return policy
		}
	}
	return defaultDataPolicy
}
