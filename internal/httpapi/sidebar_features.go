package httpapi

import (
	"net/http"
	"sort"
)

var sidebarMenuKeys = map[string]bool{"overview": true, "deploy": true, "apps": true, "releases": true, "backups": true, "billing": true, "recharge": true, "checkin": true, "usage": true, "tickets": true, "faq": true}

func (s *Server) sidebarVisibility(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT menu_key,visible FROM sidebar_visibility ORDER BY menu_key")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	values := map[string]bool{}
	for rows.Next() {
		var key string
		var visible bool
		if err = rows.Scan(&key, &visible); err != nil {
			s.internalError(w, err)
			return
		}
		values[key] = visible
	}
	writeJSON(w, 200, map[string]any{"visibility": values})
}
func (s *Server) updateSidebarVisibility(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct{ Visibility map[string]bool }
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if len(q.Visibility) != len(sidebarMenuKeys) {
		writeError(w, 400, "validation_failed", "必须提交全部侧栏菜单状态")
		return
	}
	keys := make([]string, 0, len(q.Visibility))
	for key := range q.Visibility {
		if !sidebarMenuKeys[key] {
			writeError(w, 400, "validation_failed", "包含未知侧栏菜单")
			return
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	for _, key := range keys {
		if _, err = tx.Exec(r.Context(), "INSERT INTO sidebar_visibility(menu_key,visible,updated_by,updated_at) VALUES($1,$2,$3,now()) ON CONFLICT(menu_key) DO UPDATE SET visible=EXCLUDED.visible,updated_by=EXCLUDED.updated_by,updated_at=now()", key, q.Visibility[key], p.ID); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'sidebar.visibility.update','sidebar_visibility','user_console',$2,jsonb_build_object('visibility',$3::jsonb))", p.ID, requestID(r.Context()), q.Visibility); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": true})
}
