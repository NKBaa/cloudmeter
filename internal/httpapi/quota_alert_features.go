package httpapi

import (
	"net/http"
	"time"
)

func (s *Server) getBalanceAlertSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	_, _ = s.db.Exec(r.Context(), "INSERT INTO balance_alert_settings(user_id) VALUES($1) ON CONFLICT DO NOTHING", p.ID)
	var enabled bool
	var threshold int64
	var cooldown int
	var updated time.Time
	if err := s.db.QueryRow(r.Context(), "SELECT enabled,threshold_cents,cooldown_hours,updated_at FROM balance_alert_settings WHERE user_id=$1", p.ID).Scan(&enabled, &threshold, &cooldown, &updated); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": enabled, "thresholdCents": threshold, "cooldownHours": cooldown, "updatedAt": updated})
}

func (s *Server) updateBalanceAlertSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		Enabled        bool  `json:"enabled"`
		ThresholdCents int64 `json:"thresholdCents"`
		CooldownHours  int   `json:"cooldownHours"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.ThresholdCents < 0 || q.ThresholdCents > 100000000 || q.CooldownHours < 1 || q.CooldownHours > 720 {
		writeError(w, 400, "validation_failed", "threshold or cooldown is invalid")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), "INSERT INTO balance_alert_settings(user_id,enabled,threshold_cents,cooldown_hours,updated_at) VALUES($1,$2,$3,$4,now()) ON CONFLICT(user_id) DO UPDATE SET enabled=$2,threshold_cents=$3,cooldown_hours=$4,updated_at=now(),below_threshold=false", p.ID, q.Enabled, q.ThresholdCents, q.CooldownHours)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,$1,'balance-alert.settings.update','balance_alert_settings',$1::text,$2,jsonb_build_object('enabled',$3::boolean,'threshold_cents',$4::bigint,'cooldown_hours',$5::int))`, p.ID, requestID(r.Context()), q.Enabled, q.ThresholdCents, q.CooldownHours); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": q.Enabled, "thresholdCents": q.ThresholdCents, "cooldownHours": q.CooldownHours})
}

func (s *Server) getQuotaSettings(w http.ResponseWriter, r *http.Request) {
	var initial, invite int64
	if err := s.db.QueryRow(r.Context(), "SELECT initial_grant_cents,invite_reward_cents FROM system_state WHERE singleton").Scan(&initial, &invite); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"initialGrantCents": initial, "inviteRewardCents": invite})
}

func (s *Server) updateQuotaSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		InitialGrantCents int64 `json:"initialGrantCents"`
		InviteRewardCents int64 `json:"inviteRewardCents"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.InitialGrantCents < 0 || q.InviteRewardCents < 0 || q.InitialGrantCents > 10000000000 || q.InviteRewardCents > 10000000000 {
		writeError(w, 400, "validation_failed", "quota values are invalid")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "UPDATE system_state SET initial_grant_cents=$1,invite_reward_cents=$2 WHERE singleton", q.InitialGrantCents, q.InviteRewardCents); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,$1,'quota.settings.update','system_state','singleton',$2,jsonb_build_object('initial_grant_cents',$3::bigint,'invite_reward_cents',$4::bigint))`, p.ID, requestID(r.Context()), q.InitialGrantCents, q.InviteRewardCents); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"initialGrantCents": q.InitialGrantCents, "inviteRewardCents": q.InviteRewardCents})
}
