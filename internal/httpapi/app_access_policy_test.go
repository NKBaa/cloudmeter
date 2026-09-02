package httpapi

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestResolveAppAccessPolicy(t *testing.T) {
	created, err := resolveAppAccessPolicy(&appAccessSettingsRequest{
		PasswordEnabled: true,
		Username:        " visitor ",
		Password:        "correct-password",
	}, appAccessPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Enabled || created.Username != "visitor" || bcrypt.CompareHashAndPassword([]byte(created.Hash), []byte("correct-password")) != nil {
		t.Fatalf("unexpected created policy: enabled=%v username=%q", created.Enabled, created.Username)
	}

	preserved, err := resolveAppAccessPolicy(&appAccessSettingsRequest{PasswordEnabled: true, Username: "member"}, created)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.Username != "member" || preserved.Hash != created.Hash {
		t.Fatal("empty update password must preserve the existing hash")
	}

	disabled, err := resolveAppAccessPolicy(&appAccessSettingsRequest{}, created)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Username != "" || disabled.Hash != "" {
		t.Fatal("disabling password access must clear stored credentials")
	}
}

func TestResolveAppAccessPolicyRequiresInitialPassword(t *testing.T) {
	_, err := resolveAppAccessPolicy(&appAccessSettingsRequest{PasswordEnabled: true, Username: "visitor"}, appAccessPolicy{})
	if err == nil {
		t.Fatal("initial password was not required")
	}
}
