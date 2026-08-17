package httpapi

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type billingStatement struct {
	ID          string    `json:"id"`
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
	Currency    string    `json:"currency"`
	TotalCents  int64     `json:"totalCents"`
	ItemCount   int64     `json:"itemCount"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type billingStatementItem struct {
	ID               string    `json:"id"`
	Kind             string    `json:"kind"`
	AppSlug          *string   `json:"appSlug"`
	UsageCode        string    `json:"usageCode"`
	Unit             string    `json:"unit"`
	Quantity         string    `json:"quantity"`
	PricingVersionID string    `json:"pricingVersionId"`
	UnitPriceMicros  int64     `json:"unitPriceMicros"`
	AmountCents      int64     `json:"amountCents"`
	WindowStart      time.Time `json:"windowStart"`
	WindowEnd        time.Time `json:"windowEnd"`
}

func billStatus(periodEnd time.Time) string {
	if periodEnd.After(time.Now()) {
		return "open"
	}
	return "finalized"
}

func spreadsheetSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func (s *Server) listBillingStatements(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	rows, err := s.db.Query(r.Context(), `SELECT b.id,b.period_start,b.period_end,b.currency,b.total_cents,
		(SELECT count(*) FROM bill_items i WHERE i.bill_id=b.id)+(SELECT count(*) FROM subscription_bill_items i WHERE i.bill_id=b.id),b.updated_at
		FROM bills b WHERE b.user_id=$1 ORDER BY b.period_start DESC LIMIT 24`, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []billingStatement{}
	for rows.Next() {
		var item billingStatement
		if err := rows.Scan(&item.ID, &item.PeriodStart, &item.PeriodEnd, &item.Currency, &item.TotalCents, &item.ItemCount, &item.UpdatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		item.Status = billStatus(item.PeriodEnd)
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"bills": items})
}

func (s *Server) billingStatementDetail(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	billID := r.PathValue("billID")
	var bill billingStatement
	err := s.db.QueryRow(r.Context(), `SELECT b.id,b.period_start,b.period_end,b.currency,b.total_cents,
		(SELECT count(*) FROM bill_items i WHERE i.bill_id=b.id)+(SELECT count(*) FROM subscription_bill_items i WHERE i.bill_id=b.id),b.updated_at
		FROM bills b WHERE b.id=$1 AND b.user_id=$2`, billID, p.ID).Scan(&bill.ID, &bill.PeriodStart, &bill.PeriodEnd, &bill.Currency, &bill.TotalCents, &bill.ItemCount, &bill.UpdatedAt)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "bill_not_found", "bill not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	bill.Status = billStatus(bill.PeriodEnd)
	rows, err := s.db.Query(r.Context(), `
		SELECT 'usage/'||i.id::text,'usage',i.app_slug,i.usage_code,i.unit,i.quantity::text,i.pricing_version_id::text,i.unit_price_micros,i.amount_cents,i.window_start,i.window_end,i.created_at
		FROM bill_items i JOIN bills b ON b.id=i.bill_id WHERE i.bill_id=$1 AND b.user_id=$2
		UNION ALL
		SELECT 'subscription/'||i.id::text,'subscription',i.plan_name,'subscription.'||i.action,'cycle','1',i.plan_version_id::text,i.amount_cents*1000000,i.amount_cents,i.service_period_start,i.service_period_end,i.created_at
		FROM subscription_bill_items i JOIN bills b ON b.id=i.bill_id WHERE i.bill_id=$1 AND b.user_id=$2
		ORDER BY 12,1`, billID, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []billingStatementItem{}
	for rows.Next() {
		var item billingStatementItem
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.Kind, &item.AppSlug, &item.UsageCode, &item.Unit, &item.Quantity, &item.PricingVersionID, &item.UnitPriceMicros, &item.AmountCents, &item.WindowStart, &item.WindowEnd, &createdAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"bill": bill, "items": items})
}

func (s *Server) exportBillingStatement(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	billID := r.PathValue("billID")
	var periodStart time.Time
	if err := s.db.QueryRow(r.Context(), `SELECT period_start FROM bills WHERE id=$1 AND user_id=$2`, billID, p.ID).Scan(&periodStart); err == pgx.ErrNoRows {
		writeError(w, 404, "bill_not_found", "bill not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT i.app_slug,i.usage_code,i.unit,i.quantity::text,i.pricing_version_id::text,i.unit_price_micros,i.amount_cents,i.window_start,i.window_end,i.created_at
		FROM bill_items i JOIN bills b ON b.id=i.bill_id WHERE i.bill_id=$1 AND b.user_id=$2
		UNION ALL
		SELECT i.plan_name,'subscription.'||i.action,'cycle','1',i.plan_version_id::text,i.amount_cents*1000000,i.amount_cents,i.service_period_start,i.service_period_end,i.created_at
		FROM subscription_bill_items i JOIN bills b ON b.id=i.bill_id WHERE i.bill_id=$1 AND b.user_id=$2
		ORDER BY 10,2`, billID, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=cloudmeter-bill-%s.csv`, periodStart.UTC().Format("2006-01")))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"应用或套餐", "费用项", "单位", "数量", "价格版本", "单价(微分)", "金额(分)", "开始时间", "结束时间"})
	for rows.Next() {
		var app *string
		var code, unit, quantity, pricingVersionID string
		var unitPrice, amount int64
		var start, end time.Time
		var createdAt time.Time
		if err := rows.Scan(&app, &code, &unit, &quantity, &pricingVersionID, &unitPrice, &amount, &start, &end, &createdAt); err != nil {
			return
		}
		appName := "账户级"
		if app != nil {
			appName = *app
		}
		_ = writer.Write([]string{spreadsheetSafe(appName), spreadsheetSafe(code), spreadsheetSafe(unit), spreadsheetSafe(quantity), pricingVersionID, strconv.FormatInt(unitPrice, 10), strconv.FormatInt(amount, 10), start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)})
	}
	writer.Flush()
}
