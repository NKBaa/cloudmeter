package main

import (
	"cloudmeter/internal/domain"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestNextDeploymentState(t *testing.T) {
	cases := []struct{ from, want domain.DeploymentState }{
		{domain.DeploymentQueued, domain.DeploymentPulling},
		{domain.DeploymentPulling, domain.DeploymentStarting},
		{domain.DeploymentStarting, domain.DeploymentChecking},
		{domain.DeploymentChecking, domain.DeploymentSwitching},
		{domain.DeploymentSwitching, domain.DeploymentSucceeded},
	}
	for _, tc := range cases {
		got, ok := nextDeploymentState(tc.from)
		if !ok || got != tc.want {
			t.Fatalf("%s -> %s, want %s", tc.from, got, tc.want)
		}
	}
	if _, ok := nextDeploymentState(domain.DeploymentFailed); ok {
		t.Fatal("failed state progressed")
	}
}

func TestNextDeploymentStateWithHealthFailure(t *testing.T) {
	got, ok := nextDeploymentStateWithHealth(domain.DeploymentChecking, false, 1)
	if !ok || got != domain.DeploymentChecking {
		t.Fatalf("first failed health check got %s, %v", got, ok)
	}
	got, ok = nextDeploymentStateWithHealth(domain.DeploymentChecking, false, deploymentMaxHealthAttempts)
	if !ok || got != domain.DeploymentRollingBack {
		t.Fatalf("got %s, %v", got, ok)
	}
	got, ok = nextDeploymentStateWithHealth(domain.DeploymentChecking, true, 1)
	if !ok || got != domain.DeploymentSwitching {
		t.Fatalf("healthy got %s, %v", got, ok)
	}
	got, ok = nextDeploymentStateWithHealth(domain.DeploymentRollingBack, true, 1)
	if !ok || got != domain.DeploymentFailed {
		t.Fatalf("rollback got %s, %v", got, ok)
	}
}

func TestSnapshotHealthOK(t *testing.T) {
	if snapshotHealthOK([]byte(`{"health_spec":{"simulateFailure":true}}`)) {
		t.Fatal("simulated failure reported healthy")
	}
	if !snapshotHealthOK([]byte(`{"health_spec":{}}`)) {
		t.Fatal("default health reported failed")
	}
}

func TestHealthInterval(t *testing.T) {
	if got := healthInterval([]byte(`{"health_spec":{"intervalSeconds":17}}`)); got != 17 {
		t.Fatalf("interval=%d", got)
	}
	if got := healthInterval([]byte(`{"health_spec":{"intervalSeconds":0}}`)); got != 5 {
		t.Fatalf("fallback interval=%d", got)
	}
}

func TestHealthPath(t *testing.T) {
	if got, ok := healthPath(map[string]any{"path": "/ready"}); !ok || got != "/ready" {
		t.Fatalf("healthPath got %q, %v", got, ok)
	}
	if _, ok := healthPath(map[string]any{}); ok {
		t.Fatal("empty health path enabled a probe")
	}
	if got, ok := healthPath(map[string]any{"path": "ready"}); !ok || got != "/__invalid_health_path__" {
		t.Fatalf("invalid health path got %q, %v", got, ok)
	}
}

func TestHealthTimeout(t *testing.T) {
	if got := healthTimeout(map[string]any{"timeoutSeconds": float64(7)}); got != 7 {
		t.Fatalf("timeout got %d", got)
	}
	if got := healthTimeout(map[string]any{"timeoutSeconds": float64(60)}); got != 5 {
		t.Fatalf("invalid timeout got %d", got)
	}
}

func TestHealthAcceptedStatusCodes(t *testing.T) {
	got := healthAcceptedStatusCodes(map[string]any{
		"acceptedStatusCodes": []any{401.0, 403.0, 401.0, 99.0, 600.0, 401.5, "404"},
	})
	if len(got) != 2 || got[0] != 401 || got[1] != 403 {
		t.Fatalf("accepted status codes=%v", got)
	}
	if got = healthAcceptedStatusCodes(map[string]any{}); len(got) != 0 {
		t.Fatalf("default accepted status codes=%v", got)
	}
}

func TestFailedHealthTransitionsToRollback(t *testing.T) {
	next, ok := nextDeploymentStateWithHealth(domain.DeploymentChecking, false, deploymentMaxHealthAttempts)
	if !ok || next != domain.DeploymentRollingBack {
		t.Fatalf("failed health transition = %q, %v", next, ok)
	}
}

func TestDeploymentHealthAttempt(t *testing.T) {
	for attempts, want := range map[int]int{0: 1, 2: 1, 3: 1, 4: 2, 10: 8} {
		if got := deploymentHealthAttempt(attempts); got != want {
			t.Fatalf("attempts=%d got health attempt %d, want %d", attempts, got, want)
		}
	}
}

func TestDeploymentFailureState(t *testing.T) {
	tests := []struct {
		operation string
		hasLast   bool
		status    string
		reason    string
	}{
		{operation: "deploy", status: "failed"},
		{operation: "update", hasLast: true, status: "running"},
		{operation: "rollback", hasLast: true, status: "running"},
		{operation: "start", hasLast: true, status: "stopped"},
		{operation: "billing_recovery", hasLast: true, status: "suspended", reason: "billing_insufficient"},
		{operation: "subscription_recovery", hasLast: true, status: "suspended", reason: "subscription_expired"},
	}
	for _, test := range tests {
		status, reason := deploymentFailureState(test.operation, test.hasLast)
		if status != test.status || reason != test.reason {
			t.Fatalf("%s failure state = (%s, %s), want (%s, %s)", test.operation, status, reason, test.status, test.reason)
		}
	}
}

func TestRecoveryOperation(t *testing.T) {
	if !isRecoveryOperation("billing_recovery") || !isRecoveryOperation("subscription_recovery") {
		t.Fatal("recovery operation was not recognized")
	}
	if isRecoveryOperation("start") || isRecoveryOperation("update") {
		t.Fatal("user lifecycle operation was recognized as recovery")
	}
}

func TestRestoreFailureBeforeStopKeepsAppRunning(t *testing.T) {
	status, appStatus := restoreResult(fmt.Errorf("helper image pull failed"), true)
	if status != "failed" || appStatus != "running" {
		t.Fatalf("pre-stop restore failure got status=%s app=%s", status, appStatus)
	}
}

func TestBackupStorageQuantityUsesExactDecimal(t *testing.T) {
	if got := backupStorageQuantity(1 << 30); got != "0.003472222222" {
		t.Fatalf("one GiB five-minute quantity = %s", got)
	}
	if got := backupStorageQuantity(0); got != "0.000000000000" {
		t.Fatalf("zero-byte quantity = %s", got)
	}
}

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
	if backupStorageQuotaExceededParts("1", 1<<29, 1<<28, 1<<28) {
		t.Fatal("multi-part quota exceeded at exact boundary")
	}
	if !backupStorageQuotaExceededParts("1", 1<<29, 1<<28, (1<<28)+1) {
		t.Fatal("multi-part quota did not exceed after one byte")
	}
	if !backupStorageQuotaExceededParts("1", 0, -1) {
		t.Fatal("negative byte part did not fail closed")
	}
}

func TestBytesQuantityUsesExactDecimal(t *testing.T) {
	if got := bytesQuantity(1<<30, 5, 60); got != "0.083333333333" {
		t.Fatalf("one GiB five-minute hourly quantity = %s", got)
	}
}

func TestSystemDiskQuantityUsesGiBDays(t *testing.T) {
	if got := decimalTimeQuantity("1", 5, 24*60); got != "0.003472222222" {
		t.Fatalf("one GiB five-minute quantity = %s", got)
	}
	if got := decimalTimeQuantity("0", 5, 24*60); got != "0" {
		t.Fatalf("zero GiB quantity = %s", got)
	}
	if got := decimalTimeQuantity("512", 5, 1024*60); got != "0.041666666667" {
		t.Fatalf("512 MiB five-minute quantity = %s", got)
	}
}

func TestEgressQuotaExceededUsesExactDecimal(t *testing.T) {
	if egressQuotaExceeded("1", "0.999999999", 0) {
		t.Fatal("quota exceeded without additional bytes")
	}
	if !egressQuotaExceeded("1", "0.999999999", 2) {
		t.Fatal("quota did not exceed after additional bytes")
	}
	if !egressQuotaExceeded("invalid", "0", 1) {
		t.Fatal("invalid quota input did not fail closed")
	}
}

func TestDeclaredSecretVersionsFiltersUndeclaredKeys(t *testing.T) {
	wantedID := "77965aa8-8059-4a81-80ac-be5f3f62db9a"
	versions, err := declaredSecretVersions(
		map[string]any{"secretKeys": []any{"API_KEY"}},
		map[string]any{
			"API_KEY":       wantedID,
			"LD_PRELOAD":    "964fc6b0-1706-4cf4-b2e3-aaada551d631",
			"HTTP_PROXY":    "ffa69304-a1ba-49c4-b874-3de6c154898d",
			"UNKNOWN_VALUE": 42,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions["API_KEY"] != wantedID {
		t.Fatalf("filtered versions=%v", versions)
	}
}

func TestDeclaredSecretVersionsRequiresEveryDeclaredKey(t *testing.T) {
	_, err := declaredSecretVersions(
		map[string]any{"secretKeys": []any{"API_KEY", "MODEL_TOKEN"}},
		map[string]any{"API_KEY": "77965aa8-8059-4a81-80ac-be5f3f62db9a"},
	)
	if err == nil || !strings.Contains(err.Error(), "MODEL_TOKEN") {
		t.Fatalf("missing declared Secret error=%v", err)
	}
}

func TestDeclaredSecretVersionsRejectsMalformedConfiguration(t *testing.T) {
	for name, runtimeSpec := range map[string]map[string]any{
		"invalid keys":     {"secretKeys": "API_KEY"},
		"invalid key name": {"secretKeys": []any{"LD-PRELOAD"}},
	} {
		if _, err := declaredSecretVersions(runtimeSpec, map[string]any{}); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if _, err := declaredSecretVersions(
		map[string]any{"secretKeys": []any{"API_KEY"}},
		map[string]any{"API_KEY": "not-a-uuid"},
	); err == nil {
		t.Fatal("malformed Secret version reference was accepted")
	}
}

func TestRuntimeContainerPatternOnlyMatchesAppReleaseUUIDs(t *testing.T) {
	valid := "cm-964fc6b0-1706-4cf4-b2e3-aaada551d631-ffa69304-a1ba-49c4-b874-3de6c154898d"
	match := runtimeContainerPattern.FindStringSubmatch(valid)
	if len(match) != 3 || match[1] != "964fc6b0-1706-4cf4-b2e3-aaada551d631" || match[2] != "ffa69304-a1ba-49c4-b874-3de6c154898d" {
		t.Fatalf("match=%v", match)
	}
	for _, invalid := range []string{"cm-backup-job", "cm-restore-job", "cm-not-a-uuid", valid + "-extra"} {
		if runtimeContainerPattern.MatchString(invalid) {
			t.Fatalf("invalid runtime name matched: %s", invalid)
		}
	}
}

func TestScopedRuntimeContainerIdentity(t *testing.T) {
	previous := runtimeScope
	runtimeScope = runtimeScopeToken("cloudmeter-test")
	t.Cleanup(func() { runtimeScope = previous })
	appID := "964fc6b0-1706-4cf4-b2e3-aaada551d631"
	releaseID := "ffa69304-a1ba-49c4-b874-3de6c154898d"
	name := containerName(appID, releaseID)
	gotApp, gotRelease, legacy, ok := runtimeContainerIdentity(name)
	if !ok || legacy || gotApp != appID || gotRelease != releaseID {
		t.Fatalf("scoped container identity = (%q, %q, %v, %v)", gotApp, gotRelease, legacy, ok)
	}
	foreign := "cm-" + runtimeScopeToken("another-stack") + "-" + appID + "-" + releaseID
	if _, _, _, ok = runtimeContainerIdentity(foreign); ok {
		t.Fatal("foreign runtime scope was accepted")
	}
	if got := healthProbeName("77965aa8-8059-4a81-80ac-be5f3f62db9a"); got != "cm-health-"+runtimeScope+"-77965aa88059" {
		t.Fatalf("scoped health probe name=%s", got)
	}
}

func TestStopContainerMatchesJobIdentity(t *testing.T) {
	previousOwner, previousScope := runtimeOwner, runtimeScope
	runtimeOwner = "cloudmeter-test"
	runtimeScope = runtimeScopeToken(runtimeOwner)
	t.Cleanup(func() { runtimeOwner, runtimeScope = previousOwner, previousScope })
	appID := "964fc6b0-1706-4cf4-b2e3-aaada551d631"
	releaseID := "ffa69304-a1ba-49c4-b874-3de6c154898d"
	if !stopContainerMatches(appID, releaseID, containerName(appID, releaseID)) {
		t.Fatal("owner-scoped stop container was rejected")
	}
	for _, invalid := range []string{
		"postgres",
		"cm-" + runtimeScopeToken("another-stack") + "-" + appID + "-" + releaseID,
		"cm-" + appID + "-" + releaseID,
		containerName("11111111-1111-4111-8111-111111111111", releaseID),
	} {
		if stopContainerMatches(appID, releaseID, invalid) {
			t.Fatalf("invalid stop container was accepted: %s", invalid)
		}
	}
}

func TestDependencyAliasesBypassEgressProxy(t *testing.T) {
	spec := map[string]any{
		"dependencies": []any{
			map[string]any{"key": "model", "productId": "123e4567-e89b-12d3-a456-426614174000", "serviceSlug": "ollama", "required": true},
			map[string]any{"key": "search", "productId": "123e4567-e89b-12d3-a456-426614174001", "serviceSlug": "searxng", "required": false},
		},
	}
	if got := strings.Join(dependencyNoProxy(spec), ","); got != "localhost,127.0.0.1,::1,ollama,searxng" {
		t.Fatalf("NO_PROXY=%s", got)
	}
}

func TestPlatformRestartOrderExcludesStateAndUserServices(t *testing.T) {
	wanted := "egress-proxy,app-router,web,api,gateway"
	if got := strings.Join(platformRestartOrder, ","); got != wanted {
		t.Fatalf("platform restart order=%s", got)
	}
	for _, forbidden := range []string{"postgres", "redis", "migrate", "worker"} {
		for _, service := range platformRestartOrder {
			if service == forbidden {
				t.Fatalf("stateful or self service %q entered pre-completion restart order", forbidden)
			}
		}
	}
}
