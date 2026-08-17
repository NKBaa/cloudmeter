package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPublicIPRejectsPrivateAndSpecialRanges(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "100.64.0.1", "169.254.1.1", "::1", "fc00::1", "2001:db8::1"} {
		if publicIP(net.ParseIP(value)) {
			t.Fatalf("private address accepted: %s", value)
		}
	}
	if !publicIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("public address rejected")
	}
}

func TestProxyAuthenticationUsesHMACIdentity(t *testing.T) {
	p := &proxy{token: "01234567890123456789012345678901"}
	request := httptest.NewRequest("GET", "http://example.com", nil)
	request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 36)+":wrong")))
	if _, ok := p.authenticate(request); ok {
		t.Fatal("invalid proxy credential accepted")
	}
}

func TestReportRebuildsRequestBodyForRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		var sample map[string]any
		if err := json.NewDecoder(r.Body).Decode(&sample); err != nil {
			t.Errorf("attempt %d received invalid JSON: %v", attempt, err)
		}
		if sample["byteDelta"] != float64(512) {
			t.Errorf("attempt %d received wrong byte delta: %v", attempt, sample["byteDelta"])
		}
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	p := &proxy{token: strings.Repeat("x", 32), apiURL: server.URL, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), client: server.Client()}
	p.report(strings.Repeat("a", 36), 512)
	if attempts.Load() != 2 {
		t.Fatalf("expected two attempts, got %d", attempts.Load())
	}
}
