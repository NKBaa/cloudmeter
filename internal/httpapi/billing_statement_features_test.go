package httpapi

import (
	"testing"
	"time"
)

func TestBillStatus(t *testing.T) {
	if got := billStatus(time.Now().Add(time.Hour)); got != "open" {
		t.Fatalf("future statement status = %q", got)
	}
	if got := billStatus(time.Now().Add(-time.Hour)); got != "finalized" {
		t.Fatalf("past statement status = %q", got)
	}
}

func TestSpreadsheetSafe(t *testing.T) {
	for _, value := range []string{"=SUM(A1:A2)", " +cmd", "-1+2", "@formula"} {
		if got := spreadsheetSafe(value); got[0] != '\'' {
			t.Fatalf("spreadsheetSafe(%q) = %q", value, got)
		}
	}
	if got := spreadsheetSafe("app.runtime.minutes"); got != "app.runtime.minutes" {
		t.Fatalf("ordinary cell changed to %q", got)
	}
}
