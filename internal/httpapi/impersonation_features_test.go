package httpapi

import "testing"

func TestImpersonationConfirmationMatchesTargetEmail(t *testing.T) {
	if !impersonationConfirmationMatches("User@Example.com", " user@example.com ") {
		t.Fatal("expected normalized target email to match")
	}
	if impersonationConfirmationMatches("user@example.com", "other@example.com") {
		t.Fatal("unexpected confirmation match")
	}
}
