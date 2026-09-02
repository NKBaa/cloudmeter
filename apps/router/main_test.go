package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestNewAccessToken(t *testing.T) {
	first, err := newAccessToken()
	if err != nil || first == "" {
		t.Fatalf("first token=%q err=%v", first, err)
	}
	second, err := newAccessToken()
	if err != nil || second == "" || second == first {
		t.Fatalf("second token=%q err=%v", second, err)
	}
}

func TestAccessGrantQueriesDoNotUseReservedGrantAlias(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "app_access_grants grant") {
		t.Fatal("PostgreSQL GRANT keyword must not be used as an unquoted table alias")
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

func TestReservedAccessCookieIsNotForwarded(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/apps/user/app/", nil)
	request.Header.Set("Cookie", "theme=dark; cloudmeter_app_access=secret; app_session=ok")
	removeRequestCookie(request, appAccessCookie)
	if got := request.Header.Get("Cookie"); got != "theme=dark; app_session=ok" {
		t.Fatalf("unexpected forwarded cookies: %q", got)
	}
}

func TestUpstreamCannotOverwriteReservedAccessCookie(t *testing.T) {
	header := http.Header{}
	header.Add("Set-Cookie", "cloudmeter_app_access=stolen; Path=/")
	header.Add("Set-Cookie", "app_session=ok; Path=/")
	stripReservedResponseCookie(header, appAccessCookie)
	values := header.Values("Set-Cookie")
	if len(values) != 1 || values[0] != "app_session=ok; Path=/" {
		t.Fatalf("unexpected response cookies: %#v", values)
	}
}

func TestRedirectAppRoot(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/apps/user/demo?tab=chat", nil)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
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

	apiClient := httptest.NewRequest(http.MethodGet, "/apps/user/demo", nil)
	apiClient.Header.Set("Accept", "*/*")
	if redirectAppRoot(httptest.NewRecorder(), apiClient, "/apps/user/demo") {
		t.Fatal("non-browser root request must be proxied, not redirected")
	}

	websocket := httptest.NewRequest(http.MethodGet, "/apps/user/demo", nil)
	websocket.Header.Set("Accept", "text/html")
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
