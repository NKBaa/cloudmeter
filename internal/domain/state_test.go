package domain

import "testing"

func TestDeploymentTransitions(t *testing.T) {
	if err := ValidateDeploymentTransition(DeploymentChecking, DeploymentSwitching); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDeploymentTransition(DeploymentChecking, DeploymentChecking); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDeploymentTransition(DeploymentRollingBack, DeploymentFailed); err != nil {
		t.Fatal("rollback did not reach terminal failure: ", err)
	}
	if err := ValidateDeploymentTransition(DeploymentFailed, DeploymentStarting); err == nil {
		t.Fatal("terminal deployment unexpectedly transitioned")
	}
}

func TestOrderTransitions(t *testing.T) {
	if err := ValidateOrderTransition(OrderPending, OrderPaid); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOrderTransition(OrderRefunded, OrderPaid); err == nil {
		t.Fatal("refunded order unexpectedly transitioned")
	}
}

func TestRefundTransitions(t *testing.T) {
	if err := ValidateRefundTransition(RefundProcessing, RefundSucceeded); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRefundTransition(RefundProcessing, RefundFailed); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRefundTransition(RefundSucceeded, RefundFailed); err == nil {
		t.Fatal("terminal refund unexpectedly transitioned")
	}
}
