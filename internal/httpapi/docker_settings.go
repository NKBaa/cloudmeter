package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

type dockerSettingsRequest struct {
	RegistryMirrors    []string `json:"registryMirrors"`
	DefaultRegistry    string   `json:"defaultRegistry"`
	RegistryUsername   string   `json:"registryUsername"`
	RegistryPassword   string   `json:"registryPassword"`
	HTTPProxy          string   `json:"httpProxy"`
	HTTPSProxy         string   `json:"httpsProxy"`
	NoProxy            string   `json:"noProxy"`
	PullTimeoutSeconds int      `json:"pullTimeoutSeconds"`
}

func (s *Server) getDockerSettings(w http.ResponseWriter, r *http.Request) {
	var mirrors, detectedMirrors []string
	var defaultRegistry, username, httpProxy, httpsProxy, noProxy string
	var detectedHTTPProxy, detectedHTTPSProxy, detectedNoProxy, lastError string
	var passwordConfigured bool
	var timeout int
	var lastCheckedAt any
	err := s.db.QueryRow(r.Context(), `SELECT registry_mirrors,default_registry,registry_username,(registry_password<>''),
		http_proxy,https_proxy,no_proxy,pull_timeout_seconds,detected_registry_mirrors,detected_http_proxy,
		detected_https_proxy,detected_no_proxy,last_checked_at,last_check_error
		FROM docker_runtime_settings WHERE singleton`).Scan(
		&mirrors, &defaultRegistry, &username, &passwordConfigured, &httpProxy, &httpsProxy, &noProxy, &timeout,
		&detectedMirrors, &detectedHTTPProxy, &detectedHTTPSProxy, &detectedNoProxy, &lastCheckedAt, &lastError,
	)
	if err != nil {
		s.internalError(w, err)
		return
	}
	for i, mirror := range detectedMirrors {
		detectedMirrors[i] = strings.TrimRight(strings.TrimSpace(mirror), "/")
	}
	desiredMirrors, actualMirrors := append([]string(nil), mirrors...), append([]string(nil), detectedMirrors...)
	slices.Sort(desiredMirrors)
	slices.Sort(actualMirrors)
	restartRequired := !slices.Equal(desiredMirrors, actualMirrors) ||
		httpProxy != detectedHTTPProxy || httpsProxy != detectedHTTPSProxy || noProxy != detectedNoProxy
	generated := map[string]any{"registry-mirrors": mirrors}
	if httpProxy != "" || httpsProxy != "" || noProxy != "" {
		generated["proxies"] = map[string]string{"http-proxy": httpProxy, "https-proxy": httpsProxy, "no-proxy": noProxy}
	}
	daemonJSON, _ := json.MarshalIndent(generated, "", "  ")
	writeJSON(w, http.StatusOK, map[string]any{
		"registryMirrors": mirrors, "defaultRegistry": defaultRegistry, "registryUsername": username,
		"registryPasswordConfigured": passwordConfigured, "httpProxy": httpProxy, "httpsProxy": httpsProxy,
		"noProxy": noProxy, "pullTimeoutSeconds": timeout, "detectedRegistryMirrors": detectedMirrors,
		"detectedHttpProxy": detectedHTTPProxy, "detectedHttpsProxy": detectedHTTPSProxy,
		"detectedNoProxy": detectedNoProxy, "lastCheckedAt": lastCheckedAt, "lastCheckError": lastError,
		"daemonRestartRequired": restartRequired, "daemonJson": string(daemonJSON),
	})
}

func (s *Server) updateDockerSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q dockerSettingsRequest
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var validationErr error
	if q.RegistryMirrors, validationErr = normalizedMirrorURLs(q.RegistryMirrors); validationErr != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", validationErr.Error())
		return
	}
	q.DefaultRegistry = strings.Trim(strings.TrimSpace(q.DefaultRegistry), "/")
	q.RegistryUsername = strings.TrimSpace(q.RegistryUsername)
	q.NoProxy = strings.TrimSpace(q.NoProxy)
	if !validRegistryPrefix(q.DefaultRegistry) {
		writeError(w, http.StatusBadRequest, "validation_failed", "默认镜像库应填写 registry.example.com 或 registry.example.com/namespace，不含协议")
		return
	}
	if len(q.RegistryUsername) > 256 || len(q.RegistryPassword) > 4096 || len(q.NoProxy) > 4096 {
		writeError(w, http.StatusBadRequest, "validation_failed", "Docker 配置超过长度限制")
		return
	}
	if q.PullTimeoutSeconds < 30 || q.PullTimeoutSeconds > 1800 {
		writeError(w, http.StatusBadRequest, "validation_failed", "镜像拉取超时必须在 30 到 1800 秒之间")
		return
	}
	if q.HTTPProxy, validationErr = normalizedProxyURL(q.HTTPProxy); validationErr != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "HTTP 代理："+validationErr.Error())
		return
	}
	if q.HTTPSProxy, validationErr = normalizedProxyURL(q.HTTPSProxy); validationErr != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "HTTPS 代理："+validationErr.Error())
		return
	}
	if (q.RegistryUsername != "" || q.RegistryPassword != "") && q.DefaultRegistry == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "配置 Registry 凭据前必须填写默认镜像库")
		return
	}
	encryptedPassword := ""
	if q.RegistryPassword != "" {
		var err error
		encryptedPassword, err = s.secrets.Encrypt("docker.registry.password", q.RegistryPassword)
		if err != nil {
			s.internalError(w, err)
			return
		}
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `UPDATE docker_runtime_settings SET registry_mirrors=$1,default_registry=$2,registry_username=$3,
		registry_password=CASE WHEN $4='' THEN registry_password ELSE $4 END,http_proxy=$5,https_proxy=$6,no_proxy=$7,
		pull_timeout_seconds=$8,updated_at=now(),updated_by=$9 WHERE singleton`, q.RegistryMirrors, q.DefaultRegistry,
		q.RegistryUsername, encryptedPassword, q.HTTPProxy, q.HTTPSProxy, q.NoProxy, q.PullTimeoutSeconds, p.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if q.RegistryUsername == "" && q.RegistryPassword == "" {
		if _, err = tx.Exec(r.Context(), "UPDATE docker_runtime_settings SET registry_password='' WHERE singleton"); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'docker.settings.update','docker_runtime_settings','singleton',$2,jsonb_build_object(
		'registry_mirror_count',$3::int,'default_registry',$4::text,'registry_username_configured',$5::boolean,
		'http_proxy_configured',$6::boolean,'https_proxy_configured',$7::boolean,'pull_timeout_seconds',$8::int))`,
		p.ID, requestID(r.Context()), len(q.RegistryMirrors), q.DefaultRegistry, q.RegistryUsername != "", q.HTTPProxy != "", q.HTTPSProxy != "", q.PullTimeoutSeconds); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "daemonRestartRequired": true})
}

func normalizedMirrorURLs(values []string) ([]string, error) {
	if len(values) > 16 {
		return nil, &settingsValidationError{"镜像加速源最多 16 个"}
	}
	result, seen := make([]string, 0, len(values)), map[string]bool{}
	for _, value := range values {
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, &settingsValidationError{"镜像加速源必须是无凭据的 HTTPS 地址"}
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
}

func validRegistryPrefix(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 512 || strings.ContainsAny(value, " \t\r\n@?#") || strings.Contains(value, "://") || strings.HasPrefix(value, "/") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func normalizedProxyURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", &settingsValidationError{"必须是无内嵌账号密码的 HTTP(S) 地址"}
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", &settingsValidationError{"不能包含查询参数或片段"}
	}
	return value, nil
}

type settingsValidationError struct{ message string }

func (e *settingsValidationError) Error() string { return e.message }
