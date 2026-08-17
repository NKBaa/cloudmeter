package httpapi

import (
	"testing"
	"time"
)

func TestAddCalendarMonthClampsEndOfMonth(t *testing.T) {
	start := time.Date(2028, time.January, 31, 12, 30, 0, 0, time.FixedZone("test", 8*60*60))
	want := time.Date(2028, time.February, 29, 4, 30, 0, 0, time.UTC)
	if got := addCalendarMonth(start); !got.Equal(want) {
		t.Fatalf("addCalendarMonth(%s) = %s, want %s", start, got, want)
	}
}

func TestQuoteSubscriptionLifecycle(t *testing.T) {
	now := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	ends := time.Date(2026, time.September, 16, 8, 0, 0, 0, time.UTC)
	current := &currentSubscription{
		PlanID: "basic", PlanVersionID: "basic-v1", Status: "active",
		CyclePriceCents: 9900, StartsAt: now.AddDate(0, -1, 0), EndsAt: &ends,
	}

	upgrade := quoteSubscription(now, current, "pro", "pro-v1", 19900, 9900)
	if upgrade.Action != "upgrade" || upgrade.AmountCents != 10000 || upgrade.SubscriptionEndsAt == nil || !upgrade.SubscriptionEndsAt.Equal(ends) {
		t.Fatalf("unexpected upgrade quote: %+v", upgrade)
	}

	versionUpgrade := quoteSubscription(now, current, "basic", "basic-v2", 12900, 9900)
	if versionUpgrade.Action != "upgrade" || versionUpgrade.AmountCents != 3000 || versionUpgrade.ServicePeriodStart != now || versionUpgrade.SubscriptionEndsAt == nil || !versionUpgrade.SubscriptionEndsAt.Equal(ends) {
		t.Fatalf("unexpected same-plan version upgrade quote: %+v", versionUpgrade)
	}

	downgrade := quoteSubscription(now, current, "starter", "starter-v1", 4900, 9900)
	if downgrade.Action != "downgrade" || downgrade.AmountCents != 0 || downgrade.SubscriptionEndsAt == nil || !downgrade.SubscriptionEndsAt.Equal(ends) {
		t.Fatalf("unexpected downgrade quote: %+v", downgrade)
	}

	renewal := quoteSubscription(now, current, "basic", "basic-v1", 9900, 9900)
	wantRenewalEnd := time.Date(2026, time.October, 16, 8, 0, 0, 0, time.UTC)
	if renewal.Action != "renewal" || renewal.AmountCents != 9900 || !renewal.ServicePeriodStart.Equal(ends) || renewal.SubscriptionEndsAt == nil || !renewal.SubscriptionEndsAt.Equal(wantRenewalEnd) {
		t.Fatalf("unexpected renewal quote: %+v", renewal)
	}

	expired := *current
	expired.Status = "expired"
	expired.EndsAt = &now
	reactivation := quoteSubscription(now, &expired, "basic", "basic-v1", 9900, 9900)
	if reactivation.Action != "renewal" || reactivation.AmountCents != 9900 || !reactivation.ServicePeriodStart.Equal(now) {
		t.Fatalf("unexpected reactivation quote: %+v", reactivation)
	}
}

func TestCanPurchasePlan(t *testing.T) {
	current := &currentSubscription{PlanID: "current-plan"}
	tests := []struct {
		name    string
		enabled bool
		current *currentSubscription
		target  string
		want    bool
	}{
		{name: "enabled plan", enabled: true, target: "another-plan", want: true},
		{name: "disabled without subscription", target: "current-plan", want: false},
		{name: "disabled current plan", current: current, target: "current-plan", want: true},
		{name: "disabled different plan", current: current, target: "another-plan", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canPurchasePlan(test.enabled, test.current, test.target); got != test.want {
				t.Fatalf("canPurchasePlan() = %t, want %t", got, test.want)
			}
		})
	}
}
