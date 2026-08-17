package httpapi

import (
	"strings"
	"testing"
)

func TestNormalizeProductDetails(t *testing.T) {
	slug, name, err := normalizeProductDetails("  Model-API  ", "  Model API  ")
	if err != nil {
		t.Fatal(err)
	}
	if slug != "model-api" || name != "Model API" {
		t.Fatalf("normalized product = %q, %q", slug, name)
	}

	for _, invalid := range []string{"-model", "model_api", "model/api", strings.Repeat("a", 64)} {
		if _, _, err = normalizeProductDetails(invalid, "Model"); err == nil {
			t.Fatalf("invalid product slug %q was accepted", invalid)
		}
	}
	if _, _, err = normalizeProductDetails("model", strings.Repeat("产", maxProductNameCharacters+1)); err == nil {
		t.Fatal("overlong Unicode product name was accepted")
	}
}

func TestRestoredProductStatus(t *testing.T) {
	tests := []struct {
		published bool
		tested    bool
		want      string
	}{
		{published: true, tested: true, want: "published"},
		{tested: true, want: "testing"},
		{want: "draft"},
	}
	for _, test := range tests {
		if got := restoredProductStatus(test.published, test.tested); got != test.want {
			t.Errorf("restoredProductStatus(%v,%v)=%q, want %q", test.published, test.tested, got, test.want)
		}
	}
}
