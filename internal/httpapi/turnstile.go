package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type turnstileSettings struct {
	Enabled                bool   `json:"enabled"`
	SiteKey                string `json:"siteKey"`
	SecretConfigured       bool   `json:"secretConfigured"`
	LoginProtection        bool   `json:"loginProtection"`
	RegistrationProtection bool   `json:"registrationProtection"`
}

func (s *Server) getTurnstileSettings(w http.ResponseWriter, r *http.Request) {
	var settings turnstileSettings
	var encryptedSecret string
	if err := s.db.QueryRow(r.Context(), "SELECT turnstile_enabled,turnstile_site_key,turnstile_secret_key,turnstile_login_protection,turnstile_registration_protection FROM system_state WHERE singleton").Scan(&settings.Enabled, &settings.SiteKey, &encryptedSecret, &settings.LoginProtection, &settings.RegistrationProtection); err != nil {
		s.internalError(w, err)
		return
	}
	settings.SecretConfigured = strings.TrimSpace(encryptedSecret) != ""
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateTurnstileSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var request struct {
		Enabled                bool   `json:"enabled"`
		SiteKey                string `json:"siteKey"`
		SecretKey              string `json:"secretKey"`
		LoginProtection        bool   `json:"loginProtection"`
		RegistrationProtection bool   `json:"registrationProtection"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.SiteKey = strings.TrimSpace(request.SiteKey)
	request.SecretKey = strings.TrimSpace(request.SecretKey)
	if len(request.SiteKey) > 256 || len(request.SecretKey) > 512 {
		writeError(w, http.StatusBadRequest, "validation_failed", "Turnstile key is too long")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var encryptedSecret string
	if err = tx.QueryRow(r.Context(), "SELECT turnstile_secret_key FROM system_state WHERE singleton FOR UPDATE").Scan(&encryptedSecret); err != nil {
		s.internalError(w, err)
		return
	}
	if request.SecretKey != "" {
		encryptedSecret, err = s.secrets.Encrypt("turnstile.secret_key", request.SecretKey)
		if err != nil {
			s.internalError(w, err)
			return
		}
	}
	if request.Enabled && (request.SiteKey == "" || encryptedSecret == "") {
		writeError(w, http.StatusConflict, "turnstile_incomplete", "启用 Turnstile 前必须配置 Site Key 和 Secret Key")
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE system_state SET turnstile_enabled=$1,turnstile_site_key=$2,turnstile_secret_key=$3,turnstile_login_protection=$4,turnstile_registration_protection=$5 WHERE singleton", request.Enabled, request.SiteKey, encryptedSecret, request.LoginProtection, request.RegistrationProtection); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'turnstile.settings.update','system_state','singleton',$2,jsonb_build_object('enabled',$3::boolean,'login_protection',$4::boolean,'registration_protection',$5::boolean,'secret_updated',$6::boolean))", p.ID, requestID(r.Context()), request.Enabled, request.LoginProtection, request.RegistrationProtection, request.SecretKey != ""); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "secretConfigured": encryptedSecret != ""})
}

func (s *Server) verifyTurnstile(r *http.Request, token, purpose string) error {
	var enabled, protected bool
	var encryptedSecret string
	column := "turnstile_login_protection"
	if purpose == "registration" {
		column = "turnstile_registration_protection"
	}
	query := fmt.Sprintf("SELECT turnstile_enabled,turnstile_secret_key,%s FROM system_state WHERE singleton", column)
	if err := s.db.QueryRow(r.Context(), query).Scan(&enabled, &encryptedSecret, &protected); err != nil {
		return err
	}
	if !enabled || !protected {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("请完成人机验证")
	}
	secret, err := s.secrets.Decrypt("turnstile.secret_key", encryptedSecret)
	if err != nil {
		return err
	}
	form := url.Values{"secret": {secret}, "response": {token}}
	if clientIP := s.requestClientIP(r); clientIP != nil {
		form.Set("remoteip", clientIP.String())
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://challenges.cloudflare.com/turnstile/v0/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 8 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("Turnstile verification unavailable: %w", err)
	}
	defer response.Body.Close()
	var result struct {
		Success bool `json:"success"`
	}
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Errorf("invalid Turnstile response: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("人机验证失败或已过期，请重试")
	}
	return nil
}
