package httpapi

func snapshotRuntime(snapshot map[string]any) map[string]any {
	if value, ok := snapshot["runtime_spec"].(map[string]any); ok {
		return value
	}
	return map[string]any{}
}
