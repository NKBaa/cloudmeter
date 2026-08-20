package httpapi

import (
	"net/http"
	"strings"
)

func (s *Server) listDockerImages(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT image_id,repo_tags,size_bytes,created_at,container_references,sampled_at FROM docker_image_inventory ORDER BY container_references DESC,size_bytes DESC")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id string
		var tags []string
		var size, refs int64
		var created, sampled any
		if err = rows.Scan(&id, &tags, &size, &created, &refs, &sampled); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "repoTags": tags, "sizeBytes": size, "createdAt": created, "containerReferences": refs, "sampledAt": sampled})
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	var jobs []map[string]any = []map[string]any{}
	jobRows, err := s.db.Query(r.Context(), "SELECT id::text,image_id,status,last_error,created_at,completed_at FROM docker_image_deletion_jobs ORDER BY created_at DESC LIMIT 30")
	if err == nil {
		defer jobRows.Close()
		for jobRows.Next() {
			var id, image, status, last string
			var created, completed any
			if jobRows.Scan(&id, &image, &status, &last, &created, &completed) == nil {
				jobs = append(jobs, map[string]any{"id": id, "imageId": image, "status": status, "lastError": last, "createdAt": created, "completedAt": completed})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"images": items, "deletionJobs": jobs})
}
func (s *Server) deleteDockerImage(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	id := strings.TrimSpace(r.PathValue("imageID"))
	if !strings.HasPrefix(id, "sha256:") || len(id) != 71 {
		writeError(w, 400, "validation_failed", "invalid Docker image ID")
		return
	}
	var q struct{ Confirmation string }
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.Confirmation != id[len(id)-12:] {
		writeError(w, 400, "confirmation_mismatch", "请输入镜像 ID 末 12 位确认删除")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var refs int
	if err = tx.QueryRow(r.Context(), "SELECT container_references FROM docker_image_inventory WHERE image_id=$1 FOR UPDATE", id).Scan(&refs); err != nil {
		writeError(w, 404, "docker_image_not_found", "镜像不存在或库存尚未同步")
		return
	}
	if refs > 0 {
		writeError(w, 409, "docker_image_in_use", "镜像仍被容器引用，不能删除")
		return
	}
	var job string
	if err = tx.QueryRow(r.Context(), "INSERT INTO docker_image_deletion_jobs(image_id,requested_by) VALUES($1,$2) RETURNING id", id, p.ID).Scan(&job); err != nil {
		writeError(w, 409, "docker_image_delete_in_progress", "该镜像已有删除任务")
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'docker.image.delete.request','docker_image',$2,$3,jsonb_build_object('job_id',$4::text))", p.ID, id, requestID(r.Context()), job); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"jobId": job, "status": "queued"})
}
