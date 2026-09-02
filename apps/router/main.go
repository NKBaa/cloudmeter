package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"cloudmeter/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const appAccessCookie = "cloudmeter_app_access"

const (
	routeModeHeader    = "X-CloudMeter-Route-Mode"
	routeModeLegacy    = "legacy"
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
		if routeMode != routeModeLegacy && routeMode != routeModeSubdomain {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}

		var appBaseDomain, serverURL string
		if err := db.QueryRow(r.Context(), `SELECT coalesce(app_base_domain,''),coalesce(server_url,'') FROM system_settings WHERE singleton`).Scan(&appBaseDomain, &serverURL); err != nil {
			logger.Error("system route settings lookup failed", "error", err)
			http.Error(w, "route settings unavailable", http.StatusBadGateway)
			return
		}
		appBaseDomain = normalizeHostname(appBaseDomain)
		hostMatch := ""
		if routeMode == routeModeSubdomain {
			hostname := requestHostname(r.Host)
			suffix := "." + appBaseDomain
			if appBaseDomain == "" || !strings.HasSuffix(hostname, suffix) {
				http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
				return
			}
			hostMatch = strings.TrimSuffix(hostname, suffix)
			if hostMatch == "" || strings.Contains(hostMatch, ".") {
				http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
				return
			}
			if r.URL.Path == "/.cloudmeter/access" {
				redeemAppAccessGrant(db, logger, w, r, hostMatch)
				return
			}
		}

		cookie, cookieErr := r.Cookie(appAccessCookie)
		if cookieErr != nil || strings.TrimSpace(cookie.Value) == "" {
			if acceptsHTML(r) {
				redirectToConsole(w, r, serverURL, "/console/apps")
				return
			}
			http.Error(w, "application sign-in required", http.StatusUnauthorized)
			return
		}
		tokenHash := sha256.Sum256([]byte(cookie.Value))
		var sessionUserID string
		var authErr error
		if routeMode == routeModeSubdomain {
			authErr = db.QueryRow(r.Context(), `SELECT access_grant.user_id::text
				FROM app_access_grants access_grant
				JOIN users ON users.id=access_grant.user_id AND users.status='active'
				JOIN user_apps app ON app.id=access_grant.user_app_id AND app.user_id=access_grant.user_id AND app.deleted_at IS NULL
				WHERE access_grant.token_hash=$1 AND access_grant.expires_at>now() AND app.route_host_label=$2`, tokenHash[:], hostMatch).Scan(&sessionUserID)
		} else {
			authErr = db.QueryRow(r.Context(), `SELECT session.user_id::text FROM sessions session
				JOIN users ON users.id=session.user_id AND users.status='active'
				WHERE session.token_hash=$1 AND session.revoked_at IS NULL AND session.expires_at>now()`, tokenHash[:]).Scan(&sessionUserID)
		}
		if authErr != nil {
			if authErr == pgx.ErrNoRows {
				if acceptsHTML(r) {
					redirectToConsole(w, r, serverURL, "/console/apps")
					return
				}
				http.Error(w, "application session is invalid or expired", http.StatusUnauthorized)
				return
			}
			logger.Error("application session lookup failed", "error", authErr)
			http.Error(w, "session lookup failed", http.StatusBadGateway)
			return
		}
		var prefix, host string
		var port int
		var routeSpec map[string]any

		err := db.QueryRow(r.Context(), `SELECT
		'/apps/'||u.slug||'/'||a.slug,
		'release-'||left(replace(rel.id::text,'-',''),12),
		ar.upstream_port,
		coalesce(rel.immutable_snapshot->'route_spec','{}'::jsonb)
		FROM app_routes ar
		JOIN user_apps a ON a.id=ar.user_app_id AND a.instance_id=ar.instance_id
		JOIN users u ON u.id=a.user_id
		JOIN app_releases rel ON rel.id=ar.release_id AND rel.user_app_id=a.id
		WHERE a.user_id=$2 AND u.status='active' AND a.status IN ('running','updating') AND a.last_successful_release_id=rel.id AND rel.state='active'
		  AND (($3='subdomain' AND $4<>'' AND $4=a.route_host_label)
		       OR ($3='legacy' AND ($1='/apps/'||u.slug||'/'||a.slug OR $1 LIKE '/apps/'||u.slug||'/'||a.slug||'/%')))
		ORDER BY length('/apps/'||u.slug||'/'||a.slug) DESC LIMIT 1`, r.URL.Path, sessionUserID, routeMode, hostMatch).Scan(&prefix, &host, &port, &routeSpec)
		if err == pgx.ErrNoRows {
			// The application is not actively routable (stopped, failed, or the
			// route is stale). Resolve the instance and send the user to the
			// console detail page instead of a blank 404. Non-navigation
			// requests keep the plain 404 so assets fail predictably.
			if acceptsHTML(r) {
				var instanceID string
				if lookupErr := db.QueryRow(r.Context(), `SELECT a.instance_id::text
					FROM app_routes ar
					JOIN user_apps a ON a.id=ar.user_app_id AND a.instance_id=ar.instance_id
					JOIN users u ON u.id=a.user_id
					WHERE a.user_id=$2 AND a.deleted_at IS NULL AND u.status='active'
					  AND (($3='subdomain' AND $4<>'' AND $4=a.route_host_label)
					       OR ($3='legacy' AND ($1='/apps/'||u.slug||'/'||a.slug OR $1 LIKE '/apps/'||u.slug||'/'||a.slug||'/%')))
					ORDER BY length('/apps/'||u.slug||'/'||a.slug) DESC LIMIT 1`, r.URL.Path, sessionUserID, routeMode, hostMatch).Scan(&instanceID); lookupErr == nil && instanceID != "" {
					redirectToConsole(w, r, serverURL, "/console/apps/"+instanceID)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		if err != nil {
			logger.Error("route lookup failed", "error", err)
			http.Error(w, "route lookup failed", http.StatusBadGateway)
			return
		}
		if routeMode == routeModeSubdomain {
			prefix = ""
		}
		if redirectAppRoot(w, r, prefix) {
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
		rootRequest := r.Method == http.MethodGet && r.URL.Path == prefix+"/" && !isWebSocketRequest(r)
		if routeBoolean(routeSpec, "sse", true) {
			proxy.FlushInterval = -1
		}
		original := proxy.Director
		proxy.Director = func(req *http.Request) {
			original(req)
			removeRequestCookie(req, appAccessCookie)
			if routeBoolean(routeSpec, "stripPrefix", true) {
				path := strings.TrimPrefix(req.URL.Path, prefix)
				if path == "" {
					path = "/"
				}
				req.URL.Path = joinURLPath(routeString(routeSpec, "basePath", "/"), path)
			}
			if rootRequest {
				// Let Go transparently decode an upstream gzip response so the
				// public-path adapter can safely inspect the root HTML document.
				req.Header.Del("Accept-Encoding")
				// A 304 has no body to adapt. Always fetch the root document so a
				// previously cached unprefixed <base> cannot bypass the adapter.
				req.Header.Del("If-None-Match")
				req.Header.Del("If-Modified-Since")
			}
			req.Header.Set("X-Forwarded-Prefix", prefix)
		}
		cookiePath := routeString(routeSpec, "cookiePath", "")
		proxy.ModifyResponse = func(response *http.Response) error {
			stripReservedResponseCookie(response.Header, appAccessCookie)
			if prefix != "" && (cookiePath != "" || rootRequest) {
				if rootRequest {
					if err := rewriteRootHTMLBase(response, prefix); err != nil {
						return err
					}
				}
				if cookiePath != "" {
					values := response.Header.Values("Set-Cookie")
					if len(values) > 0 {
						response.Header.Del("Set-Cookie")
						for _, value := range values {
							response.Header.Add("Set-Cookie", rewriteCookiePath(value, cookiePath, prefix))
						}
					}
				}
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

func redeemAppAccessGrant(db *pgxpool.Pool, logger *slog.Logger, w http.ResponseWriter, r *http.Request, hostMatch string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	grant := strings.TrimSpace(r.URL.Query().Get("grant"))
	if grant == "" {
		http.Error(w, "application access grant is required", http.StatusUnauthorized)
		return
	}
	grantHash := sha256.Sum256([]byte(grant))
	tx, err := db.Begin(r.Context())
	if err != nil {
		logger.Error("application access transaction failed", "error", err)
		http.Error(w, "application access unavailable", http.StatusBadGateway)
		return
	}
	defer tx.Rollback(r.Context())
	var expiresAt time.Time
	if err = tx.QueryRow(r.Context(), `SELECT access_grant.expires_at
		FROM app_access_grants access_grant
		JOIN users ON users.id=access_grant.user_id AND users.status='active'
		JOIN user_apps app ON app.id=access_grant.user_app_id AND app.user_id=access_grant.user_id AND app.deleted_at IS NULL
		WHERE access_grant.token_hash=$1 AND access_grant.expires_at>now() AND app.status IN ('running','updating')
		  AND app.route_host_label=$2
		FOR UPDATE OF access_grant`, grantHash[:], hostMatch).Scan(&expiresAt); err != nil {
		if err == pgx.ErrNoRows {
			http.Error(w, "application access grant is invalid or expired", http.StatusUnauthorized)
			return
		}
		logger.Error("application access grant lookup failed", "error", err)
		http.Error(w, "application access unavailable", http.StatusBadGateway)
		return
	}
	cookieToken, err := newAccessToken()
	if err != nil {
		logger.Error("application cookie generation failed", "error", err)
		http.Error(w, "application access unavailable", http.StatusInternalServerError)
		return
	}
	cookieHash := sha256.Sum256([]byte(cookieToken))
	if _, err = tx.Exec(r.Context(), `UPDATE app_access_grants SET token_hash=$1 WHERE token_hash=$2`, cookieHash[:], grantHash[:]); err != nil {
		logger.Error("application access grant rotation failed", "error", err)
		http.Error(w, "application access unavailable", http.StatusBadGateway)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		logger.Error("application access grant commit failed", "error", err)
		http.Error(w, "application access unavailable", http.StatusBadGateway)
		return
	}
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	secure := strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     appAccessCookie,
		Value:    cookieToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
		Expires:  expiresAt,
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func newAccessToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
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

func redirectToConsole(w http.ResponseWriter, r *http.Request, serverURL, path string) {
	serverURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if serverURL == "" {
		http.Error(w, "platform server URL is not configured", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, serverURL+path, http.StatusFound)
}

func removeRequestCookie(r *http.Request, reserved string) {
	values := make([]string, 0)
	for _, cookie := range r.Cookies() {
		if cookie.Name != reserved {
			values = append(values, cookie.Name+"="+cookie.Value)
		}
	}
	if len(values) == 0 {
		r.Header.Del("Cookie")
	} else {
		r.Header.Set("Cookie", strings.Join(values, "; "))
	}
}

func stripReservedResponseCookie(header http.Header, reserved string) {
	values := header.Values("Set-Cookie")
	if len(values) == 0 {
		return
	}
	header.Del("Set-Cookie")
	for _, value := range values {
		name, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(strings.TrimSpace(name), reserved) {
			header.Add("Set-Cookie", value)
		}
	}
}

const maxRootHTMLRewriteBytes = 4 << 20

var rootHTMLBasePattern = regexp.MustCompile(`(?i)(<base\b[^>]*\bhref\s*=\s*["'])/(["'])`)

type joinedReadCloser struct {
	io.Reader
	io.Closer
}

func rewriteRootHTMLBase(response *http.Response, publicPath string) error {
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices ||
		!strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/html") ||
		(response.Header.Get("Content-Encoding") != "" && !strings.EqualFold(response.Header.Get("Content-Encoding"), "identity")) {
		return nil
	}
	body := response.Body
	payload, err := io.ReadAll(io.LimitReader(body, maxRootHTMLRewriteBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxRootHTMLRewriteBytes {
		response.Body = &joinedReadCloser{Reader: io.MultiReader(bytes.NewReader(payload), body), Closer: body}
		return nil
	}
	_ = body.Close()
	prefix := strings.TrimRight(publicPath, "/") + "/"
	rewritten := rootHTMLBasePattern.ReplaceAll(payload, []byte("$1"+prefix+"$2"))
	response.Body = io.NopCloser(bytes.NewReader(rewritten))
	response.ContentLength = int64(len(rewritten))
	response.Header.Set("Content-Length", fmt.Sprint(len(rewritten)))
	if !bytes.Equal(payload, rewritten) {
		response.Header.Del("ETag")
		response.Header.Del("Content-MD5")
	}
	return nil
}

func redirectAppRoot(w http.ResponseWriter, r *http.Request, prefix string) bool {
	// Only browser navigation benefits from the trailing-slash redirect so
	// relative URLs resolve against the public base. API clients and health
	// probes (curl sends Accept: */*) proxy straight through.
	if r.URL.Path != prefix || isWebSocketRequest(r) || !acceptsHTML(r) {
		return false
	}
	location := prefix + "/"
	if r.URL.RawQuery != "" {
		location += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, location, http.StatusPermanentRedirect)
	return true
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

func acceptsHTML(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
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
