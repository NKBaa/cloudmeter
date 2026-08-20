package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		s.internalError(w, err)
		return
	}
	code := hex.EncodeToString(raw)
	var reward int64
	if err := s.db.QueryRow(r.Context(), "SELECT invite_reward_cents FROM system_state WHERE singleton").Scan(&reward); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err := s.db.Exec(r.Context(), "INSERT INTO user_invites(inviter_user_id,code,reward_cents) VALUES($1,$2,$3)", p.ID, code, reward); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "rewardCents": reward})
}

func (s *Server) redeemInvite(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.Code = strings.ToLower(strings.TrimSpace(q.Code))
	if len(q.Code) != 24 {
		writeError(w, 400, "validation_failed", "invite code is invalid")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var inviteID, inviterID string
	var reward int64
	err = tx.QueryRow(r.Context(), "SELECT id,inviter_user_id,reward_cents FROM user_invites WHERE code=$1 AND invited_user_id IS NULL FOR UPDATE", q.Code).Scan(&inviteID, &inviterID, &reward)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "invite_not_found", "invite code is unavailable")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if inviterID == p.ID {
		writeError(w, 400, "invite_self", "cannot redeem your own invite")
		return
	}
	var already bool
	if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM user_invites WHERE invited_user_id=$1)", p.ID).Scan(&already); err != nil {
		s.internalError(w, err)
		return
	}
	if already {
		writeError(w, 409, "invite_already_redeemed", "this account already redeemed an invite")
		return
	}
	var walletID string
	var balance int64
	if err = tx.QueryRow(r.Context(), "SELECT id,balance_cents FROM wallets WHERE user_id=$1 FOR UPDATE", inviterID).Scan(&walletID, &balance); err != nil {
		s.internalError(w, err)
		return
	}
	if reward > 0 {
		if _, err = tx.Exec(r.Context(), "INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,metadata) VALUES($1,'grant',$2,$3,$4,jsonb_build_object('source','invite','invited_user_id',$5::text))", walletID, "invite/"+inviteID, reward, balance+reward, p.ID); err != nil {
			s.internalError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), "UPDATE wallets SET balance_cents=balance_cents+$1,version=version+1 WHERE id=$2", reward, walletID); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), "UPDATE user_invites SET invited_user_id=$1,rewarded_at=now() WHERE id=$2 AND invited_user_id IS NULL", p.ID, inviteID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,$2,'invite.reward','user_invite',$3,$4,jsonb_build_object('reward_cents',$5::bigint,'invited_user_id',$1::text))`, p.ID, inviterID, inviteID, requestID(r.Context()), reward); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"rewardCents": reward, "rewardedUserId": inviterID})
}
