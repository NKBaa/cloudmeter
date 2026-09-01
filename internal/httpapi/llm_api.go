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

func (s *Server) llmListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT id::text,email,display_name,status,created_at,updated_at FROM users ORDER BY created_at DESC LIMIT 100")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, email, name, status string
		var created, updated time.Time
		if err := rows.Scan(&id, &email, &name, &status, &created, &updated); err != nil {
			s.internalError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "email": email, "displayName": name, "status": status, "createdAt": created, "updatedAt": updated})
	}
	writeJSON(w, 200, map[string]any{"users": out})
}

func (s *Server) llmPatchUser(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	id := r.PathValue("userID")
	if !validUUID(id) {
		writeError(w, 400, "validation_failed", "userID must be a UUID")
		return
	}
	var q struct {
		DisplayName *string
		Status      *string
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.DisplayName == nil && q.Status == nil {
		writeError(w, 400, "validation_failed", "provide displayName or status")
		return
	}
	if q.Status != nil && *q.Status != "active" && *q.Status != "suspended" {
		writeError(w, 400, "validation_failed", "status must be active or suspended")
		return
	}
	if q.Status != nil && *q.Status == "suspended" && id == p.ID {
		writeError(w, http.StatusConflict, "cannot_suspend_self", "不能暂停当前 API 所属管理员")
		return
	}
	fields := []string{}
	args := []any{}
	if q.DisplayName != nil {
		fields = append(fields, fmt.Sprintf("display_name=$%d", len(args)+1))
		args = append(args, strings.TrimSpace(*q.DisplayName))
	}
	if q.Status != nil {
		fields = append(fields, fmt.Sprintf("status=$%d", len(args)+1))
		args = append(args, *q.Status)
	}
	args = append(args, id)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if q.Status != nil && *q.Status == "suspended" {
		var privileged, activeSuperAdmins int
		if err = tx.QueryRow(r.Context(), `SELECT CASE WHEN EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$1 AND r.code='super_admin') THEN 1 ELSE 0 END`, id).Scan(&privileged); err != nil {
			s.internalError(w, err)
			return
		}
		if privileged == 1 {
			if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active'`).Scan(&activeSuperAdmins); err != nil {
				s.internalError(w, err)
				return
			}
			if activeSuperAdmins <= 1 {
				writeError(w, http.StatusConflict, "last_super_admin", "不能暂停最后一个活跃超级管理员")
				return
			}
		}
	}
	res, err := tx.Exec(r.Context(), fmt.Sprintf("UPDATE users SET %s,updated_at=now() WHERE id=$%d", strings.Join(fields, ","), len(args)), args...)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if res.RowsAffected() == 0 {
		writeError(w, 404, "user_not_found", "user not found")
		return
	}
	if q.Status != nil && *q.Status == "suspended" {
		_, _ = tx.Exec(r.Context(), "UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL", id)
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id) VALUES($1,$2,'llm.user.update','user',$2,$3)", p.ID, id, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": true, "userId": id})
}

func (s *Server) llmListAuditLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT id,action,resource_type,resource_id,request_id,metadata,created_at FROM audit_logs ORDER BY id DESC LIMIT 100")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var action, typ, req string
		var rid *string
		var metadata map[string]any
		var created time.Time
		if err := rows.Scan(&id, &action, &typ, &rid, &req, &metadata, &created); err != nil {
			s.internalError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "action": action, "resourceType": typ, "resourceId": rid, "requestId": req, "metadata": metadata, "createdAt": created})
	}
	writeJSON(w, 200, map[string]any{"logs": out})
}
func (s *Server) llmClearRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	s.clearRuntimeLogs(w, r)
}
func (s *Server) llmDatabaseSummary(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT table_name FROM information_schema.tables WHERE table_schema='public' ORDER BY table_name")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			s.internalError(w, err)
			return
		}
		out = append(out, n)
	}
	writeJSON(w, 200, map[string]any{"database": "postgresql", "tables": out, "policy": "仅允许版本化白名单维护，不提供任意 SQL"})
}
func (s *Server) llmDatabaseMaintenance(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct{ Operation string }
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.Operation != "analyze" {
		writeError(w, 400, "validation_failed", "operation must be analyze")
		return
	}
	if _, err := s.db.Exec(r.Context(), "ANALYZE"); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err := s.db.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id) VALUES($1,'llm.database.maintenance','database','public',$2)", p.ID, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"completed": true, "operation": q.Operation})
}
