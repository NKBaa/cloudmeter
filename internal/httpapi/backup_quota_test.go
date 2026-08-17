package httpapi

import (
	"math"
	"testing"
)

func TestBackupStorageQuotaExceededUsesExactDecimal(t *testing.T) {
	if backupStorageQuotaExceeded("1", 1<<30, 0) {
		t.Fatal("quota exceeded at exact boundary")
	}
	if !backupStorageQuotaExceeded("1", 1<<30, 1) {
		t.Fatal("quota did not exceed after one byte")
	}
	if !backupStorageQuotaExceeded("invalid", 0, 1) {
		t.Fatal("invalid limit did not fail closed")
	}
	if !backupStorageQuotaExceeded("1", math.MaxInt64, 1) {
		t.Fatal("overflow-sized usage did not fail closed")
	}
}
