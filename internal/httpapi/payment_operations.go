package httpapi

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

func (s *Server) paymentOperation(w http.ResponseWriter, r *http.Request, operation string) {
	p, _ := r.Context().Value(principalKey).(principal)
	orderID := r.PathValue("orderID")
	var order providerOrder
	var provider string
	if err := s.db.QueryRow(r.Context(), `SELECT id,provider,amount_cents,status,coalesce(provider_ref,'') FROM payment_orders WHERE id=$1`, orderID).Scan(&order.ID, &provider, &order.AmountCents, &order.Status, &order.ProviderRef); err == pgx.ErrNoRows {
		writeError(w, 404, "order_not_found", "payment order not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	impl, err := s.paymentProvider(r.Context(), provider)
	if err != nil {
		if recordErr := s.recordPaymentOperation(r, orderID, provider, operation, "unconfigured", "", err.Error(), p.ID); recordErr != nil {
			s.internalError(w, recordErr)
			return
		}
		writeError(w, 503, "payment_provider_operation_unconfigured", err.Error())
		return
	}
	var result providerResult
	if operation == "query" {
		result, err = impl.QueryPayment(r.Context(), order)
	} else {
		result, err = impl.ClosePayment(r.Context(), order)
	}
	if err != nil {
		classification := "failed"
		if errors.Is(err, errProviderOperationUnconfigured) {
			classification = "unconfigured"
		}
		if recordErr := s.recordPaymentOperation(r, orderID, provider, operation, classification, "", err.Error(), p.ID); recordErr != nil {
			s.internalError(w, recordErr)
			return
		}
		status := 502
		if classification == "unconfigured" {
			status = 503
		}
		writeError(w, status, "payment_provider_operation_failed", err.Error())
		return
	}
	if recordErr := s.recordPaymentOperation(r, orderID, provider, operation, "succeeded", result.Status, result.Message, p.ID); recordErr != nil {
		s.internalError(w, recordErr)
		return
	}
	if operation == "close" && result.Status == "closed" {
		tx, txErr := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
		if txErr != nil {
			s.internalError(w, txErr)
			return
		}
		defer tx.Rollback(r.Context())
		var current string
		if txErr = tx.QueryRow(r.Context(), `SELECT status FROM payment_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&current); txErr != nil {
			s.internalError(w, txErr)
			return
		}
		if current == "pending" {
			if _, txErr = tx.Exec(r.Context(), `UPDATE payment_orders SET status='closed',closed_at=now() WHERE id=$1`, orderID); txErr == nil {
				_, txErr = tx.Exec(r.Context(), `INSERT INTO payment_order_events(order_id,from_status,to_status,message) VALUES($1,'pending','closed',$2)`, orderID, result.Message)
			}
			if txErr == nil {
				_, txErr = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'payment.order.close','payment_order',$2,$3,jsonb_build_object('provider',$4::text))`, p.ID, orderID, requestID(r.Context()), provider)
			}
			if txErr != nil {
				s.internalError(w, txErr)
				return
			}
		}
		if txErr = tx.Commit(r.Context()); txErr != nil {
			s.internalError(w, txErr)
			return
		}
		order.Status = "closed"
	}
	if operation == "query" {
		_, _ = s.db.Exec(r.Context(), `UPDATE payment_orders SET last_queried_at=now() WHERE id=$1`, orderID)
	}
	writeJSON(w, 200, map[string]any{"orderId": orderID, "provider": provider, "status": order.Status, "providerStatus": result.Status, "message": result.Message})
}

func (s *Server) queryPayment(w http.ResponseWriter, r *http.Request) {
	s.paymentOperation(w, r, "query")
}
func (s *Server) closePayment(w http.ResponseWriter, r *http.Request) {
	s.paymentOperation(w, r, "close")
}

func (s *Server) recordPaymentOperation(r *http.Request, orderID, provider, operation, result, providerStatus, message, actorID string) error {
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `INSERT INTO payment_provider_operations(order_id,provider,operation,result,provider_status,message,request_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, orderID, provider, operation, result, providerStatus, message, requestID(r.Context())); err != nil {
		return err
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'payment.provider.'||$2,'payment_order',$3,$4,jsonb_build_object('provider',$5::text,'result',$6::text,'provider_status',$7::text))`, actorID, operation, orderID, requestID(r.Context()), provider, result, providerStatus); err != nil {
		return err
	}
	return tx.Commit(r.Context())
}
