package httpapi

import (
	"strings"
	"testing"

	"cloudmeter/internal/config"
)

func TestNormalizeGatewaySettingsAppliesModeOrigin(t *testing.T) {
	standalone := gatewaySettings{
		AccessMode: "apps_only", ServerURL: "https://console.example.com:9999", StandalonePort: 8080,
		HTTPPolicy: "redirect", ConsoleCertificateMode: "automatic", AppCertificateMode: "automatic",
		ACMECA: defaultACMEDirectory,
	}
	if err := normalizeGatewaySettings(&standalone); err != nil {
		t.Fatal(err)
	}
	if standalone.ServerURL != "https://console.example.com:9999" {
		t.Fatalf("standalone server URL=%q", standalone.ServerURL)
	}

	managed := gatewaySettings{
		AccessMode: "all_caddy", ServerURL: "http://console.example.com:8080", StandalonePort: 8080,
		TLSEnabled: true, HTTPPolicy: "redirect", ConsoleCertificateMode: "automatic", AppCertificateMode: "automatic",
		ACMEEmail: "ops@example.com", ACMECA: defaultACMEDirectory, ACMEKeyType: "p256", RenewIntervalMinutes: 10,
		ACMEDNSProvider: "cloudflare", ACMEDNSCredentials: map[string]string{"apiToken": "test-token"},
	}
	if err := normalizeGatewaySettings(&managed); err != nil {
		t.Fatal(err)
	}
	if managed.ServerURL != "https://console.example.com" {
		t.Fatalf("managed server URL=%q", managed.ServerURL)
	}
}

func TestRenderGatewayCaddyfileModes(t *testing.T) {
	s := &Server{cfg: config.Config{StandalonePort: 8080}}
	settings := gatewaySettings{
		AccessMode: "all_caddy", ServerURL: "https://console.example.com", AppBaseDomain: "apps.example.com",
		TLSEnabled: true, HTTPPolicy: "redirect", HSTSEnabled: true, HTTP3Enabled: true,
		ConsoleCertificateMode: "automatic", AppCertificateMode: "automatic", ACMEEmail: "ops@example.com", ACMECA: defaultACMEDirectory,
		ACMEKeyType: "p256", ACMEDNSProvider: "alidns",
		ACMEDNSCredentials: map[string]string{"accessKeyId": "test-id", "accessKeySecret": "test-secret"}, RenewIntervalMinutes: 10,
	}
	content, err := s.renderGatewayCaddyfile(settings)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{
		"https://console.example.com", "http://console.example.com", "*.apps.example.com",
		"on_demand_tls", "X-CloudMeter-Entry-Token", "Strict-Transport-Security", "protocols h1 h2 h3",
		"key_type p256", "renew_interval 10m",
		"acme_dns alidns", `access_key_id "test-id"`, `access_key_secret "test-secret"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("generated Caddyfile is missing %q:\n%s", expected, text)
		}
	}

	settings.AccessMode = "apps_only"
	settings.ServerURL = "http://console.example.com:8080"
	content, err = s.renderGatewayCaddyfile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "https://console.example.com") {
		t.Fatalf("applications-only mode included a console Caddy site:\n%s", content)
	}
}

func TestNormalizeGatewaySettingsRequiresDNSProviderCredentials(t *testing.T) {
	settings := gatewaySettings{
		AccessMode: "all_caddy", ServerURL: "https://console.example.com", TLSEnabled: true, HTTPPolicy: "redirect",
		ConsoleCertificateMode: "automatic", AppCertificateMode: "automatic", ACMEEmail: "ops@example.com",
		ACMECA: defaultACMEDirectory, ACMEKeyType: "p256", RenewIntervalMinutes: 10, ACMEDNSProvider: "cloudflare",
	}
	if err := normalizeGatewaySettings(&settings); err == nil || !strings.Contains(err.Error(), "凭据") {
		t.Fatalf("expected missing credential error, got %v", err)
	}
	settings.ACMEDNSCredentials = map[string]string{"apiToken": "token", "unexpected": "discarded"}
	if err := normalizeGatewaySettings(&settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.ACMEDNSCredentials) != 1 || settings.ACMEDNSCredentials["apiToken"] != "token" {
		t.Fatalf("credentials were not normalized: %#v", settings.ACMEDNSCredentials)
	}
}
