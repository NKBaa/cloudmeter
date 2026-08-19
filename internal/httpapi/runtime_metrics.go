package httpapi

import (
	"net/http"
	"time"
)

// appRuntimeMetrics returns only fresh point-in-time samples. Docker access
// remains isolated in the worker; the API reads the worker's latest sample.
func (s *Server) appRuntimeMetrics(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	rows, err := s.db.Query(r.Context(), `SELECT metrics.user_app_id::text,
		metrics.cpu_usage_cores::float8,
		metrics.memory_usage_bytes::float8/1048576,
		metrics.sampled_at
		FROM app_runtime_metrics metrics
		JOIN user_apps app ON app.id=metrics.user_app_id
		WHERE app.user_id=$1 AND app.deleted_at IS NULL AND app.status='running'
		  AND metrics.sampled_at>=now()-interval '15 seconds'
		ORDER BY metrics.user_app_id`, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type metric struct {
		AppID          string    `json:"appId"`
		CPUUsageCores  float64   `json:"cpuUsageCores"`
		MemoryUsageMiB float64   `json:"memoryUsageMiB"`
		SampledAt      time.Time `json:"sampledAt"`
	}
	items := make([]metric, 0)
	for rows.Next() {
		var item metric
		if err := rows.Scan(&item.AppID, &item.CPUUsageCores, &item.MemoryUsageMiB, &item.SampledAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"metrics": items})
}
