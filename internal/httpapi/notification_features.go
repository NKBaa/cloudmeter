package httpapi

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	rows, err := s.db.Query(r.Context(), `SELECT id,kind,severity,title,content,metadata,read_at,created_at FROM user_notifications WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, kind, severity, title, content string
		var metadata map[string]any
		var readAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&id, &kind, &severity, &title, &content, &metadata, &readAt, &createdAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "severity": severity, "title": title, "content": content, "metadata": metadata, "readAt": readAt, "createdAt": createdAt})
	}
	writeJSON(w, 200, map[string]any{"notifications": items})
}

func (s *Server) readNotification(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	id := r.PathValue("notificationID")
	var readAt time.Time
	err := s.db.QueryRow(r.Context(), `UPDATE user_notifications SET read_at=coalesce(read_at,now()) WHERE id=$1 AND user_id=$2 RETURNING read_at`, id, p.ID).Scan(&readAt)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "notification_not_found", "notification not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "readAt": readAt})
}
