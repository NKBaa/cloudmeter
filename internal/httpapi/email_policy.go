package httpapi

import (
	"fmt"
	"net/mail"
	"strings"
)

func normalizeEmailDomainWhitelist(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		domain := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "@")))
		if domain == "" {
			continue
		}
		if !validEmailDomain(domain) {
			return nil, fmt.Errorf("invalid email domain whitelist entry: %s", domain)
		}
		if _, exists := seen[domain]; exists {
			continue
		}
		seen[domain] = struct{}{}
		result = append(result, domain)
	}
	return result, nil
}

func validEmailDomain(domain string) bool {
	if len(domain) > 253 || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func validatePolicyEmail(raw string, whitelist []string, blockAliases bool) (string, string) {
	email := strings.ToLower(strings.TrimSpace(raw))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", "email is invalid"
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "email is invalid"
	}
	local, domain := parts[0], parts[1]
	if blockAliases && (strings.Contains(local, "+") || strings.Contains(local, ".")) {
		return "", "email aliases are not allowed"
	}
	if len(whitelist) > 0 {
		allowed := false
		for _, item := range whitelist {
			item = strings.ToLower(strings.TrimSpace(item))
			if item != "" && (domain == item || strings.HasSuffix(domain, "."+item)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", "email domain is not allowed"
		}
	}
	return email, ""
}
