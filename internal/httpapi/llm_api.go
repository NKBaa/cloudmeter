package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type llmAPIKeyStatus struct {
	Configured bool       `json:"configured"`
	Name       string     `json:"name,omitempty"`
	Prefix     string     `json:"prefix,omitempty"`
	CreatedAt  *time.Time `json:"createdAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

func (s *Server) getLLMAPIKey(w http.ResponseWriter, r *http.Request) {
	var status llmAPIKeyStatus
	err := s.db.QueryRow(r.Context(), `SELECT name,token_prefix,created_at,last_used_at FROM llm_api_keys WHERE singleton AND revoked_at IS NULL`).
		Scan(&status.Name, &status.Prefix, &status.CreatedAt, &status.LastUsedAt)
	if err == pgx.ErrNoRows {
		writeJSON(w, http.StatusOK, status)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	status.Configured = true
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) rotateLLMAPIKey(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" {
		q.Name = "默认大模型密钥"
	}
	if len([]rune(q.Name)) > 64 {
		writeError(w, http.StatusBadRequest, "validation_failed", "密钥名称不能超过 64 个字符")
		return
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		s.internalError(w, err)
		return
	}
	token := "cm_llm_" + base64.RawURLEncoding.EncodeToString(random)
	hash := sha256.Sum256([]byte(token))
	prefix := token[:15]
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `INSERT INTO llm_api_keys(singleton,name,token_hash,token_prefix,created_by,created_at,last_used_at,revoked_at)
		VALUES(true,$1,$2,$3,$4,now(),NULL,NULL) ON CONFLICT(singleton) DO UPDATE SET name=$1,token_hash=$2,token_prefix=$3,created_by=$4,created_at=now(),last_used_at=NULL,revoked_at=NULL`, q.Name, hash[:], prefix, p.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'llm.api-key.rotate','llm_api_key','singleton',$2,jsonb_build_object('name',$3::text,'prefix',$4::text))`, p.ID, requestID(r.Context()), q.Name, prefix); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": token, "status": llmAPIKeyStatus{Configured: true, Name: q.Name, Prefix: prefix}})
}

func (s *Server) revokeLLMAPIKey(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `UPDATE llm_api_keys SET revoked_at=now() WHERE singleton AND revoked_at IS NULL`); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id) VALUES($1,'llm.api-key.revoke','llm_api_key','singleton',$2)`, p.ID, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authenticateLLM(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(auth, "Bearer cm_llm_") {
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "有效的大模型 API 密钥是必需的")
			return
		}
		hash := sha256.Sum256([]byte(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))))
		var p principal
		err := s.db.QueryRow(r.Context(), `SELECT key.created_by,u.email,u.display_name FROM llm_api_keys key JOIN users u ON u.id=key.created_by WHERE key.singleton AND key.token_hash=$1 AND key.revoked_at IS NULL AND u.status='active'`, hash[:]).Scan(&p.ID, &p.Email, &p.DisplayName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "大模型 API 密钥无效或已撤销")
			return
		}
		p.Roles = []string{"llm_api"}
		_, _ = s.db.Exec(r.Context(), `UPDATE llm_api_keys SET last_used_at=now() WHERE singleton`)
		ctx := context.WithValue(r.Context(), principalKey, p)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) llmSystemAnalysis(w http.ResponseWriter, r *http.Request) {
	var users, apps, runningApps, pendingJobs, auditFailures int64
	err := s.db.QueryRow(r.Context(), `SELECT
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM user_apps),
		(SELECT count(*) FROM user_apps WHERE status='running'),
		(SELECT count(*) FROM deployment_jobs WHERE state NOT IN ('succeeded','failed')),
		(SELECT count(*) FROM audit_logs WHERE created_at>now()-interval '24 hours' AND (action ILIKE '%fail%' OR action ILIKE '%error%'))`).
		Scan(&users, &apps, &runningApps, &pendingJobs, &auditFailures)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var settings SystemSettingsResponse
	if err = s.db.QueryRow(r.Context(), `SELECT system_name,server_url,coalesce(app_base_domain,''),logo_url,footer_text,about_content,homepage_content,terms_of_service,privacy_policy,updated_at FROM system_settings WHERE singleton`).Scan(&settings.SystemName, &settings.ServerURL, &settings.AppBaseDomain, &settings.LogoURL, &settings.FooterText, &settings.AboutContent, &settings.HomepageContent, &settings.TermsOfService, &settings.PrivacyPolicy, &settings.UpdatedAt); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"generatedAt": time.Now(), "system": settings, "metrics": map[string]int64{"users": users, "applications": apps, "runningApplications": runningApps, "pendingDeploymentJobs": pendingJobs, "auditFailuresLast24Hours": auditFailures}})
}

func (s *Server) llmGetSystemSettings(w http.ResponseWriter, r *http.Request) {
	s.getSystemSettings(w, r)
}

func (s *Server) llmPatchSystemSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		SystemName      *string `json:"systemName"`
		LogoURL         *string `json:"logoUrl"`
		FooterText      *string `json:"footerText"`
		AboutContent    *string `json:"aboutContent"`
		HomepageContent *string `json:"homepageContent"`
		TermsOfService  *string `json:"termsOfService"`
		PrivacyPolicy   *string `json:"privacyPolicy"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	fields := []string{}
	args := []any{}
	add := func(column string, value *string, trim bool) {
		if value != nil {
			fields = append(fields, fmt.Sprintf("%s=$%d", column, len(args)+1))
			if trim {
				args = append(args, strings.TrimSpace(*value))
			} else {
				args = append(args, *value)
			}
		}
	}
	add("system_name", q.SystemName, true)
	add("logo_url", q.LogoURL, true)
	add("footer_text", q.FooterText, false)
	add("about_content", q.AboutContent, false)
	add("homepage_content", q.HomepageContent, false)
	add("terms_of_service", q.TermsOfService, false)
	add("privacy_policy", q.PrivacyPolicy, false)
	if len(fields) == 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "至少提供一个允许修改的字段")
		return
	}
	if q.SystemName != nil && (strings.TrimSpace(*q.SystemName) == "" || len([]rune(strings.TrimSpace(*q.SystemName))) > 64) {
		writeError(w, http.StatusBadRequest, "validation_failed", "系统名称必须在 1 到 64 个字符之间")
		return
	}
	args = append(args, p.ID)
	query := fmt.Sprintf("UPDATE system_settings SET %s,updated_at=now(),updated_by=$%d WHERE singleton", strings.Join(fields, ","), len(args))
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), query, args...); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'llm.system.settings.update','system_settings','singleton',$2,jsonb_build_object('fields',$3::text))`, p.ID, requestID(r.Context()), strings.Join(fields, ",")); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	s.getSystemSettings(w, r)
}
