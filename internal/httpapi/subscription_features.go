package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type subscriptionPlan struct {
	PlanID          string         `json:"planId"`
	PlanVersionID   string         `json:"planVersionId"`
	Code            string         `json:"code"`
	Name            string         `json:"name"`
	Version         int            `json:"version"`
	CyclePriceCents int64          `json:"cyclePriceCents"`
	Entitlements    map[string]any `json:"entitlements"`
	EffectiveAt     time.Time      `json:"effectiveAt"`
	Current         bool           `json:"current"`
	PurchaseAction  string         `json:"purchaseAction"`
	PayableCents    int64          `json:"payableCents"`
}

type currentSubscription struct {
	PlanID          string         `json:"planId"`
	PlanVersionID   string         `json:"planVersionId"`
	Code            string         `json:"code"`
	Name            string         `json:"name"`
	Status          string         `json:"status"`
	CyclePriceCents int64          `json:"cyclePriceCents"`
	Entitlements    map[string]any `json:"entitlements"`
	StartsAt        time.Time      `json:"startsAt"`
	EndsAt          *time.Time     `json:"endsAt"`
	GraceEndsAt     *time.Time     `json:"graceEndsAt"`
}

type subscriptionPurchase struct {
	ID                 string     `json:"id"`
	PlanVersionID      string     `json:"planVersionId"`
	PlanCode           string     `json:"planCode"`
	PlanName           string     `json:"planName"`
	Action             string     `json:"action"`
	Status             string     `json:"status"`
	AmountCents        int64      `json:"amountCents"`
	BalanceAfterCents  int64      `json:"balanceAfterCents"`
	ServicePeriodStart time.Time  `json:"servicePeriodStart"`
	ServicePeriodEnd   time.Time  `json:"servicePeriodEnd"`
	SubscriptionEndsAt *time.Time `json:"subscriptionEndsAt"`
	CreatedAt          time.Time  `json:"createdAt"`
}

type subscriptionQuote struct {
	Action             string
	AmountCents        int64
	ServicePeriodStart time.Time
	ServicePeriodEnd   time.Time
	SubscriptionEndsAt *time.Time
}

func addCalendarMonth(value time.Time) time.Time {
	value = value.UTC()
	year, month, day := value.Date()
	nextMonth := time.Date(year, month+1, 1, value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), time.UTC)
	lastDay := nextMonth.AddDate(0, 1, -1).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(nextMonth.Year(), nextMonth.Month(), day, value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), time.UTC)
}

func quoteSubscription(now time.Time, current *currentSubscription, targetPlanID, targetVersionID string, targetPrice, paidBaseline int64) subscriptionQuote {
	now = now.UTC()
	activeFinite := current != nil && current.Status == "active" && current.EndsAt != nil && current.EndsAt.After(now)
	if activeFinite && current.PlanVersionID == targetVersionID {
		start := current.EndsAt.UTC()
		end := addCalendarMonth(start)
		return subscriptionQuote{Action: "renewal", AmountCents: targetPrice, ServicePeriodStart: start, ServicePeriodEnd: end, SubscriptionEndsAt: &end}
	}
	if activeFinite {
		action := "change"
		if targetPrice > current.CyclePriceCents {
			action = "upgrade"
		} else if targetPrice < current.CyclePriceCents {
			action = "downgrade"
		}
		amount := targetPrice - paidBaseline
		if amount < 0 {
			amount = 0
		}
		end := current.EndsAt.UTC()
		var subscriptionEnd *time.Time
		if targetPrice > 0 {
			subscriptionEnd = &end
		}
		return subscriptionQuote{Action: action, AmountCents: amount, ServicePeriodStart: now, ServicePeriodEnd: end, SubscriptionEndsAt: subscriptionEnd}
	}

	action := "purchase"
	if current != nil && current.PlanID == targetPlanID {
		action = "renewal"
	} else if current != nil {
		if targetPrice > current.CyclePriceCents {
			action = "upgrade"
		} else if targetPrice < current.CyclePriceCents {
			action = "downgrade"
		} else {
			action = "change"
		}
	}
	end := addCalendarMonth(now)
	var subscriptionEnd *time.Time
	if targetPrice > 0 {
		subscriptionEnd = &end
	}
	return subscriptionQuote{Action: action, AmountCents: targetPrice, ServicePeriodStart: now, ServicePeriodEnd: end, SubscriptionEndsAt: subscriptionEnd}
}

func canPurchasePlan(enabled bool, current *currentSubscription, targetPlanID string) bool {
	return enabled || current != nil && current.PlanID == targetPlanID
}

func (s *Server) subscriptionPlans(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	current, err := s.loadCurrentSubscription(r, p.ID)
	if err != nil && err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	if err == pgx.ErrNoRows {
		current = nil
	}
	var paidBaseline int64
	if err = s.db.QueryRow(r.Context(), `SELECT coalesce(max(pv.cycle_price_cents),0)
		FROM subscription_purchases sp JOIN plan_versions pv ON pv.id=sp.plan_version_id
		WHERE sp.user_id=$1 AND sp.status='succeeded' AND sp.service_period_start<=now() AND sp.service_period_end>now()`, p.ID).Scan(&paidBaseline); err != nil {
		s.internalError(w, err)
		return
	}
	if current != nil && current.Status == "active" && current.EndsAt != nil && current.EndsAt.After(time.Now()) && current.CyclePriceCents > paidBaseline {
		paidBaseline = current.CyclePriceCents
	}
	currentPlanID := ""
	if current != nil {
		currentPlanID = current.PlanID
	}
	rows, err := s.db.Query(r.Context(), `SELECT plan_id,plan_version_id,code,name,version,cycle_price_cents,entitlements,effective_at FROM (
		SELECT DISTINCT ON (p.id) p.id AS plan_id,pv.id AS plan_version_id,p.code,p.name,pv.version,pv.cycle_price_cents,pv.entitlements,pv.effective_at
		FROM plans p JOIN plan_versions pv ON pv.plan_id=p.id
		WHERE pv.effective_at<=now() AND (p.purchase_enabled OR p.id=nullif($1,'')::uuid)
		ORDER BY p.id,pv.effective_at DESC,pv.version DESC
) latest ORDER BY cycle_price_cents,name`, currentPlanID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	plans := []subscriptionPlan{}
	now := time.Now().UTC()
	for rows.Next() {
		var item subscriptionPlan
		if err = rows.Scan(&item.PlanID, &item.PlanVersionID, &item.Code, &item.Name, &item.Version, &item.CyclePriceCents, &item.Entitlements, &item.EffectiveAt); err != nil {
			s.internalError(w, err)
			return
		}
		quote := quoteSubscription(now, current, item.PlanID, item.PlanVersionID, item.CyclePriceCents, paidBaseline)
		item.Current = current != nil && current.PlanVersionID == item.PlanVersionID
		item.PurchaseAction = quote.Action
		item.PayableCents = quote.AmountCents
		plans = append(plans, item)
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	purchases, err := s.recentSubscriptionPurchases(r, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans, "current": current, "purchases": purchases})
}

func (s *Server) loadCurrentSubscription(r *http.Request, userID string) (*currentSubscription, error) {
	query := `SELECT p.id,pv.id,p.code,p.name,us.status,us.cycle_price_cents_snapshot,us.entitlements_snapshot,us.starts_at,us.ends_at,us.grace_ends_at
		FROM user_subscriptions us JOIN plan_versions pv ON pv.id=us.plan_version_id JOIN plans p ON p.id=pv.plan_id WHERE us.user_id=$1`
	var current currentSubscription
	err := s.db.QueryRow(r.Context(), query, userID).Scan(&current.PlanID, &current.PlanVersionID, &current.Code, &current.Name, &current.Status, &current.CyclePriceCents, &current.Entitlements, &current.StartsAt, &current.EndsAt, &current.GraceEndsAt)
	return &current, err
}

func (s *Server) recentSubscriptionPurchases(r *http.Request, userID string) ([]subscriptionPurchase, error) {
	rows, err := s.db.Query(r.Context(), `SELECT sp.id,sp.plan_version_id,p.code,p.name,sp.action,sp.status,sp.amount_cents,sp.balance_after_cents,sp.service_period_start,sp.service_period_end,sp.subscription_ends_at,sp.created_at
		FROM subscription_purchases sp JOIN plan_versions pv ON pv.id=sp.plan_version_id JOIN plans p ON p.id=pv.plan_id
		WHERE sp.user_id=$1 ORDER BY sp.created_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []subscriptionPurchase{}
	for rows.Next() {
		var item subscriptionPurchase
		if err = rows.Scan(&item.ID, &item.PlanVersionID, &item.PlanCode, &item.PlanName, &item.Action, &item.Status, &item.AmountCents, &item.BalanceAfterCents, &item.ServicePeriodStart, &item.ServicePeriodEnd, &item.SubscriptionEndsAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) purchaseSubscription(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		PlanVersionID  string `json:"planVersionId"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	q.PlanVersionID = strings.ToLower(strings.TrimSpace(q.PlanVersionID))
	q.IdempotencyKey = strings.TrimSpace(q.IdempotencyKey)
	if !validUUID(q.PlanVersionID) || q.IdempotencyKey == "" || len(q.IdempotencyKey) > 128 {
		writeError(w, http.StatusBadRequest, "validation_failed", "planVersionId and idempotencyKey are required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var lockedUser string
	if err = tx.QueryRow(r.Context(), `SELECT id FROM users WHERE id=$1 AND status='active' FOR UPDATE`, p.ID).Scan(&lockedUser); err != nil {
		writeError(w, http.StatusForbidden, "account_unavailable", "active account required")
		return
	}
	var existing subscriptionPurchase
	err = tx.QueryRow(r.Context(), `SELECT sp.id,sp.plan_version_id,p.code,p.name,sp.action,sp.status,sp.amount_cents,sp.balance_after_cents,sp.service_period_start,sp.service_period_end,sp.subscription_ends_at,sp.created_at
		FROM subscription_purchases sp JOIN plan_versions pv ON pv.id=sp.plan_version_id JOIN plans p ON p.id=pv.plan_id
		WHERE sp.user_id=$1 AND sp.idempotency_key=$2`, p.ID, q.IdempotencyKey).Scan(&existing.ID, &existing.PlanVersionID, &existing.PlanCode, &existing.PlanName, &existing.Action, &existing.Status, &existing.AmountCents, &existing.BalanceAfterCents, &existing.ServicePeriodStart, &existing.ServicePeriodEnd, &existing.SubscriptionEndsAt, &existing.CreatedAt)
	if err == nil {
		if existing.PlanVersionID != q.PlanVersionID {
			writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was already used for another plan")
			return
		}
		_ = tx.Commit(r.Context())
		if existing.Status == "insufficient_funds" {
			writeError(w, http.StatusConflict, "insufficient_balance", "wallet balance is lower than the subscription charge")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"purchase": existing, "idempotent": true})
		return
	}
	if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	var targetPlanID, targetCode, targetName string
	var targetPrice int64
	var targetPurchaseEnabled bool
	var targetEntitlements map[string]any
	err = tx.QueryRow(r.Context(), `SELECT p.id,p.code,p.name,pv.cycle_price_cents,pv.entitlements,p.purchase_enabled
		FROM plan_versions pv JOIN plans p ON p.id=pv.plan_id
		WHERE pv.id=$1 AND pv.effective_at<=now()
		  AND pv.id=(SELECT id FROM plan_versions WHERE plan_id=p.id AND effective_at<=now() ORDER BY effective_at DESC,version DESC LIMIT 1)
		FOR SHARE OF p,pv`, q.PlanVersionID).Scan(&targetPlanID, &targetCode, &targetName, &targetPrice, &targetEntitlements, &targetPurchaseEnabled)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "plan_version_unavailable", "latest effective plan version not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	var current *currentSubscription
	var currentValue currentSubscription
	err = tx.QueryRow(r.Context(), `SELECT p.id,pv.id,p.code,p.name,us.status,us.cycle_price_cents_snapshot,us.entitlements_snapshot,us.starts_at,us.ends_at,us.grace_ends_at
		FROM user_subscriptions us JOIN plan_versions pv ON pv.id=us.plan_version_id JOIN plans p ON p.id=pv.plan_id WHERE us.user_id=$1 FOR UPDATE OF us`, p.ID).Scan(&currentValue.PlanID, &currentValue.PlanVersionID, &currentValue.Code, &currentValue.Name, &currentValue.Status, &currentValue.CyclePriceCents, &currentValue.Entitlements, &currentValue.StartsAt, &currentValue.EndsAt, &currentValue.GraceEndsAt)
	if err == nil {
		current = &currentValue
	} else if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	if !canPurchasePlan(targetPurchaseEnabled, current, targetPlanID) {
		writeError(w, http.StatusNotFound, "plan_version_unavailable", "plan is not available for self-service purchase")
		return
	}
	if current != nil && current.Status == "active" && current.EndsAt == nil && current.PlanVersionID == q.PlanVersionID {
		writeError(w, http.StatusConflict, "subscription_no_change", "this permanent subscription is already active")
		return
	}
	if current != nil && current.PlanVersionID != q.PlanVersionID {
		var futureCycles bool
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM subscription_purchases WHERE user_id=$1 AND status='succeeded' AND service_period_start>now() AND service_period_end>now())`, p.ID).Scan(&futureCycles); err != nil {
			s.internalError(w, err)
			return
		}
		if futureCycles {
			writeError(w, http.StatusConflict, "prepaid_cycles_locked", "a future prepaid cycle exists; wait for it to begin before changing plans")
			return
		}
	}
	var walletID string
	var balance int64
	if err = tx.QueryRow(r.Context(), `SELECT id,balance_cents FROM wallets WHERE user_id=$1 FOR UPDATE`, p.ID).Scan(&walletID, &balance); err != nil {
		s.internalError(w, err)
		return
	}
	var paidBaseline int64
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(max(pv.cycle_price_cents),0)
		FROM subscription_purchases sp JOIN plan_versions pv ON pv.id=sp.plan_version_id
		WHERE sp.user_id=$1 AND sp.status='succeeded' AND sp.service_period_start<=now() AND sp.service_period_end>now()`, p.ID).Scan(&paidBaseline); err != nil {
		s.internalError(w, err)
		return
	}
	if current != nil && current.Status == "active" && current.EndsAt != nil && current.EndsAt.After(time.Now()) && current.CyclePriceCents > paidBaseline {
		paidBaseline = current.CyclePriceCents
	}
	now := time.Now().UTC()
	quote := quoteSubscription(now, current, targetPlanID, q.PlanVersionID, targetPrice, paidBaseline)
	auditActor := p.ID
	if p.Impersonating {
		auditActor = p.ActorID
	}
	var previousVersion any
	if current != nil {
		previousVersion = current.PlanVersionID
	}
	var purchaseID string
	if err = tx.QueryRow(r.Context(), `SELECT gen_random_uuid()::text`).Scan(&purchaseID); err != nil {
		s.internalError(w, err)
		return
	}
	if balance < quote.AmountCents {
		_, err = tx.Exec(r.Context(), `INSERT INTO subscription_purchases(id,user_id,plan_version_id,previous_plan_version_id,idempotency_key,action,status,amount_cents,balance_after_cents,service_period_start,service_period_end,subscription_ends_at)
			VALUES($1,$2,$3,$4,$5,$6,'insufficient_funds',$7,$8,$9,$10,$11)`, purchaseID, p.ID, q.PlanVersionID, previousVersion, q.IdempotencyKey, quote.Action, quote.AmountCents, balance, quote.ServicePeriodStart, quote.ServicePeriodEnd, quote.SubscriptionEndsAt)
		if err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO user_notifications(user_id,event_key,kind,severity,title,content,metadata)
				VALUES($1,'subscription-purchase-failed/'||$2,'subscription_purchase_failed','warning','套餐购买失败','钱包余额不足，请先充值后重新购买。',jsonb_build_object('purchase_id',$2::text,'plan_version_id',$3::text,'required_cents',$4::bigint,'balance_cents',$5::bigint))`, p.ID, purchaseID, q.PlanVersionID, quote.AmountCents, balance)
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
				VALUES($1,$2,'subscription.purchase.insufficient','subscription_purchase',$3,$4,jsonb_build_object('plan_version_id',$5::text,'amount_cents',$6::bigint,'balance_cents',$7::bigint))`, auditActor, p.ID, purchaseID, requestID(r.Context()), q.PlanVersionID, quote.AmountCents, balance)
		}
		if err != nil {
			s.internalError(w, err)
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeError(w, http.StatusConflict, "insufficient_balance", "wallet balance is lower than the subscription charge")
		return
	}
	newBalance := balance - quote.AmountCents
	var ledgerID any
	if quote.AmountCents > 0 {
		var id int64
		err = tx.QueryRow(r.Context(), `INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,metadata)
			VALUES($1,'subscription',$2,$3,$4,jsonb_build_object('purchase_id',$5::text,'plan_version_id',$6::text,'action',$7::text,'service_period_start',$8::timestamptz,'service_period_end',$9::timestamptz)) RETURNING id`, walletID, "subscription/"+purchaseID, -quote.AmountCents, newBalance, purchaseID, q.PlanVersionID, quote.Action, quote.ServicePeriodStart, quote.ServicePeriodEnd).Scan(&id)
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE wallets SET balance_cents=$1,version=version+1 WHERE id=$2`, newBalance, walletID)
		}
		if err != nil {
			s.internalError(w, err)
			return
		}
		ledgerID = id
	}
	startsAt := now
	if current != nil && current.Status == "active" && current.EndsAt != nil && current.EndsAt.After(now) {
		startsAt = current.StartsAt
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO user_subscriptions(user_id,plan_version_id,entitlements_snapshot,cycle_price_cents_snapshot,status,starts_at,ends_at,grace_ends_at)
		VALUES($1,$2,$3,$4,'active',$5,$6,NULL)
		ON CONFLICT(user_id) DO UPDATE SET plan_version_id=EXCLUDED.plan_version_id,entitlements_snapshot=EXCLUDED.entitlements_snapshot,cycle_price_cents_snapshot=EXCLUDED.cycle_price_cents_snapshot,status='active',starts_at=EXCLUDED.starts_at,ends_at=EXCLUDED.ends_at,grace_ends_at=NULL,updated_at=now()`, p.ID, q.PlanVersionID, targetEntitlements, targetPrice, startsAt, quote.SubscriptionEndsAt)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO subscription_purchases(id,user_id,plan_version_id,previous_plan_version_id,idempotency_key,action,status,amount_cents,balance_after_cents,service_period_start,service_period_end,subscription_ends_at,wallet_ledger_entry_id)
			VALUES($1,$2,$3,$4,$5,$6,'succeeded',$7,$8,$9,$10,$11,$12)`, purchaseID, p.ID, q.PlanVersionID, previousVersion, q.IdempotencyKey, quote.Action, quote.AmountCents, newBalance, quote.ServicePeriodStart, quote.ServicePeriodEnd, quote.SubscriptionEndsAt, ledgerID)
	}
	var billID string
	if err == nil {
		periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		periodEnd := periodStart.AddDate(0, 1, 0)
		err = tx.QueryRow(r.Context(), `INSERT INTO bills(user_id,period_start,period_end) VALUES($1,$2,$3) ON CONFLICT(user_id,period_start,period_end) DO UPDATE SET updated_at=now() RETURNING id`, p.ID, periodStart, periodEnd).Scan(&billID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO subscription_bill_items(bill_id,subscription_purchase_id,plan_version_id,plan_code,plan_name,action,amount_cents,service_period_start,service_period_end)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, billID, purchaseID, q.PlanVersionID, targetCode, targetName, quote.Action, quote.AmountCents, quote.ServicePeriodStart, quote.ServicePeriodEnd)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE bills SET total_cents=total_cents+$2,updated_at=now() WHERE id=$1`, billID, quote.AmountCents)
	}
	var creditGranted int64
	if err == nil {
		var grantID, grantRef string
		grantErr := tx.QueryRow(r.Context(), `SELECT id,amount_cents,business_ref FROM grant_subscription_credit($1::uuid,$2::uuid)`, p.ID, auditActor).Scan(&grantID, &creditGranted, &grantRef)
		if grantErr != nil && grantErr != pgx.ErrNoRows {
			err = grantErr
		} else if grantErr == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
				VALUES($1,$2,'subscription.credit_grant','credit_grant',$3,$4,jsonb_build_object('amount_cents',$5::bigint,'business_ref',$6::text,'source','subscription_purchase'))`, auditActor, p.ID, grantID, requestID(r.Context()), creditGranted, grantRef)
		}
	}
	var resumeJobs int64
	if err == nil {
		resumeJobs, err = resumeSubscriptionApps(r, tx, p.ID, "subscription-purchase/"+purchaseID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO user_notifications(user_id,event_key,kind,severity,title,content,metadata)
			VALUES($1,'subscription-purchased/'||$2,'subscription_purchased','info','套餐已生效','套餐购买或续期已完成，费用已写入不可变账本。',jsonb_build_object('purchase_id',$2::text,'plan_version_id',$3::text,'amount_cents',$4::bigint,'subscription_ends_at',$5::timestamptz))`, p.ID, purchaseID, q.PlanVersionID, quote.AmountCents, quote.SubscriptionEndsAt)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
			VALUES($1,$2,'subscription.purchase','subscription_purchase',$3,$4,jsonb_build_object('plan_version_id',$5::text,'action',$6::text,'amount_cents',$7::bigint,'balance_after_cents',$8::bigint,'resume_jobs',$9::bigint))`, auditActor, p.ID, purchaseID, requestID(r.Context()), q.PlanVersionID, quote.Action, quote.AmountCents, newBalance, resumeJobs)
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	result := subscriptionPurchase{ID: purchaseID, PlanVersionID: q.PlanVersionID, PlanCode: targetCode, PlanName: targetName, Action: quote.Action, Status: "succeeded", AmountCents: quote.AmountCents, BalanceAfterCents: newBalance, ServicePeriodStart: quote.ServicePeriodStart, ServicePeriodEnd: quote.ServicePeriodEnd, SubscriptionEndsAt: quote.SubscriptionEndsAt, CreatedAt: now}
	writeJSON(w, http.StatusCreated, map[string]any{"purchase": result, "creditGrantedCents": creditGranted, "resumeJobs": resumeJobs})
}

func resumeSubscriptionApps(r *http.Request, tx pgx.Tx, userID, reference string) (int64, error) {
	result, err := tx.Exec(r.Context(), `INSERT INTO deployment_jobs(user_app_id,release_id,idempotency_key,operation,source_release_id)
		SELECT a.id,a.last_successful_release_id,$2 || '/' || a.id::text,'subscription_recovery',a.last_successful_release_id
		FROM user_apps a
		WHERE a.user_id=$1 AND a.status='suspended' AND a.suspension_reason='subscription_expired'
		  AND a.last_successful_release_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM deployment_jobs j WHERE j.user_app_id=a.id AND j.state NOT IN ('succeeded','failed'))
		ON CONFLICT(idempotency_key) DO NOTHING`, userID, reference)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(r.Context(), `UPDATE user_apps a SET status='updating',suspension_reason=NULL
		WHERE a.user_id=$1 AND a.status='suspended' AND a.suspension_reason='subscription_expired'
		  AND EXISTS (SELECT 1 FROM deployment_jobs j WHERE j.user_app_id=a.id AND j.state='queued' AND j.idempotency_key LIKE $2 || '/%')`, userID, reference)
	return result.RowsAffected(), err
}
