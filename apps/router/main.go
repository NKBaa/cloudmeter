package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cloudmeter/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	routeModeHeader    = "X-CloudMeter-Route-Mode"
	routeModeSubdomain = "subdomain"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		response, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://127.0.0.1:8082/healthz")
		if err != nil || response.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		_ = response.Body.Close()
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.RouterToken = strings.TrimSpace(cfg.RouterToken)
	if len(cfg.RouterToken) < 32 {
		logger.Error("ROUTER_INTERNAL_TOKEN must contain at least 32 characters")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database configuration failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/", requireRouterToken(cfg.RouterToken, routeHandler(db, logger)))
	server := &http.Server{Addr: ":8082", Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		logger.Info("app router listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("router stopped", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}

func requireRouterToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-CloudMeter-Router-Token")
		if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func routeHandler(db *pgxpool.Pool, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeMode := strings.ToLower(strings.TrimSpace(r.Header.Get(routeModeHeader)))
		if routeMode != routeModeSubdomain {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}

		var appBaseDomain string
		if err := db.QueryRow(r.Context(), `SELECT coalesce(app_base_domain,'') FROM system_settings WHERE singleton`).Scan(&appBaseDomain); err != nil {
			logger.Error("system route settings lookup failed", "error", err)
			http.Error(w, "route settings unavailable", http.StatusBadGateway)
			return
		}
		appBaseDomain = normalizeHostname(appBaseDomain)
		hostname := requestHostname(r.Host)
		suffix := "." + appBaseDomain
		if appBaseDomain == "" || !strings.HasSuffix(hostname, suffix) {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		hostMatch := strings.TrimSuffix(hostname, suffix)
		if hostMatch == "" || strings.Contains(hostMatch, ".") {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		var host string
		var port int
		var routeSpec map[string]any
		var passwordEnabled bool
		var accessUsername, accessPasswordHash string

		err := db.QueryRow(r.Context(), `SELECT
			'release-'||left(replace(rel.id::text,'-',''),12), ar.upstream_port,
			coalesce(rel.immutable_snapshot->'route_spec','{}'::jsonb),
			a.access_password_enabled,a.access_username,a.access_password_hash
			FROM app_routes ar
			JOIN user_apps a ON a.id=ar.user_app_id AND a.instance_id=ar.instance_id
			JOIN users u ON u.id=a.user_id AND u.status='active'
			JOIN app_releases rel ON rel.id=ar.release_id AND rel.user_app_id=a.id
			WHERE a.deleted_at IS NULL AND a.status IN ('running','updating')
			  AND a.last_successful_release_id=rel.id AND rel.state='active' AND a.route_host_label=$1
			LIMIT 1`, hostMatch).Scan(&host, &port, &routeSpec, &passwordEnabled, &accessUsername, &accessPasswordHash)
		if err == pgx.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			logger.Error("route lookup failed", "error", err)
			http.Error(w, "route lookup failed", http.StatusBadGateway)
			return
		}
		if !authorizeBasicAccess(w, r, passwordEnabled, accessUsername, accessPasswordHash) {
			return
		}
		if isWebSocketRequest(r) && !routeBoolean(routeSpec, "websocket", true) {
			http.Error(w, "websocket is disabled for this application", http.StatusUpgradeRequired)
			return
		}
		if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") && !routeBoolean(routeSpec, "sse", true) {
			http.Error(w, "server-sent events are disabled for this application", http.StatusNotAcceptable)
			return
		}
		target, _ := url.Parse(fmt.Sprintf("http://%s:%d", host, port))
		proxy := httputil.NewSingleHostReverseProxy(target)
		if routeBoolean(routeSpec, "sse", true) {
			proxy.FlushInterval = -1
		}
		original := proxy.Director
		proxy.Director = func(req *http.Request) {
			original(req)
			if passwordEnabled {
				req.Header.Del("Authorization")
			}
			path := req.URL.Path
			if path == "" {
				path = "/"
			}
			req.URL.Path = joinURLPath(routeString(routeSpec, "basePath", "/"), path)
		}
		proxy.ModifyResponse = func(response *http.Response) error {
			if passwordEnabled {
				setProtectedCacheHeaders(response.Header)
			}
			return nil
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
			logger.Error("upstream failed", "host", host, "error", err)
			http.Error(w, "application unavailable", http.StatusBadGateway)
		}
		proxy.ServeHTTP(w, r)
	})
}

func authorizeBasicAccess(w http.ResponseWriter, r *http.Request, enabled bool, expectedUsername, passwordHash string) bool {
	if !enabled {
		return true
	}
	username, password, ok := r.BasicAuth()
	usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(expectedUsername)) == 1
	passwordMatch := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) == nil
	if ok && usernameMatch && passwordMatch {
		return true
	}
	setProtectedCacheHeaders(w.Header())
	w.Header().Set("WWW-Authenticate", `Basic realm="Application", charset="UTF-8"`)
	http.Error(w, "application authentication required", http.StatusUnauthorized)
	return false
}

func setProtectedCacheHeaders(header http.Header) {
	header.Set("Cache-Control", "private, no-store")
	for _, value := range header.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Authorization") {
				return
			}
		}
	}
	header.Add("Vary", "Authorization")
}

func requestHostname(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		return normalizeHostname(host)
	}
	return normalizeHostname(strings.Trim(value, "[]"))
}

func normalizeHostname(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func routeBoolean(spec map[string]any, key string, fallback bool) bool {
	value, ok := spec[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func routeString(spec map[string]any, key, fallback string) string {
	value, ok := spec[key].(string)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func joinURLPath(base, suffix string) string {
	if base == "" || base == "/" {
		if suffix == "" {
			return "/"
		}
		if strings.HasPrefix(suffix, "/") {
			return suffix
		}
		return "/" + suffix
	}
	if suffix == "" || suffix == "/" {
		return strings.TrimRight(base, "/") + "/"
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") && strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}
