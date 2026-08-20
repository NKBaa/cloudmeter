package httpapi

import (
	"net/http"
	"strings"
)

type faqRequest struct {
	Question  string
	Answer    string
	Enabled   bool
	SortOrder int
}

func (s *Server) listFAQs(w http.ResponseWriter, r *http.Request)      { s.queryFAQs(w, r, true) }
func (s *Server) adminListFAQs(w http.ResponseWriter, r *http.Request) { s.queryFAQs(w, r, false) }
func (s *Server) queryFAQs(w http.ResponseWriter, r *http.Request, public bool) {
	query := "SELECT id::text,question,answer,enabled,sort_order,updated_at FROM faqs"
	if public {
		query += " WHERE enabled"
	}
	query += " ORDER BY sort_order,created_at,id"
	rows, err := s.db.Query(r.Context(), query)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, question, answer string
		var enabled bool
		var sortOrder int
		var updatedAt any
		if err = rows.Scan(&id, &question, &answer, &enabled, &sortOrder, &updatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "question": question, "answer": answer, "enabled": enabled, "sortOrder": sortOrder, "updatedAt": updatedAt})
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"faqs": items})
}
func cleanFAQ(request *faqRequest) bool {
	request.Question = strings.TrimSpace(request.Question)
	request.Answer = strings.TrimSpace(request.Answer)
	return request.Question != "" && len(request.Question) <= 300 && request.Answer != "" && len(request.Answer) <= 10000 && request.SortOrder >= -1000000 && request.SortOrder <= 1000000
}
func (s *Server) createFAQ(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var request faqRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if !cleanFAQ(&request) {
		writeError(w, 400, "validation_failed", "问题、答案或排序值无效")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	if err = tx.QueryRow(r.Context(), "INSERT INTO faqs(question,answer,enabled,sort_order,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id", request.Question, request.Answer, request.Enabled, request.SortOrder, p.ID).Scan(&id); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id) VALUES($1,'faq.create','faq',$2,$3)", p.ID, id, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}
func (s *Server) updateFAQ(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	id := r.PathValue("faqID")
	if !validUUID(id) {
		writeError(w, 400, "validation_failed", "faqID must be UUID")
		return
	}
	var request faqRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if !cleanFAQ(&request) {
		writeError(w, 400, "validation_failed", "问题、答案或排序值无效")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), "UPDATE faqs SET question=$2,answer=$3,enabled=$4,sort_order=$5,updated_at=now() WHERE id=$1", id, request.Question, request.Answer, request.Enabled, request.SortOrder)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "faq_not_found", "常见问题不存在")
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id) VALUES($1,'faq.update','faq',$2,$3)", p.ID, id, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": true})
}
func (s *Server) deleteFAQ(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	id := r.PathValue("faqID")
	if !validUUID(id) {
		writeError(w, 400, "validation_failed", "faqID must be UUID")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), "DELETE FROM faqs WHERE id=$1", id)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "faq_not_found", "常见问题不存在")
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id) VALUES($1,'faq.delete','faq',$2,$3)", p.ID, id, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}
