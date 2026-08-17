package httpapi

import "testing"

func TestNormalizePublicBaseURL(t *testing.T) {
	for raw, want := range map[string]string{
		"":                             "",
		" https://cloud.example.com/ ": "https://cloud.example.com",
		"http://127.0.0.1:18083":       "http://127.0.0.1:18083",
	} {
		got, err := normalizePublicBaseURL(raw)
		if err != nil || got != want {
			t.Fatalf("normalizePublicBaseURL(%q)=%q,%v want %q,nil", raw, got, err, want)
		}
	}
	for _, raw := range []string{"cloud.example.com", "ftp://cloud.example.com", "https://:443", "https://user@cloud.example.com", "https://cloud.example.com/base", "https://cloud.example.com?q=1", "https://cloud.example.com/#fragment"} {
		if _, err := normalizePublicBaseURL(raw); err == nil {
			t.Fatalf("invalid public base URL %q was accepted", raw)
		}
	}
}

func TestValidateLinuxDoOAuthProfile(t *testing.T) {
	valid := linuxDoOAuthProfile{ID: 42, Username: "member", Name: "Member", Email: "member@example.com", Active: true, TrustLevel: 2}
	got, err := validateLinuxDoOAuthProfile(valid, 2)
	if err != nil || got.ID != "42" || got.Email != valid.Email {
		t.Fatalf("valid LinuxDo profile rejected: user=%+v err=%v", got, err)
	}
	for name, profile := range map[string]linuxDoOAuthProfile{
		"inactive":  {ID: 42, Active: false, TrustLevel: 4},
		"silenced":  {ID: 42, Active: true, Silenced: true, TrustLevel: 4},
		"low trust": {ID: 42, Active: true, TrustLevel: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateLinuxDoOAuthProfile(profile, 2); err == nil {
				t.Fatal("ineligible LinuxDo profile was accepted")
			}
		})
	}
}
