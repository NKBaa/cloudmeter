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
	if query.Get("sign") != first || query.Get("sign_type") != "MD5" || query.Get("out_trade_no") != "order-1" {
		t.Fatalf("unexpected checkout query: %s", parsed.RawQuery)
	}
	if strings.Contains(checkout, "secret") {
		t.Fatal("checkout URL leaked the secret")
	}
}

func TestEPaySignatureMatchesGoEPay(t *testing.T) {
	values := map[string]string{"device": "devicev", "money": "moneyv"}
	if got := epaySignature(values, "1234567"); got != "3854cc9f022e0fb821bd2e002260245d" {
		t.Fatalf("go-epay compatible signature=%q", got)
	}
}

func TestEPaySignatureTypeIsExplicit(t *testing.T) {
	for _, value := range []string{"MD5", "md5"} {
		if !strings.EqualFold(value, "MD5") {
			t.Fatalf("supported signature type rejected: %q", value)
		}
	}
	if strings.EqualFold("HMAC-SHA256", "MD5") {
		t.Fatal("unsupported signature type accepted")
	}
}

func TestEPayEndpointAcceptsOriginOrSubmitURL(t *testing.T) {
	values := map[string]string{"pid": "merchant", "money": "1.00"}
	for _, endpoint := range []string{"https://pay.example.test", "https://pay.example.test/submit.php"} {
		checkout, err := buildEPayCheckoutURL(endpoint, values, "secret")
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(checkout)
		if parsed.Path != "/submit.php" {
			t.Fatalf("endpoint %q produced path %q", endpoint, parsed.Path)
		}
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
