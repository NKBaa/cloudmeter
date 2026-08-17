package httpapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeEmailDomainWhitelist(t *testing.T) {
	got, err := normalizeEmailDomainWhitelist([]string{" @Example.COM ", "sub.example.com", "example.com", ""})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example.com", "sub.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("domains=%v want=%v", got, want)
	}
}

func TestNormalizeEmailDomainWhitelistRejectsInvalidEntries(t *testing.T) {
	for _, domain := range []string{".example.com", "example..com", "-example.com", "example-.com", "example_.com", "example.com/path"} {
		t.Run(domain, func(t *testing.T) {
			if _, err := normalizeEmailDomainWhitelist([]string{domain}); err == nil {
				t.Fatalf("invalid domain %q was accepted", domain)
			}
		})
	}
	tooLongLabel := strings.Repeat("a", 64) + ".com"
	if _, err := normalizeEmailDomainWhitelist([]string{tooLongLabel}); err == nil {
		t.Fatal("domain with an oversized label was accepted")
	}
}

func TestValidatePolicyEmail(t *testing.T) {
	if got, policyError := validatePolicyEmail(" User@Sub.Example.com ", []string{"example.com"}, true); got != "user@sub.example.com" || policyError != "" {
		t.Fatalf("normalized=%q error=%q", got, policyError)
	}
	for name, address := range map[string]string{
		"plus alias":     "user+tag@example.com",
		"dot alias":      "user.name@example.com",
		"outside domain": "user@example.net",
	} {
		t.Run(name, func(t *testing.T) {
			if _, policyError := validatePolicyEmail(address, []string{"example.com"}, true); policyError == "" {
				t.Fatalf("address %q was accepted", address)
			}
		})
	}
	if got, policyError := validatePolicyEmail("user+tag@example.com", nil, false); got == "" || policyError != "" {
		t.Fatalf("aliases should be allowed when the policy is disabled: got=%q error=%q", got, policyError)
	}
	if got, policyError := validatePolicyEmail("user.name@example.com", nil, false); got == "" || policyError != "" {
		t.Fatalf("dot aliases should be allowed when the policy is disabled: got=%q error=%q", got, policyError)
	}
}

func TestSMTPConfigurationReady(t *testing.T) {
	if !smtpConfigurationReady(true, "smtp.example.com", 587, "user", true, "sender@example.com", "starttls") {
		t.Fatal("complete SMTP settings were not ready")
	}
	for name, ready := range map[string]bool{
		"disabled":         smtpConfigurationReady(false, "smtp.example.com", 587, "", false, "sender@example.com", "starttls"),
		"missing host":     smtpConfigurationReady(true, "", 587, "", false, "sender@example.com", "starttls"),
		"invalid port":     smtpConfigurationReady(true, "smtp.example.com", 0, "", false, "sender@example.com", "starttls"),
		"missing password": smtpConfigurationReady(true, "smtp.example.com", 587, "user", false, "sender@example.com", "starttls"),
		"invalid sender":   smtpConfigurationReady(true, "smtp.example.com", 587, "", false, "Sender <sender@example.com>", "starttls"),
		"invalid TLS":      smtpConfigurationReady(true, "smtp.example.com", 587, "", false, "sender@example.com", "optional"),
	} {
		t.Run(name, func(t *testing.T) {
			if ready {
				t.Fatal("incomplete SMTP settings were reported ready")
			}
		})
	}
}
