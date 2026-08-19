package httpapi

import (
	"context"
	"errors"
	"net/url"
)

var errProviderOperationUnconfigured = errors.New("payment provider operation is not configured")

type providerOrder struct {
	ID          string
	AmountCents int64
	Status      string
	ProviderRef string
}

type providerCreateRequest struct {
	Order  providerOrder
	Origin string
}

type providerCallback struct {
	OrderID     string
	AmountCents int64
	ProviderRef string
	Succeeded   bool
}

type providerResult struct {
	Status  string
	Message string
}

type paymentProvider interface {
	CreatePayment(context.Context, providerCreateRequest) (string, error)
	VerifyCallback(context.Context, url.Values) (providerCallback, error)
	QueryPayment(context.Context, providerOrder) (providerResult, error)
	ClosePayment(context.Context, providerOrder) (providerResult, error)
}

type manualPaymentProvider struct{}

func (manualPaymentProvider) CreatePayment(context.Context, providerCreateRequest) (string, error) {
	return "", nil
}
func (manualPaymentProvider) VerifyCallback(context.Context, url.Values) (providerCallback, error) {
	return providerCallback{}, errProviderOperationUnconfigured
}
func (manualPaymentProvider) QueryPayment(_ context.Context, order providerOrder) (providerResult, error) {
	return providerResult{Status: order.Status, Message: "manual order status read from the platform ledger"}, nil
}
func (manualPaymentProvider) ClosePayment(_ context.Context, order providerOrder) (providerResult, error) {
	if order.Status == "closed" {
		return providerResult{Status: "closed", Message: "manual order is already closed"}, nil
	}
	if order.Status != "pending" {
		return providerResult{}, errors.New("only pending manual orders can be closed")
	}
	return providerResult{Status: "closed", Message: "manual order closed"}, nil
}

type epayPaymentProvider struct{ merchant, endpoint, secret, paymentType string }

func (p epayPaymentProvider) CreatePayment(_ context.Context, req providerCreateRequest) (string, error) {
	values := map[string]string{"pid": p.merchant, "type": p.paymentType, "out_trade_no": req.Order.ID, "notify_url": req.Origin + "/api/payments/epay/callback", "return_url": req.Origin + "/console/recharge", "name": "CloudMeter 账户充值", "money": centsAmountText(req.Order.AmountCents), "device": "pc"}
	return buildEPayCheckoutURL(p.endpoint, values, p.secret)
}
func (p epayPaymentProvider) VerifyCallback(_ context.Context, form url.Values) (providerCallback, error) {
	return verifyEPayCallback(form, p.merchant, p.secret)
}
func (epayPaymentProvider) QueryPayment(context.Context, providerOrder) (providerResult, error) {
	return providerResult{}, errProviderOperationUnconfigured
}
func (epayPaymentProvider) ClosePayment(context.Context, providerOrder) (providerResult, error) {
	return providerResult{}, errProviderOperationUnconfigured
}

func (s *Server) paymentProvider(ctx context.Context, name string) (paymentProvider, error) {
	if name == "manual" {
		return manualPaymentProvider{}, nil
	}
	if name != "epay" {
		return nil, errors.New("unsupported payment provider")
	}
	var enabled bool
	var merchant, endpoint, encrypted, paymentType string
	if err := s.db.QueryRow(ctx, `SELECT enabled,merchant_id,endpoint,secret,payment_type FROM payment_provider_configs WHERE provider='epay'`).Scan(&enabled, &merchant, &endpoint, &encrypted, &paymentType); err != nil {
		return nil, err
	}
	if !enabled || merchant == "" || endpoint == "" || encrypted == "" {
		return nil, errors.New("EPay is not configured")
	}
	secret, err := s.secrets.Decrypt("payment.secret.epay", encrypted)
	if err != nil {
		return nil, err
	}
	return epayPaymentProvider{merchant: merchant, endpoint: endpoint, secret: secret, paymentType: paymentType}, nil
}
