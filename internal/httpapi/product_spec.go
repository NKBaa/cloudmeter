package httpapi

import (
	"fmt"
	"math"
	"strings"
)

const defaultDataPolicy = "volume_compatible"

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
	delete(spec, "portEditable")
	delete(spec, "portEnvVar")
	basePath, err := normalizedAbsolutePath(spec["basePath"], "route basePath", false)
	if err != nil {
		return err
	}
	if basePath == "" {
		basePath = "/"
	}
	spec["basePath"] = basePath
	delete(spec, "stripPrefix")
	if _, err = normalizedBoolean(spec, "websocket", true); err != nil {
		return err
	}
	if _, err = normalizedBoolean(spec, "sse", true); err != nil {
		return err
	}
	delete(spec, "cookiePath")
	// Optional direct host-port mapping capability. When available, users can
	// opt in per instance; the Worker then auto-assigns a free host port on
	// every deployment so direct access works alongside the platform gateway.
	mapping, ok := spec["portMapping"].(map[string]any)
	if ok {
		available, err := normalizedBoolean(mapping, "available", false)
		if err != nil {
			return err
		}
		spec["portMapping"] = map[string]any{"available": available}
	} else {
		delete(spec, "portMapping")
	}
	return nil
}

func normalizeHealthSpec(spec map[string]any) error {
	path, err := normalizedAbsolutePath(spec["path"], "health path", true)
	if err != nil {
		return err
	}
	spec["path"] = path
	acceptedStatusCodes, err := normalizedAcceptedStatusCodes(spec["acceptedStatusCodes"])
	if err != nil {
		return err
	}
	spec["acceptedStatusCodes"] = acceptedStatusCodes
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

func normalizedAcceptedStatusCodes(raw any) ([]int, error) {
	if raw == nil {
		return []int{}, nil
	}
	values, ok := raw.([]any)
	if !ok {
		if typed, typedOK := raw.([]int); typedOK {
			values = make([]any, len(typed))
			for index, value := range typed {
				values[index] = float64(value)
			}
		} else {
			return nil, fmt.Errorf("health acceptedStatusCodes must be an array")
		}
	}
	if len(values) > 32 {
		return nil, fmt.Errorf("health acceptedStatusCodes cannot contain more than 32 values")
	}
	result := make([]int, 0, len(values))
	seen := map[int]bool{}
	for _, rawValue := range values {
		value, integer := exactInteger(rawValue)
		if !integer || value < 100 || value > 599 {
			return nil, fmt.Errorf("health acceptedStatusCodes values must be integers from 100 to 599")
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
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

// routePortMappingAvailable reports whether the product version lets users opt
// in to direct host-port publishing.
func routePortMappingAvailable(routeSpec map[string]any) bool {
	mapping, ok := routeSpec["portMapping"].(map[string]any)
	if !ok {
		return false
	}
	available, _ := mapping["available"].(bool)
	return available
}
