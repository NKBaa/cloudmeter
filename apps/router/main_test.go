package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestRoutePathAndCookieHelpers(t *testing.T) {
	if got := joinURLPath("/ui", "/api/items"); got != "/ui/api/items" {
		t.Fatalf("path=%s", got)
	}
	if got := joinURLPath("/", "/api/items"); got != "/api/items" {
		t.Fatalf("root path=%s", got)
	}
	cookie := rewriteCookiePath("session=abc; Path=/ui; HttpOnly; SameSite=Lax", "/ui", "/apps/user/app")
	if cookie != "session=abc; Path=/apps/user/app; HttpOnly; SameSite=Lax" {
		t.Fatalf("cookie=%s", cookie)
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
