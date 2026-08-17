package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

type egressSampleRequest struct {
	SampleID   string    `json:"sampleId"`
	ByteDelta  int64     `json:"byteDelta"`
	ObservedAt time.Time `json:"observedAt"`
	Source     string    `json:"source"`
}

func (s *Server) ingestEgressSample(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(s.cfg.EgressIngestToken)
	provided := r.Header.Get("X-CloudMeter-Egress-Token")
	if token == "" || len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
		writeError(w, http.StatusForbidden, "egress_collector_forbidden", "egress collector authentication failed")
		return
	}
	var req egressSampleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	req.SampleID = strings.TrimSpace(req.SampleID)
	if req.SampleID == "" || len(req.SampleID) > 128 || req.ByteDelta < 0 || (req.Source != "egress_gateway" && req.Source != "egress_proxy") {
		writeError(w, 400, "validation_failed", "sampleId, byteDelta and source are invalid")
		return
	}
	now := time.Now().UTC()
	if req.ObservedAt.IsZero() {
		req.ObservedAt = now
	}
	if req.ObservedAt.After(now.Add(2*time.Minute)) || req.ObservedAt.Before(now.Truncate(5*time.Minute)) {
		writeError(w, 400, "validation_failed", "observedAt must belong to the current open window")
		return
	}
	appID := r.PathValue("appID")
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var appExists bool
	if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM user_apps WHERE id=$1)", appID).Scan(&appExists); err != nil {
		s.internalError(w, err)
		return
	}
	if !appExists {
		var productTestExists bool
		if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM app_product_version_tests WHERE id=$1)", appID).Scan(&productTestExists); err != nil {
			s.internalError(w, err)
			return
		}
		if !productTestExists {
			writeError(w, 404, "app_not_found", "application not found")
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"accepted": true, "testTraffic": true})
		return
	}
	if _, err = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtext($1))", appID); err != nil {
		s.internalError(w, err)
		return
	}
	var inserted bool
	var cumulative int64
	if err = tx.QueryRow(r.Context(), `INSERT INTO app_egress_cursors(user_app_id,cumulative_bytes,observed_at,source) VALUES($1,0,$2,$3)
		ON CONFLICT(user_app_id) DO UPDATE SET observed_at=GREATEST(app_egress_cursors.observed_at,EXCLUDED.observed_at),source=EXCLUDED.source,updated_at=now()
		RETURNING cumulative_bytes`, appID, req.ObservedAt, req.Source).Scan(&cumulative); err != nil {
		s.internalError(w, err)
		return
	}
	err = tx.QueryRow(r.Context(), `WITH sample AS (
		INSERT INTO app_egress_samples(sample_id,user_app_id,cumulative_bytes,byte_delta,observed_at,source)
		VALUES($1,$2,$3::bigint+$4::bigint,$4::bigint,$5,$6) ON CONFLICT(sample_id) DO NOTHING RETURNING cumulative_bytes
	) SELECT coalesce((SELECT true FROM sample),false)`, req.SampleID, appID, cumulative, req.ByteDelta, req.ObservedAt, req.Source).Scan(&inserted)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if inserted {
		if _, err = tx.Exec(r.Context(), `UPDATE app_egress_cursors SET cumulative_bytes=$2,updated_at=now() WHERE user_app_id=$1`, appID, cumulative+req.ByteDelta); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"accepted": true, "duplicate": !inserted})
}
