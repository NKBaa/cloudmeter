package httpapi

import "testing"

func TestValidateInitialSecrets(t *testing.T) {
	runtimeSpec := map[string]any{"secretKeys": []any{"API_KEY", "MODEL_TOKEN"}}
	if err := validateInitialSecrets(runtimeSpec, map[string]string{"API_KEY": "one", "MODEL_TOKEN": "two"}); err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string]map[string]string{
		"missing": {"API_KEY": "one"},
		"empty":   {"API_KEY": "one", "MODEL_TOKEN": ""},
		"unknown": {"API_KEY": "one", "MODEL_TOKEN": "two", "OTHER": "three"},
	} {
		if err := validateInitialSecrets(runtimeSpec, values); err == nil {
			t.Fatalf("%s secrets accepted", name)
		}
	}
}

func TestMissingSnapshotSecrets(t *testing.T) {
	snapshot := map[string]any{
		"runtime_spec":    map[string]any{"secretKeys": []any{"API_KEY", "MODEL_TOKEN"}},
		"secret_versions": map[string]any{"API_KEY": "version-id"},
	}
	missing, err := missingSnapshotSecrets(snapshot)
	if err != nil || len(missing) != 1 || missing[0] != "MODEL_TOKEN" {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
}
