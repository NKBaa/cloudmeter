package domain

import "fmt"

type DeploymentState string

const (
	DeploymentQueued      DeploymentState = "queued"
	DeploymentPulling     DeploymentState = "pulling"
	DeploymentStarting    DeploymentState = "starting"
	DeploymentChecking    DeploymentState = "health_checking"
	DeploymentSwitching   DeploymentState = "switching_route"
	DeploymentSucceeded   DeploymentState = "succeeded"
	DeploymentRollingBack DeploymentState = "rolling_back"
	DeploymentFailed      DeploymentState = "failed"
)

var deploymentTransitions = map[DeploymentState]map[DeploymentState]bool{
	DeploymentQueued:      {DeploymentPulling: true, DeploymentFailed: true},
	DeploymentPulling:     {DeploymentStarting: true, DeploymentRollingBack: true, DeploymentFailed: true},
	DeploymentStarting:    {DeploymentChecking: true, DeploymentRollingBack: true, DeploymentFailed: true},
	DeploymentChecking:    {DeploymentChecking: true, DeploymentSwitching: true, DeploymentRollingBack: true, DeploymentFailed: true},
	DeploymentSwitching:   {DeploymentSucceeded: true, DeploymentRollingBack: true},
	DeploymentRollingBack: {DeploymentFailed: true},
}

func ValidateDeploymentTransition(from, to DeploymentState) error {
	if !deploymentTransitions[from][to] {
		return fmt.Errorf("invalid deployment transition %q -> %q", from, to)
	}
	return nil
}

type OrderState string

const (
	OrderPending   OrderState = "pending"
	OrderPaid      OrderState = "paid"
	OrderClosed    OrderState = "closed"
	OrderRefunding OrderState = "refunding"
	OrderRefunded  OrderState = "refunded"
)

var orderTransitions = map[OrderState]map[OrderState]bool{
	OrderPending:   {OrderPaid: true, OrderClosed: true},
	OrderPaid:      {OrderRefunding: true},
	OrderRefunding: {OrderRefunded: true, OrderPaid: true},
}

func ValidateOrderTransition(from, to OrderState) error {
	if !orderTransitions[from][to] {
		return fmt.Errorf("invalid order transition %q -> %q", from, to)
	}
	return nil
}

type RefundState string

const (
	RefundProcessing RefundState = "processing"
	RefundSucceeded  RefundState = "succeeded"
	RefundFailed     RefundState = "failed"
)

var refundTransitions = map[RefundState]map[RefundState]bool{
	RefundProcessing: {RefundSucceeded: true, RefundFailed: true},
}

func ValidateRefundTransition(from, to RefundState) error {
	if !refundTransitions[from][to] {
		return fmt.Errorf("invalid refund transition %q -> %q", from, to)
	}
	return nil
}
