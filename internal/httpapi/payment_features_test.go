package httpapi

import (
	"strings"
	"testing"
)

func TestRefundReasonLimitCountsCharacters(t *testing.T) {
	accepted := strings.Repeat("退", 500)
	if len(accepted) <= 500 {
		t.Fatal("test input must distinguish UTF-8 bytes from characters")
	}
	if refundReasonTooLong(accepted) {
		t.Fatal("500-character refund reason was rejected")
	}
	if !refundReasonTooLong(accepted + "款") {
		t.Fatal("501-character refund reason was accepted")
	}
}

func TestImmediateRefundProviderPolicy(t *testing.T) {
	if !supportsImmediateRefund("manual") {
		t.Fatal("manual payments must support an immediate platform refund")
	}
	if supportsImmediateRefund("epay") {
		t.Fatal("EPay must not be marked refunded without a configured provider operation")
	}
}
