package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
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
	mux.Handle("/apps/", requireRouterToken(cfg.RouterToken, routeHandler(db, logger)))
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
		var prefix, host string
		var port int
		var routeSpec map[string]any
		err := db.QueryRow(r.Context(), `SELECT
		'/apps/'||u.slug||'/'||a.slug,
		'release-'||left(replace(rel.id::text,'-',''),12),
		coalesce(nullif(rel.immutable_snapshot->'route_spec'->>'port','')::int,nullif(rel.immutable_snapshot->'route_spec'->>'containerPort','')::int,8080),
		coalesce(rel.immutable_snapshot->'route_spec','{}'::jsonb)
		FROM app_routes ar
		JOIN user_apps a ON a.id=ar.user_app_id
		JOIN users u ON u.id=a.user_id
		JOIN app_releases rel ON rel.id=ar.release_id AND rel.user_app_id=a.id
		WHERE a.status IN ('running','updating') AND a.last_successful_release_id=rel.id AND rel.state='active'
		  AND ($1='/apps/'||u.slug||'/'||a.slug OR $1 LIKE '/apps/'||u.slug||'/'||a.slug||'/%')
		ORDER BY length('/apps/'||u.slug||'/'||a.slug) DESC LIMIT 1`, r.URL.Path).Scan(&prefix, &host, &port, &routeSpec)
		if err == pgx.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			logger.Error("route lookup failed", "error", err)
			http.Error(w, "route lookup failed", http.StatusBadGateway)
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
			if routeBoolean(routeSpec, "stripPrefix", true) {
				path := strings.TrimPrefix(req.URL.Path, prefix)
				if path == "" {
					path = "/"
				}
				req.URL.Path = joinURLPath(routeString(routeSpec, "basePath", "/"), path)
			}
			req.Header.Set("X-Forwarded-Prefix", prefix)
		}
		if cookiePath := routeString(routeSpec, "cookiePath", ""); cookiePath != "" {
			proxy.ModifyResponse = func(response *http.Response) error {
				values := response.Header.Values("Set-Cookie")
				if len(values) == 0 {
					return nil
				}
				response.Header.Del("Set-Cookie")
				for _, value := range values {
					response.Header.Add("Set-Cookie", rewriteCookiePath(value, cookiePath, prefix))
				}
				return nil
			}
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
			logger.Error("upstream failed", "host", host, "error", err)
			http.Error(w, "application unavailable", http.StatusBadGateway)
		}
		proxy.ServeHTTP(w, r)
	})
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

func rewriteCookiePath(header, upstreamPath, publicPath string) string {
	parts := strings.Split(header, ";")
	found := false
	for index := 1; index < len(parts); index++ {
		attribute := strings.TrimSpace(parts[index])
		name, value, ok := strings.Cut(attribute, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "Path") {
			continue
		}
		found = true
		if strings.TrimSpace(value) == upstreamPath {
			parts[index] = " Path=" + publicPath
		}
	}
	if !found && upstreamPath == "/" {
		parts = append(parts, " Path="+publicPath)
	}
	return strings.Join(parts, ";")
}
