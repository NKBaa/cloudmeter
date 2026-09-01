package httpapi

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var appBaseDomainLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type SystemSettingsResponse struct {
	SystemName      string    `json:"systemName"`
	ServerURL       string    `json:"serverUrl"`
	AppBaseDomain   string    `json:"appBaseDomain"`
	LogoURL         string    `json:"logoUrl"`
	FooterText      string    `json:"footerText"`
	AboutContent    string    `json:"aboutContent"`
	HomepageContent string    `json:"homepageContent"`
	TermsOfService  string    `json:"termsOfService"`
	PrivacyPolicy   string    `json:"privacyPolicy"`
	HostPortMin     int       `json:"hostPortMin"`
	HostPortMax     int       `json:"hostPortMax"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func (s *Server) getSystemSettingsPublic(w http.ResponseWriter, r *http.Request) {
	var res SystemSettingsResponse
	err := s.db.QueryRow(r.Context(), "SELECT system_name, server_url, coalesce(app_base_domain, ''), logo_url, footer_text, about_content, homepage_content, terms_of_service, privacy_policy, host_port_min, host_port_max, updated_at FROM system_settings WHERE singleton").
		Scan(&res.SystemName, &res.ServerURL, &res.AppBaseDomain, &res.LogoURL, &res.FooterText, &res.AboutContent, &res.HomepageContent, &res.TermsOfService, &res.PrivacyPolicy, &res.HostPortMin, &res.HostPortMax, &res.UpdatedAt)

	if err != nil {
		res.SystemName = "CloudMeter"
	} else if res.SystemName == "" {
		res.SystemName = "CloudMeter"
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) getSystemSettings(w http.ResponseWriter, r *http.Request) {
	var res SystemSettingsResponse
	err := s.db.QueryRow(r.Context(), "SELECT system_name, server_url, coalesce(app_base_domain, ''), logo_url, footer_text, about_content, homepage_content, terms_of_service, privacy_policy, host_port_min, host_port_max, updated_at FROM system_settings WHERE singleton").
		Scan(&res.SystemName, &res.ServerURL, &res.AppBaseDomain, &res.LogoURL, &res.FooterText, &res.AboutContent, &res.HomepageContent, &res.TermsOfService, &res.PrivacyPolicy, &res.HostPortMin, &res.HostPortMax, &res.UpdatedAt)

	if err != nil {
		s.internalError(w, err)
		return
	}
	if res.SystemName == "" {
		res.SystemName = "CloudMeter"
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) updateSystemSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		SystemName      string `json:"systemName"`
		ServerURL       string `json:"serverUrl"`
		AppBaseDomain   string `json:"appBaseDomain"`
		LogoURL         string `json:"logoUrl"`
		FooterText      string `json:"footerText"`
		AboutContent    string `json:"aboutContent"`
		HomepageContent string `json:"homepageContent"`
		TermsOfService  string `json:"termsOfService"`
		PrivacyPolicy   string `json:"privacyPolicy"`
		HostPortMin     int    `json:"hostPortMin"`
		HostPortMax     int    `json:"hostPortMax"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.SystemName = strings.TrimSpace(q.SystemName)
	if q.SystemName == "" || len([]rune(q.SystemName)) > 64 {
		writeError(w, 400, "validation_failed", "系统名称必须在 1 到 64 个字符之间")
		return
	}
	serverURL, err := normalizePublicBaseURL(q.ServerURL)
	if err != nil {
		writeError(w, 400, "validation_failed", "服务器地址必须是仅包含协议和主机的 HTTP(S) 地址")
		return
	}
	q.ServerURL = serverURL
	appBaseDomain, err := normalizeAppBaseDomain(q.AppBaseDomain)
	if err != nil {
		writeError(w, 400, "validation_failed", err.Error())
		return
	}
	q.AppBaseDomain = appBaseDomain
	if q.HostPortMin < 1 || q.HostPortMax > 65535 || q.HostPortMin > q.HostPortMax {
		writeError(w, 400, "validation_failed", "应用分配端口范围必须是 1 到 65535，且起始端口不得大于结束端口")
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var previousAppBaseDomain string
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(app_base_domain,'') FROM system_settings WHERE singleton FOR UPDATE`).Scan(&previousAppBaseDomain); err != nil {
		s.internalError(w, err)
		return
	}

	if _, err = tx.Exec(r.Context(), `
		INSERT INTO system_settings(singleton, system_name, server_url, app_base_domain, logo_url, footer_text, about_content, homepage_content, terms_of_service, privacy_policy, host_port_min, host_port_max, updated_at, updated_by)
		VALUES (true, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now(), $12)
		ON CONFLICT (singleton) DO UPDATE SET
			system_name=$1, server_url=$2, app_base_domain=$3, logo_url=$4, footer_text=$5, about_content=$6, homepage_content=$7, terms_of_service=$8, privacy_policy=$9,
			host_port_min=$10, host_port_max=$11, updated_at=now(), updated_by=$12
	`, q.SystemName, q.ServerURL, q.AppBaseDomain, q.LogoURL, q.FooterText, q.AboutContent, q.HomepageContent, q.TermsOfService, q.PrivacyPolicy, q.HostPortMin, q.HostPortMax, p.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE app_routes route
		SET public_path=CASE
			WHEN $1='' THEN '/apps/'||users.slug||'/'||app.slug
			ELSE '//'||app.route_host_label||'.'||$1||'/'
		END
		FROM user_apps app JOIN users ON users.id=app.user_id
		WHERE route.user_app_id=app.id`, q.AppBaseDomain); err != nil {
		s.internalError(w, err)
		return
	}
	if previousAppBaseDomain != q.AppBaseDomain {
		if _, err = tx.Exec(r.Context(), `DELETE FROM app_access_grants`); err != nil {
			s.internalError(w, err)
			return
		}
	}

	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'system.settings.update','system_settings','singleton',$2,jsonb_build_object('system_name',$3::text))`, p.ID, requestID(r.Context()), q.SystemName); err != nil {
		s.internalError(w, err)
		return
	}

	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, SystemSettingsResponse{
		SystemName:      q.SystemName,
		ServerURL:       q.ServerURL,
		AppBaseDomain:   q.AppBaseDomain,
		LogoURL:         q.LogoURL,
		FooterText:      q.FooterText,
		AboutContent:    q.AboutContent,
		HomepageContent: q.HomepageContent,
		TermsOfService:  q.TermsOfService,
		PrivacyPolicy:   q.PrivacyPolicy,
		HostPortMin:     q.HostPortMin,
		HostPortMax:     q.HostPortMax,
		UpdatedAt:       time.Now(),
	})
}

func normalizeAppBaseDomain(raw string) (string, error) {
	value := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if value == "" {
		return "", nil
	}
	if len(value) > 253 || strings.ContainsAny(value, "/:*?#@ \t\r\n") {
		return "", fmt.Errorf("应用泛子域名只能填写纯域名，不能包含协议、端口、路径或通配符")
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if !appBaseDomainLabelPattern.MatchString(label) {
			return "", fmt.Errorf("应用泛子域名格式无效")
		}
	}
	return value, nil
}
