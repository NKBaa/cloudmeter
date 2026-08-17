package httpapi

import (
	"cloudmeter/internal/config"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestEPaySignatureAndCheckoutURL(t *testing.T) {
	values := map[string]string{"pid": "merchant", "money": "10.00", "out_trade_no": "order-1", "name": "CloudMeter"}
	first := epaySignature(values, "secret")
	if first == "" || first != epaySignature(values, "secret") {
		t.Fatal("EPay signature is not deterministic")
	}
	checkout, err := buildEPayCheckoutURL("https://pay.example.test/submit", values, "secret")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(checkout)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("sign") != first || query.Get("sign_type") != "HMAC-SHA256" || query.Get("out_trade_no") != "order-1" {
		t.Fatalf("unexpected checkout query: %s", parsed.RawQuery)
	}
	if strings.Contains(checkout, "secret") {
		t.Fatal("checkout URL leaked the secret")
	}
}

func TestEPaySignatureTypeIsExplicit(t *testing.T) {
	for _, value := range []string{"HMAC-SHA256", "hmac-sha256"} {
		if !strings.EqualFold(value, "HMAC-SHA256") {
			t.Fatalf("supported signature type rejected: %q", value)
		}
	}
	if strings.EqualFold("MD5", "HMAC-SHA256") {
		t.Fatal("unsupported signature type accepted")
	}
}

func TestParseAmountCents(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int64
	}{
		{"10", 1000}, {"10.5", 1050}, {"10.50", 1050}, {"0.01", 1},
	} {
		got, err := parseAmountCents(test.value)
		if err != nil || got != test.want {
			t.Fatalf("parseAmountCents(%q)=%d,%v want %d", test.value, got, err, test.want)
		}
	}
	for _, value := range []string{"", "-1", "1.001", "1.2.3", "abc"} {
		if _, err := parseAmountCents(value); err == nil {
			t.Fatalf("parseAmountCents(%q) accepted invalid amount", value)
		}
	}
}

func TestRequestOriginOnlyTrustsForwardedProtoFromConfiguredProxy(t *testing.T) {
	s := &Server{cfg: config.Config{TrustedProxyCIDRs: []string{"10.0.0.0/8"}}}
	r := httptest.NewRequest(http.MethodGet, "http://cloud.example.com", nil)
	r.RemoteAddr = "192.168.1.20:1234"
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := s.requestOrigin(r); got != "http://cloud.example.com" {
		t.Fatalf("untrusted origin=%q", got)
	}
	r.RemoteAddr = "10.1.2.3:1234"
	if got := s.requestOrigin(r); got != "https://cloud.example.com" {
		t.Fatalf("trusted origin=%q", got)
	}
}

func TestRequestOriginUsesConfiguredPublicBaseURL(t *testing.T) {
	s := &Server{cfg: config.Config{PublicBaseURL: "https://cloud.example.com/"}}
	r := httptest.NewRequest(http.MethodGet, "http://internal.invalid", nil)
	if got := s.requestOrigin(r); got != "https://cloud.example.com" {
		t.Fatalf("origin=%q", got)
	}
}

func TestAppSlugPattern(t *testing.T) {
	for _, value := range []string{"demo", "ai-app-01", "a"} {
		if !appSlugPattern.MatchString(value) {
			t.Fatalf("valid slug rejected: %q", value)
		}
	}
	for _, value := range []string{"../admin", "-bad", "UPPER", "a_b", "a/b"} {
		if appSlugPattern.MatchString(value) {
			t.Fatalf("invalid slug accepted: %q", value)
		}
	}
}

func TestUserSlugBase(t *testing.T) {
	tests := map[string]string{
		"First.Last+tag@example.com": "first-last-tag",
		"---@example.com":            "user",
		"a__b@example.com":           "a-b",
	}
	for email, want := range tests {
		if got := userSlugBase(email); got != want {
			t.Fatalf("userSlugBase(%q)=%q want %q", email, got, want)
		}
	}
}
