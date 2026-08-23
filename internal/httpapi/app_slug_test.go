package httpapi

import "testing"

func TestNextAppSlugCandidate(t *testing.T) {
	cases := []struct {
		base    string
		attempt int
		want    string
	}{
		{"sillytavern", 0, "sillytavern"},
		{"sillytavern", 1, "sillytavern-2"},
		{"sillytavern", 2, "sillytavern-3"},
		{"sillytavern", 10, "sillytavern-11"},
		{"a", 1, "a-2"},
		{"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz", 1, "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghi-2"},
		{"toolong-", 3, "toolong-4"},
	}
	for _, c := range cases {
		got := nextAppSlugCandidate(c.base, c.attempt)
		if got != c.want {
			t.Fatalf("nextAppSlugCandidate(%q,%d)=%q want %q", c.base, c.attempt, got, c.want)
		}
		if len(got) > 63 {
			t.Fatalf("nextAppSlugCandidate(%q,%d)=%q exceeds 63 chars", c.base, c.attempt, got)
		}
	}
}
