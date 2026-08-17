package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) adminAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeError(w, 400, "validation_failed", "limit must be between 1 and 100")
			return
		}
		limit = value
	}
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeError(w, 400, "validation_failed", "before must be a positive audit log id")
			return
		}
		before = value
	}
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	identity := strings.TrimSpace(r.URL.Query().Get("identity"))
	if len(action) > 120 || len(identity) > 160 {
		writeError(w, 400, "validation_failed", "audit filters are too long")
		return
	}

	rows, err := s.db.Query(r.Context(), `SELECT l.id,l.actor_user_id,actor.display_name,actor.email,
		l.subject_user_id,subject.display_name,subject.email,l.action,l.resource_type,l.resource_id,
		l.request_id,l.ip,l.metadata,l.created_at
		FROM audit_logs l
		LEFT JOIN users actor ON actor.id=l.actor_user_id
		LEFT JOIN users subject ON subject.id=l.subject_user_id
		WHERE ($1::bigint=0 OR l.id<$1)
		  AND ($2::text='' OR l.action ILIKE '%' || $2 || '%')
		  AND ($3::text='' OR actor.email ILIKE '%' || $3 || '%' OR actor.display_name ILIKE '%' || $3 || '%'
		       OR subject.email ILIKE '%' || $3 || '%' OR subject.display_name ILIKE '%' || $3 || '%')
		ORDER BY l.id DESC LIMIT $4`, before, action, identity, limit+1)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()

	type auditLog struct {
		ID            int64          `json:"id"`
		ActorUserID   *string        `json:"actorUserId"`
		ActorName     *string        `json:"actorName"`
		ActorEmail    *string        `json:"actorEmail"`
		SubjectUserID *string        `json:"subjectUserId"`
		SubjectName   *string        `json:"subjectName"`
		SubjectEmail  *string        `json:"subjectEmail"`
		Action        string         `json:"action"`
		ResourceType  string         `json:"resourceType"`
		ResourceID    *string        `json:"resourceId"`
		RequestID     string         `json:"requestId"`
		IP            *string        `json:"ip"`
		Metadata      map[string]any `json:"metadata"`
		CreatedAt     time.Time      `json:"createdAt"`
	}
	items := []auditLog{}
	for rows.Next() {
		var item auditLog
		if err := rows.Scan(&item.ID, &item.ActorUserID, &item.ActorName, &item.ActorEmail, &item.SubjectUserID, &item.SubjectName, &item.SubjectEmail, &item.Action, &item.ResourceType, &item.ResourceID, &item.RequestID, &item.IP, &item.Metadata, &item.CreatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var nextBefore *int64
	if hasMore && len(items) > 0 {
		value := items[len(items)-1].ID
		nextBefore = &value
	}
	writeJSON(w, 200, map[string]any{"logs": items, "nextBefore": nextBefore})
}
