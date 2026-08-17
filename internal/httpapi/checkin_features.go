package httpapi

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

var checkinMonthPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func shanghaiLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}

func checkinDate(now time.Time) string { return now.In(shanghaiLocation()).Format("2006-01-02") }

func secureReward(minimum, maximum int64) (int64, error) {
	if minimum < 0 || maximum < minimum {
		return 0, fmt.Errorf("invalid reward range")
	}
	span := maximum - minimum + 1
	value, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return 0, err
	}
	return minimum + value.Int64(), nil
}

func (s *Server) checkinSummary(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().In(shanghaiLocation()).Format("2006-01")
	}
	if !checkinMonthPattern.MatchString(month) {
		writeError(w, http.StatusBadRequest, "validation_failed", "month must use YYYY-MM format")
		return
	}
	var enabled bool
	var minimum, maximum int64
	if err := s.db.QueryRow(r.Context(), "SELECT enabled,min_reward_cents,max_reward_cents FROM daily_checkin_settings WHERE singleton").Scan(&enabled, &minimum, &maximum); err != nil {
		s.internalError(w, err)
		return
	}
	rows, err := s.db.Query(r.Context(), "SELECT checkin_date::text FROM daily_checkins WHERE user_id=$1 AND to_char(checkin_date,'YYYY-MM')=$2 ORDER BY checkin_date", p.ID, month)
	if err != nil {
		s.internalError(w, err)
		return
	}
	dates := []string{}
	for rows.Next() {
		var date string
		if err = rows.Scan(&date); err != nil {
			rows.Close()
			s.internalError(w, err)
			return
		}
		dates = append(dates, date)
	}
	rows.Close()
	var total, monthReward, totalReward int64
	if err = s.db.QueryRow(r.Context(), "SELECT count(*),COALESCE(sum(reward_cents) FILTER (WHERE to_char(checkin_date,'YYYY-MM')=$2),0),COALESCE(sum(reward_cents),0) FROM daily_checkins WHERE user_id=$1", p.ID, month).Scan(&total, &monthReward, &totalReward); err != nil {
		s.internalError(w, err)
		return
	}
	today := checkinDate(time.Now())
	checkedToday := false
	if err = s.db.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM daily_checkins WHERE user_id=$1 AND checkin_date=$2::date)", p.ID, today).Scan(&checkedToday); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "checkedInToday": checkedToday, "totalCheckins": total, "monthRewardCents": monthReward, "totalRewardCents": totalReward, "month": month, "checkedDates": dates, "minRewardCents": minimum, "maxRewardCents": maximum})
}

func (s *Server) performCheckin(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	today := checkinDate(time.Now())
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var enabled bool
	var minimum, maximum int64
	if err = tx.QueryRow(r.Context(), "SELECT enabled,min_reward_cents,max_reward_cents FROM daily_checkin_settings WHERE singleton").Scan(&enabled, &minimum, &maximum); err != nil {
		s.internalError(w, err)
		return
	}
	if !enabled {
		writeError(w, http.StatusConflict, "checkin_disabled", "daily check-in is currently disabled")
		return
	}
	var walletID string
	var balance, version int64
	if err = tx.QueryRow(r.Context(), "SELECT id,balance_cents,version FROM wallets WHERE user_id=$1 FOR UPDATE", p.ID).Scan(&walletID, &balance, &version); err != nil {
		s.internalError(w, err)
		return
	}
	var existingReward int64
	if err = tx.QueryRow(r.Context(), "SELECT reward_cents FROM daily_checkins WHERE user_id=$1 AND checkin_date=$2::date", p.ID, today).Scan(&existingReward); err == nil {
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rewardCents": existingReward, "balanceCents": balance, "checkedDate": today, "idempotent": true})
		return
	} else if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	reward, err := secureReward(minimum, maximum)
	if err != nil {
		s.internalError(w, err)
		return
	}
	newBalance := balance + reward
	var entryID int64
	err = tx.QueryRow(r.Context(), "INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,metadata) VALUES($1,'checkin_reward',$2,$3,$4,jsonb_build_object('checkin_date',$5::text,'timezone','Asia/Shanghai','minimum_cents',$6::bigint,'maximum_cents',$7::bigint)) RETURNING id", walletID, "checkin/"+today, reward, newBalance, today, minimum, maximum).Scan(&entryID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE wallets SET balance_cents=$1,version=version+1 WHERE id=$2 AND version=$3", newBalance, walletID, version); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO daily_checkins(user_id,checkin_date,reward_cents,wallet_ledger_entry_id) VALUES($1,$2::date,$3,$4)", p.ID, today, reward, entryID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"rewardCents": reward, "balanceCents": newBalance, "checkedDate": today, "idempotent": false})
}

func (s *Server) getCheckinSettings(w http.ResponseWriter, r *http.Request) {
	var enabled bool
	var minimum, maximum int64
	var updatedAt time.Time
	if err := s.db.QueryRow(r.Context(), "SELECT enabled,min_reward_cents,max_reward_cents,updated_at FROM daily_checkin_settings WHERE singleton").Scan(&enabled, &minimum, &maximum, &updatedAt); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "minRewardCents": minimum, "maxRewardCents": maximum, "updatedAt": updatedAt})
}

func (s *Server) updateCheckinSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		Enabled        bool  `json:"enabled"`
		MinRewardCents int64 `json:"minRewardCents"`
		MaxRewardCents int64 `json:"maxRewardCents"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.MinRewardCents < 1 || q.MaxRewardCents < q.MinRewardCents || q.MaxRewardCents > 10000 {
		writeError(w, 400, "validation_failed", "reward range must be between 1 and 10000 cents")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "UPDATE daily_checkin_settings SET enabled=$1,min_reward_cents=$2,max_reward_cents=$3,updated_at=now(),updated_by=$4 WHERE singleton", q.Enabled, q.MinRewardCents, q.MaxRewardCents, p.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,$1,'checkin.settings.update','daily_checkin_settings','singleton',$2,jsonb_build_object('enabled',$3::boolean,'min_reward_cents',$4::bigint,'max_reward_cents',$5::bigint))`, p.ID, requestID(r.Context()), q.Enabled, q.MinRewardCents, q.MaxRewardCents); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": q.Enabled, "minRewardCents": q.MinRewardCents, "maxRewardCents": q.MaxRewardCents})
}
