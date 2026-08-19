package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRedirectAppRoot(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/apps/user/demo?tab=chat", nil)
	response := httptest.NewRecorder()
	if !redirectAppRoot(response, request, "/apps/user/demo") {
		t.Fatal("application root was not redirected")
	}
	if response.Code != http.StatusPermanentRedirect {
		t.Fatalf("status=%d", response.Code)
	}
	if location := response.Header().Get("Location"); location != "/apps/user/demo/?tab=chat" {
		t.Fatalf("location=%s", location)
	}

	websocket := httptest.NewRequest(http.MethodGet, "/apps/user/demo", nil)
	websocket.Header.Set("Connection", "Upgrade")
	websocket.Header.Set("Upgrade", "websocket")
	if redirectAppRoot(httptest.NewRecorder(), websocket, "/apps/user/demo") {
		t.Fatal("websocket handshake must not be redirected")
	}
}

func TestRewriteRootHTMLBase(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"text/html; charset=utf-8"},
			"Content-Length": []string{"58"},
			"ETag":           []string{`"root-document"`},
		},
		Body: io.NopCloser(strings.NewReader(`<!doctype html><head><base href="/"><title>App</title></head>`)),
	}
	if err := rewriteRootHTMLBase(response, "/apps/demo/my-app"); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	wanted := `<!doctype html><head><base href="/apps/demo/my-app/"><title>App</title></head>`
	if string(body) != wanted {
		t.Fatalf("body=%s", body)
	}
	if response.ContentLength != int64(len(wanted)) || response.Header.Get("Content-Length") != fmt.Sprint(len(wanted)) {
		t.Fatalf("content length=%d header=%s", response.ContentLength, response.Header.Get("Content-Length"))
	}
	if response.Header.Get("ETag") != "" {
		t.Fatal("rewritten response retained the upstream ETag")
	}
}

func TestRewriteRootHTMLBaseLeavesOtherResponsesUntouched(t *testing.T) {
	body := `<base href="/">`
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	if err := rewriteRootHTMLBase(response, "/apps/demo/my-app"); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(response.Body)
	if string(got) != body {
		t.Fatalf("non-HTML response changed: %s", got)
	}
}
