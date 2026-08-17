package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) startImpersonation(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(principalKey).(principal)
	userID := r.PathValue("userID")
	var q struct {
		WriteEnabled bool   `json:"writeEnabled"`
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var email, displayName, status string
	var privileged bool
	if err = tx.QueryRow(r.Context(), `SELECT u.email,u.display_name,u.status,EXISTS(
		SELECT 1 FROM user_roles ur JOIN roles role ON role.id=ur.role_id
		WHERE ur.user_id=u.id AND role.code IN ('admin','super_admin'))
		FROM users u WHERE u.id=$1 FOR UPDATE`, userID).Scan(&email, &displayName, &status, &privileged); err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "user_not_found", "user not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if status != "active" {
		writeError(w, http.StatusConflict, "user_inactive", "only active users can be impersonated")
		return
	}
	if privileged {
		writeError(w, http.StatusForbidden, "privileged_impersonation_forbidden", "administrator accounts cannot be impersonated")
		return
	}
	if q.WriteEnabled && !impersonationConfirmationMatches(email, q.Confirmation) {
		writeError(w, http.StatusBadRequest, "confirmation_required", "confirm the target email to enable write access")
		return
	}

	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		s.internalError(w, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(time.Hour)
	var sessionID string
	if err = tx.QueryRow(r.Context(), `INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,$2,$3) RETURNING id`, userID, tokenHash[:], expires).Scan(&sessionID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO impersonation_sessions(session_id,actor_user_id,subject_user_id,write_enabled) VALUES($1,$2,$3,$4)`, sessionID, actor.ID, userID, q.WriteEnabled); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1::uuid,$2::uuid,'impersonation.start','user',($2::uuid)::text,$3,jsonb_build_object('write_enabled',$4::boolean,'session_id',$5::text))`, actor.ID, userID, requestID(r.Context()), q.WriteEnabled, sessionID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expiresAt": expires, "writeEnabled": q.WriteEnabled, "user": map[string]string{"id": userID, "email": email, "displayName": displayName}})
}

func impersonationConfirmationMatches(email, confirmation string) bool {
	return strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(confirmation))
}

func (s *Server) endImpersonation(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	if !p.Impersonating {
		writeError(w, http.StatusConflict, "not_impersonating", "the current session is not an impersonation session")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "UPDATE sessions SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL", p.SessionID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1::uuid,$2::uuid,'impersonation.end','user',($2::uuid)::text,$3,jsonb_build_object('session_id',$4::text))`, p.ActorID, p.ID, requestID(r.Context()), p.SessionID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ended": true})
}
