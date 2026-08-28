package httpapi

import "testing"

func TestNormalizeAppBaseDomain(t *testing.T) {
	tests := map[string]string{
		"":                         "",
		" Apps.Example.COM. ":      "apps.example.com",
		"tenant-apps.example.test": "tenant-apps.example.test",
	}
	for input, want := range tests {
		got, err := normalizeAppBaseDomain(input)
		if err != nil || got != want {
			t.Fatalf("normalizeAppBaseDomain(%q)=%q,%v want %q,nil", input, got, err, want)
		}
	}
	for _, input := range []string{"https://apps.example.com", "*.apps.example.com", "apps.example.com:443", "bad_label.example.com", "-bad.example.com"} {
		if _, err := normalizeAppBaseDomain(input); err == nil {
			t.Fatalf("normalizeAppBaseDomain(%q) unexpectedly succeeded", input)
		}
	}
}
