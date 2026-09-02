package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestRequireRouterToken(t *testing.T) {
	protected := requireRouterToken("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	denied := httptest.NewRecorder()
	protected.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/apps/test", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status=%d", denied.Code)
	}
	allowedRequest := httptest.NewRequest(http.MethodGet, "/apps/test", nil)
	allowedRequest.Header.Set("X-CloudMeter-Router-Token", "secret")
	allowed := httptest.NewRecorder()
	protected.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("allowed status=%d", allowed.Code)
	}
}

func TestRequestHostname(t *testing.T) {
	for input, want := range map[string]string{
		"Demo-User.Apps.Example.COM:8080": "demo-user.apps.example.com",
		"demo-user.apps.example.com.":     "demo-user.apps.example.com",
		"[::1]:8080":                      "::1",
	} {
		if got := requestHostname(input); got != want {
			t.Fatalf("requestHostname(%q)=%q want %q", input, got, want)
		}
	}
}

func TestAuthorizeBasicAccess(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	publicResponse := httptest.NewRecorder()
	if !authorizeBasicAccess(publicResponse, httptest.NewRequest(http.MethodGet, "/", nil), false, "", "") {
		t.Fatal("public application unexpectedly required authentication")
	}

	deniedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	deniedRequest.SetBasicAuth("visitor", "wrong-password")
	deniedResponse := httptest.NewRecorder()
	if authorizeBasicAccess(deniedResponse, deniedRequest, true, "visitor", string(hash)) {
		t.Fatal("invalid credentials were accepted")
	}
	if deniedResponse.Code != http.StatusUnauthorized || deniedResponse.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("status=%d challenge=%q", deniedResponse.Code, deniedResponse.Header().Get("WWW-Authenticate"))
	}
	if got := deniedResponse.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache control=%q", got)
	}

	allowedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	allowedRequest.SetBasicAuth("visitor", "correct-password")
	if !authorizeBasicAccess(httptest.NewRecorder(), allowedRequest, true, "visitor", string(hash)) {
		t.Fatal("valid credentials were rejected")
	}
}

func TestProtectedCacheHeadersPreserveExistingVary(t *testing.T) {
	header := http.Header{"Vary": []string{"Accept-Encoding"}}
	setProtectedCacheHeaders(header)
	setProtectedCacheHeaders(header)
	if got := strings.Join(header.Values("Vary"), ","); got != "Accept-Encoding,Authorization" {
		t.Fatalf("vary=%q", got)
	}
}

func TestRoutePathHelper(t *testing.T) {
	if got := joinURLPath("/ui", "/api/items"); got != "/ui/api/items" {
		t.Fatalf("path=%s", got)
	}
	if got := joinURLPath("/", "/api/items"); got != "/api/items" {
		t.Fatalf("root path=%s", got)
	}
}

func TestWebSocketDetection(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/apps/test", nil)
	request.Header.Set("Connection", "keep-alive, Upgrade")
	request.Header.Set("Upgrade", "websocket")
	if !isWebSocketRequest(request) {
		t.Fatal("websocket request not detected")
	}
}
