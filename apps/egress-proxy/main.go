package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type proxy struct {
	token  string
	apiURL string
	logger *slog.Logger
	client *http.Client
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://127.0.0.1:3128/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		_ = resp.Body.Close()
		return
	}
	token := strings.TrimSpace(os.Getenv("EGRESS_INGEST_TOKEN"))
	if len(token) < 32 {
		panic("EGRESS_INGEST_TOKEN must contain at least 32 characters")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	p := &proxy{token: token, apiURL: strings.TrimRight(env("EGRESS_API_URL", "http://api:8081"), "/"), logger: logger, client: &http.Client{Timeout: 10 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/", p)
	server := &http.Server{Addr: ":3128", Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("egress proxy stopped", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	appID, ok := p.authenticate(r)
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="CloudMeter Egress"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		p.connect(w, r, appID)
		return
	}
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "only HTTP proxy requests are accepted", http.StatusBadRequest)
		return
	}
	conn, err := dialPublic(r.Context(), r.URL.Host, "80")
	if err != nil {
		http.Error(w, "target is not publicly routable", http.StatusForbidden)
		return
	}
	counted := &countingConn{Conn: conn}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(context.Context, string, string) (net.Conn, error) { return counted, nil }}
	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	outbound.Header = outbound.Header.Clone()
	outbound.Header.Del("Proxy-Authorization")
	outbound.Header.Del("Proxy-Connection")
	resp, err := transport.RoundTrip(outbound)
	if err != nil {
		_ = counted.Close()
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	_ = resp.Body.Close()
	transport.CloseIdleConnections()
	_ = counted.Close()
	p.report(appID, counted.written)
}

type countingConn struct {
	net.Conn
	written int64
}

func (c *countingConn) Write(value []byte) (int, error) {
	n, err := c.Conn.Write(value)
	c.written += int64(n)
	return n, err
}

func (p *proxy) connect(w http.ResponseWriter, r *http.Request, appID string) {
	target, err := dialPublic(r.Context(), r.Host, "443")
	if err != nil {
		http.Error(w, "target is not publicly routable", http.StatusForbidden)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = target.Close()
		http.Error(w, "tunneling unavailable", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		_ = target.Close()
		return
	}
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	done := make(chan struct{}, 1)
	go func() { _, _ = io.Copy(client, target); done <- struct{}{} }()
	written, _ := io.Copy(target, client)
	_ = target.Close()
	_ = client.Close()
	<-done
	p.report(appID, written)
}

func (p *proxy) authenticate(r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.Header.Get("Proxy-Authorization"))
	if !strings.HasPrefix(raw, "Basic ") {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "Basic "))
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 || len(parts[0]) != 36 {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(p.token))
	_, _ = mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	return parts[0], len(parts[1]) == len(expected) && subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) == 1
}

func dialPublic(ctx context.Context, address, defaultPort string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		host, port = address, defaultPort
	}
	if _, err = strconv.Atoi(port); err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	for _, ip := range ips {
		if !publicIP(ip) {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("no public address")
}

func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	blocked := []*net.IPNet{cidr("100.64.0.0/10"), cidr("192.0.0.0/24"), cidr("198.18.0.0/15"), cidr("2001:db8::/32"), cidr("fc00::/7")}
	for _, network := range blocked {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func cidr(value string) *net.IPNet { _, network, _ := net.ParseCIDR(value); return network }
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func (p *proxy) report(appID string, byteDelta int64) {
	if byteDelta <= 0 {
		return
	}
	sampleID := fmt.Sprintf("%s/%d", appID, time.Now().UnixNano())
	body, _ := json.Marshal(map[string]any{"sampleId": sampleID, "byteDelta": byteDelta, "observedAt": time.Now().UTC(), "source": "egress_proxy"})
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequest(http.MethodPost, p.apiURL+"/api/internal/egress/"+appID, strings.NewReader(string(body)))
		if err != nil {
			p.logger.Error("egress sample request failed", "app", appID, "sample", sampleID, "error", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CloudMeter-Egress-Token", p.token)
		resp, err := p.client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusAccepted {
				return
			}
			p.logger.Error("egress sample rejected", "app", appID, "sample", sampleID, "status", resp.StatusCode, "attempt", attempt+1)
		} else {
			p.logger.Error("egress sample report failed", "app", appID, "sample", sampleID, "error", err, "attempt", attempt+1)
		}
		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
