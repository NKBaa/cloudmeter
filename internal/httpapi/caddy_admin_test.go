package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"cloudmeter/internal/config"
)

func TestSummarizeCaddyConfig(t *testing.T) {
	content := []byte(`{
		"apps":{"http":{"servers":{"gateway":{
			"listen":[":443"],
			"automatic_https":{},
			"logs":{"default_logger_name":"access"},
			"routes":[
				{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"api:8081"}]}]},
				{"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy"}]}]}]}
			]
		}}}}
	}`)
	overview := caddyOverview{Listeners: []string{}, Upstreams: []caddyUpstream{}, TLSMode: "disabled"}
	if err := summarizeCaddyConfig(content, &overview); err != nil {
		t.Fatal(err)
	}
	if overview.ServerCount != 1 || overview.RouteCount != 2 || overview.ProxyCount != 2 {
		t.Fatalf("unexpected summary: %+v", overview)
	}
	if overview.TLSMode != "automatic" || !overview.AccessLogEnabled || !reflect.DeepEqual(overview.Listeners, []string{":443"}) {
		t.Fatalf("unexpected runtime flags: %+v", overview)
	}
}

func TestJSONDocumentsEqualIgnoresFormattingAndKeyOrder(t *testing.T) {
	if !jsonDocumentsEqual([]byte(`{"a":1,"b":[true]}`), []byte("{\n\"b\":[true],\"a\":1}")) {
		t.Fatal("semantically equal JSON documents were reported as different")
	}
	if jsonDocumentsEqual([]byte(`{"a":1}`), []byte(`{"a":2}`)) {
		t.Fatal("different JSON documents were reported as equal")
	}
}

func TestCaddyAdaptResultUnwrapsAdminAPIEnvelope(t *testing.T) {
	wrapped := []byte(`{"result":{"apps":{"http":{}}},"warnings":[]}`)
	if !jsonDocumentsEqual(caddyAdaptResult(wrapped), []byte(`{"apps":{"http":{}}}`)) {
		t.Fatal("adapt result envelope was not unwrapped")
	}
	direct := []byte(`{"apps":{"http":{}}}`)
	if !jsonDocumentsEqual(caddyAdaptResult(direct), direct) {
		t.Fatal("direct adapted JSON was changed")
	}
}

func TestCaddyOverviewSanitizesRuntimeConfig(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config/":
			_, _ = w.Write([]byte(`{"apps":{"http":{"servers":{"gateway":{"listen":[":8080"],"automatic_https":{"disable":true},"routes":[{"handle":[{"handler":"reverse_proxy","headers":{"request":{"set":{"X-Secret":["must-not-leak"]}}}}]}]}}}}}`))
		case "/adapt":
			_, _ = w.Write([]byte(`{"result":{"apps":{"http":{"servers":{"gateway":{"listen":[":8080"],"automatic_https":{"disable":true},"routes":[{"handle":[{"handler":"reverse_proxy","headers":{"request":{"set":{"X-Secret":["must-not-leak"]}}}}]}]}}}}}}`))
		case "/reverse_proxy/upstreams":
			_, _ = w.Write([]byte(`[{"address":"api:8081","num_requests":2,"fails":0}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer admin.Close()

	path := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(path, []byte(":8080 { reverse_proxy api:8081 }"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{CaddyAdminURL: admin.URL, CaddyfilePath: path}}
	recorder := httptest.NewRecorder()
	s.getCaddyOverview(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/caddy/overview", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if contains := recorder.Body.String(); len(contains) == 0 || stringContains(contains, "must-not-leak") {
		t.Fatalf("runtime secret leaked: %s", contains)
	}
	var result caddyOverview
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Connected || result.ProxyCount != 1 || result.SourceInSync == nil || !*result.SourceInSync || len(result.Upstreams) != 1 || result.Upstreams[0].Address != "api:8081" {
		t.Fatalf("unexpected overview: %+v", result)
	}
}

func TestReloadCaddyConfigValidatesBeforeLoad(t *testing.T) {
	calls := []string{}
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if r.Header.Get("Content-Type") != "text/caddyfile" {
			t.Errorf("content type=%q", r.Header.Get("Content-Type"))
		}
		if r.URL.Path == "/load" && r.Header.Get("Cache-Control") != "must-revalidate" {
			t.Errorf("reload did not force validation")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer admin.Close()
	path := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(path, []byte(":8080 { respond ok }"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{CaddyAdminURL: admin.URL, CaddyfilePath: path}}
	recorder := httptest.NewRecorder()
	s.reloadCaddyConfig(recorder, httptest.NewRequest(http.MethodPost, "/api/admin/caddy/reload", nil))
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(calls, []string{"/adapt", "/load"}) {
		t.Fatalf("status=%d calls=%v body=%s", recorder.Code, calls, recorder.Body.String())
	}
}

func stringContains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
