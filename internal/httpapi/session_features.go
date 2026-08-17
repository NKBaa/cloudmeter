package httpapi

import (
	"net/http"
)

func isSessionTerminationRequest(r *http.Request) bool {
	return (r.Method == http.MethodPost && r.URL.Path == "/api/auth/logout") ||
		(r.Method == http.MethodDelete && r.URL.Path == "/api/impersonation")
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
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
		VALUES($1::uuid,$2::uuid,'auth.logout','session',$3,$4,jsonb_build_object('impersonating',$5::boolean))`,
		p.auditActorID(), p.ID, p.SessionID, requestID(r.Context()), p.Impersonating); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
