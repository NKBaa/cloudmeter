package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"
)

const maxCaddyResponseBytes = 2 << 20

type caddyUpstream struct {
	Address     string `json:"address"`
	NumRequests int    `json:"numRequests"`
	Fails       int    `json:"fails"`
}

type caddyOverview struct {
	Connected        bool            `json:"connected"`
	LatencyMS        int64           `json:"latencyMs"`
	ServerCount      int             `json:"serverCount"`
	RouteCount       int             `json:"routeCount"`
	ProxyCount       int             `json:"proxyCount"`
	Listeners        []string        `json:"listeners"`
	TLSMode          string          `json:"tlsMode"`
	AccessLogEnabled bool            `json:"accessLogEnabled"`
	Upstreams        []caddyUpstream `json:"upstreams"`
	SourceAvailable  bool            `json:"sourceAvailable"`
	SourceDigest     string          `json:"sourceDigest"`
	SourceModifiedAt *time.Time      `json:"sourceModifiedAt,omitempty"`
	SourceInSync     *bool           `json:"sourceInSync,omitempty"`
	CheckedAt        time.Time       `json:"checkedAt"`
	StatusMessage    string          `json:"statusMessage"`
}

func (s *Server) getCaddyOverview(w http.ResponseWriter, r *http.Request) {
	overview := caddyOverview{
		Listeners: []string{},
		Upstreams: []caddyUpstream{},
		TLSMode:   "disabled",
		CheckedAt: time.Now(),
	}
	var sourceContent []byte
	if content, info, err := readCaddyfile(s.cfg.CaddyfilePath); err == nil {
		sourceContent = content
		overview.SourceAvailable = true
		overview.SourceDigest = caddySourceDigest(content)
		modified := info.ModTime()
		overview.SourceModifiedAt = &modified
	}

	started := time.Now()
	configBody, _, err := s.caddyAdminRequest(r.Context(), http.MethodGet, "/config/", nil, "")
	overview.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		overview.StatusMessage = "无法连接 Caddy Admin API，请确认网关已更新并处于运行状态"
		writeJSON(w, http.StatusOK, overview)
		return
	}
	if err = summarizeCaddyConfig(configBody, &overview); err != nil {
		overview.StatusMessage = "Caddy 已连接，但当前配置无法解析"
		writeJSON(w, http.StatusOK, overview)
		return
	}
	overview.Connected = true
	overview.StatusMessage = "Caddy Admin API 已连接，当前配置可读取"
	if len(sourceContent) > 0 {
		if adapted, _, adaptErr := s.caddyAdminRequest(r.Context(), http.MethodPost, "/adapt", sourceContent, "text/caddyfile"); adaptErr == nil {
			inSync := jsonDocumentsEqual(configBody, caddyAdaptResult(adapted))
			overview.SourceInSync = &inSync
		}
	}

	if upstreamBody, _, upstreamErr := s.caddyAdminRequest(r.Context(), http.MethodGet, "/reverse_proxy/upstreams", nil, ""); upstreamErr == nil {
		var raw []struct {
			Address     string `json:"address"`
			NumRequests int    `json:"num_requests"`
			Fails       int    `json:"fails"`
		}
		if json.Unmarshal(upstreamBody, &raw) == nil {
			for _, item := range raw {
				overview.Upstreams = append(overview.Upstreams, caddyUpstream{Address: item.Address, NumRequests: item.NumRequests, Fails: item.Fails})
			}
			sort.Slice(overview.Upstreams, func(i, j int) bool { return overview.Upstreams[i].Address < overview.Upstreams[j].Address })
		}
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) validateCaddyConfig(w http.ResponseWriter, r *http.Request) {
	content, info, err := readCaddyfile(s.cfg.CaddyfilePath)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "caddyfile_unavailable", "无法读取 Caddyfile，请检查 API 容器的只读挂载")
		return
	}
	started := time.Now()
	if _, _, err = s.caddyAdminRequest(r.Context(), http.MethodPost, "/adapt", content, "text/caddyfile"); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "caddy_config_invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":       true,
		"digest":      caddySourceDigest(content),
		"modifiedAt":  info.ModTime(),
		"validatedAt": time.Now(),
		"latencyMs":   time.Since(started).Milliseconds(),
	})
}

func (s *Server) reloadCaddyConfig(w http.ResponseWriter, r *http.Request) {
	content, _, err := readCaddyfile(s.cfg.CaddyfilePath)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "caddyfile_unavailable", "无法读取 Caddyfile，请检查 API 容器的只读挂载")
		return
	}
	// Adapt first so syntax errors are reported without touching the live config.
	if _, _, err = s.caddyAdminRequest(r.Context(), http.MethodPost, "/adapt", content, "text/caddyfile"); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "caddy_config_invalid", err.Error())
		return
	}
	headers := http.Header{"Cache-Control": []string{"must-revalidate"}}
	if _, _, err = s.caddyAdminRequestWithHeaders(r.Context(), http.MethodPost, "/load", content, "text/caddyfile", headers); err != nil {
		writeError(w, http.StatusBadGateway, "caddy_reload_failed", err.Error())
		return
	}
	p, _ := r.Context().Value(principalKey).(principal)
	if s.db != nil && p.ID != "" {
		if _, auditErr := s.db.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'caddy.config.reload','caddy','gateway',$2,jsonb_build_object('digest',$3::text))`, p.ID, requestID(r.Context()), caddySourceDigest(content)); auditErr != nil {
			s.internalError(w, auditErr)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"reloaded": true, "digest": caddySourceDigest(content), "reloadedAt": time.Now()})
}

func readCaddyfile(path string) ([]byte, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	content, err := io.ReadAll(io.LimitReader(file, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, nil, fmt.Errorf("Caddyfile is empty")
	}
	return content, info, nil
}

func caddySourceDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])[:12]
}

func jsonDocumentsEqual(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// Caddy's /adapt endpoint wraps the adapted native JSON under "result".
// Keep compatibility with direct JSON responses so tests and older Caddy
// versions still compare the actual configuration document.
func caddyAdaptResult(content []byte) []byte {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(content, &envelope) == nil && len(envelope.Result) > 0 {
		return envelope.Result
	}
	return content
}

func (s *Server) caddyAdminRequest(ctx context.Context, method, path string, body []byte, contentType string) ([]byte, http.Header, error) {
	return s.caddyAdminRequestWithHeaders(ctx, method, path, body, contentType, nil)
}

func (s *Server) caddyAdminRequestWithHeaders(ctx context.Context, method, path string, body []byte, contentType string, headers http.Header) ([]byte, http.Header, error) {
	adminURL := strings.TrimRight(strings.TrimSpace(s.cfg.CaddyAdminURL), "/")
	if adminURL == "" {
		return nil, nil, fmt.Errorf("Caddy Admin API 未配置")
	}
	req, err := http.NewRequestWithContext(ctx, method, adminURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("创建 Caddy 请求失败")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	client := &http.Client{Timeout: 8 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("连接 Caddy Admin API 失败")
	}
	defer res.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxCaddyResponseBytes))
	if readErr != nil {
		return nil, nil, fmt.Errorf("读取 Caddy 响应失败")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		detail := strings.TrimSpace(string(responseBody))
		if token := strings.TrimSpace(s.cfg.RouterToken); token != "" {
			detail = strings.ReplaceAll(detail, token, "[redacted]")
		}
		if len(detail) > 600 {
			detail = detail[:600]
		}
		if detail == "" {
			detail = res.Status
		}
		return nil, res.Header, fmt.Errorf("Caddy 拒绝配置：%s", detail)
	}
	return responseBody, res.Header, nil
}

func summarizeCaddyConfig(content []byte, overview *caddyOverview) error {
	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		return err
	}
	apps, _ := root["apps"].(map[string]any)
	httpApp, _ := apps["http"].(map[string]any)
	servers, _ := httpApp["servers"].(map[string]any)
	overview.ServerCount = len(servers)
	tlsAutomatic := false
	for _, rawServer := range servers {
		server, _ := rawServer.(map[string]any)
		if listens, ok := server["listen"].([]any); ok {
			for _, raw := range listens {
				if value, ok := raw.(string); ok {
					overview.Listeners = append(overview.Listeners, value)
				}
			}
		}
		if routes, ok := server["routes"].([]any); ok {
			overview.RouteCount += len(routes)
		}
		autoHTTPS, _ := server["automatic_https"].(map[string]any)
		disabled, _ := autoHTTPS["disable"].(bool)
		if !disabled {
			for _, listener := range overview.Listeners {
				if strings.Contains(listener, ":443") {
					tlsAutomatic = true
				}
			}
		}
	}
	overview.ProxyCount = countCaddyHandlers(root, "reverse_proxy")
	overview.AccessLogEnabled = countMapKey(root, "logs") > 0
	if tlsAutomatic {
		overview.TLSMode = "automatic"
	}
	sort.Strings(overview.Listeners)
	return nil
}

func countCaddyHandlers(value any, handler string) int {
	count := 0
	switch current := value.(type) {
	case map[string]any:
		if name, _ := current["handler"].(string); name == handler {
			count++
		}
		for _, child := range current {
			count += countCaddyHandlers(child, handler)
		}
	case []any:
		for _, child := range current {
			count += countCaddyHandlers(child, handler)
		}
	}
	return count
}

func countMapKey(value any, key string) int {
	count := 0
	switch current := value.(type) {
	case map[string]any:
		if _, ok := current[key]; ok {
			count++
		}
		for _, child := range current {
			count += countMapKey(child, key)
		}
	case []any:
		for _, child := range current {
			count += countMapKey(child, key)
		}
	}
	return count
}
