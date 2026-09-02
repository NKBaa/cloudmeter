package httpapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
)

type startupBalanceError struct {
	AvailableCents int64
	RequiredCents  int64
}

func (e startupBalanceError) Error() string {
	return fmt.Sprintf("available balance %d cents is below startup reserve %d cents", e.AvailableCents, e.RequiredCents)
}

func enforceMinimumStartupBalance(ctx context.Context, tx pgx.Tx, userID string) error {
	var walletCents, creditCents, reserveCents int64
	err := tx.QueryRow(ctx, `SELECT coalesce(wallet.balance_cents,0),
		coalesce((SELECT sum(remaining_cents) FROM credit_grants WHERE user_id=$1 AND remaining_cents>0 AND (expires_at IS NULL OR expires_at>now())),0),
		settings.startup_balance_reserve_cents
		FROM system_settings settings LEFT JOIN wallets wallet ON wallet.user_id=$1
		WHERE settings.singleton`, userID).Scan(&walletCents, &creditCents, &reserveCents)
	if err != nil {
		return err
	}
	available := walletCents + creditCents
	if available < reserveCents {
		return startupBalanceError{AvailableCents: available, RequiredCents: reserveCents}
	}
	return nil
}

func writeStartupBalanceError(w http.ResponseWriter, err error) bool {
	shortage, ok := err.(startupBalanceError)
	if !ok {
		return false
	}
	writeError(w, http.StatusConflict, "insufficient_startup_balance", fmt.Sprintf("可用余额不足，开机至少需要保留 %.2f 元，当前可用 %.2f 元", float64(shortage.RequiredCents)/100, float64(shortage.AvailableCents)/100))
	return true
}
