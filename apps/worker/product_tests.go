package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const productTestMaxHealthAttempts = 8

type productVersionTestSnapshot struct {
	Image   string         `json:"image_digest"`
	Runtime map[string]any `json:"runtime_spec"`
	Route   map[string]any `json:"route_spec"`
	Health  map[string]any `json:"health_spec"`
}

func processProductVersionTestOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		logger.Error("product test transaction failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)

	var id, state string
	var snapshot []byte
	var encryptedSecrets map[string]string
	var healthAttempts int
	terminal := false
	err = tx.QueryRow(ctx, `SELECT id::text,state,immutable_snapshot,encrypted_secrets,health_attempts
		FROM app_product_version_tests
		WHERE state NOT IN ('succeeded','failed') AND available_at<=now()
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &state, &snapshot, &encryptedSecrets, &healthAttempts)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("product test claim failed", "error", err)
		return
	}
	if executor == nil {
		tx.Rollback(ctx)
		failProductVersionTest(ctx, db, id, fmt.Errorf("Docker executor is not enabled"), logger)
		return
	}

	var spec productVersionTestSnapshot
	if json.Unmarshal(snapshot, &spec) != nil || strings.TrimSpace(spec.Image) == "" {
		tx.Rollback(ctx)
		failProductVersionTest(ctx, db, id, fmt.Errorf("test deployment snapshot is invalid"), logger)
		return
	}
	switch state {
	case "queued":
		if err = advanceProductVersionTest(ctx, tx, id, "pulling", 0); err != nil {
			logger.Error("product test queue transition failed", "test", id, "error", err)
			return
		}
	case "pulling":
		if err = executor.Pull(ctx, spec.Image); err != nil {
			tx.Rollback(ctx)
			failProductVersionTest(ctx, db, id, err, logger)
			return
		}
		if _, needsProbe := healthPath(spec.Health); needsProbe {
			if err = executor.Pull(ctx, backupHelperImage); err != nil {
				tx.Rollback(ctx)
				failProductVersionTest(ctx, db, id, fmt.Errorf("health probe helper image pull failed: %w", err), logger)
				return
			}
		}
		if err = advanceProductVersionTest(ctx, tx, id, "starting", 0); err != nil {
			logger.Error("product test pull transition failed", "test", id, "error", err)
			return
		}
	case "starting":
		if err = startProductVersionTestContainer(ctx, id, spec, encryptedSecrets); err != nil {
			tx.Rollback(ctx)
			failProductVersionTest(ctx, db, id, err, logger)
			return
		}
		if err = advanceProductVersionTest(ctx, tx, id, "health_checking", healthInterval(snapshot)); err != nil {
			logger.Error("product test start transition failed", "test", id, "error", err)
			return
		}
	case "health_checking":
		healthy, healthErr := productVersionTestHealthy(ctx, id, spec)
		if healthErr != nil {
			tx.Rollback(ctx)
			failProductVersionTest(ctx, db, id, healthErr, logger)
			return
		}
		if healthy {
			if err = completeProductVersionTest(ctx, tx, id, "succeeded", ""); err != nil {
				logger.Error("product test completion failed", "test", id, "error", err)
				return
			}
			terminal = true
		} else if healthAttempts+1 >= productTestMaxHealthAttempts {
			if err = completeProductVersionTest(ctx, tx, id, "failed", fmt.Sprintf("health check did not pass after %d attempts", productTestMaxHealthAttempts)); err != nil {
				logger.Error("product test failure completion failed", "test", id, "error", err)
				return
			}
			terminal = true
		} else if _, err = tx.Exec(ctx, `UPDATE app_product_version_tests
			SET attempts=attempts+1,health_attempts=health_attempts+1,available_at=now()+make_interval(secs=>$2),updated_at=now()
			WHERE id=$1`, id, healthInterval(snapshot)); err != nil {
			logger.Error("product test health retry update failed", "test", id, "error", err)
			return
		}
	default:
		logger.Error("unknown product test state", "test", id, "state", state)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("product test transition commit failed", "test", id, "error", err)
		return
	}
	if terminal {
		cleanupProductVersionTestRuntime(ctx, id, logger)
	}
}

func advanceProductVersionTest(ctx context.Context, tx pgx.Tx, id, next string, delaySeconds int) error {
	_, err := tx.Exec(ctx, `UPDATE app_product_version_tests
		SET state=$2,attempts=attempts+1,started_at=coalesce(started_at,now()),available_at=now()+make_interval(secs=>$3),updated_at=now()
		WHERE id=$1`, id, next, delaySeconds)
	return err
}

func startProductVersionTestContainer(ctx context.Context, id string, spec productVersionTestSnapshot, encryptedSecrets map[string]string) error {
	network, container := productTestNetworkName(id), productTestContainerName(id)
	if err := executor.EnsureNetwork(ctx, network); err != nil {
		return err
	}
	if egressProxyContainer != "" {
		if err := executor.ConnectNetwork(ctx, network, egressProxyContainer, "cloudmeter-egress-proxy"); err != nil {
			return err
		}
	}
	runtimeSpec, err := productVersionTestRuntime(id, spec.Runtime, encryptedSecrets)
	if err != nil {
		return err
	}
	if err = executor.RemoveIfExists(ctx, container); err != nil {
		return err
	}
	if err = executor.CreateProductTest(ctx, container, spec.Image, network, []string{productTestAlias(id)}, runtimeSpec); err != nil {
		return err
	}
	return executor.Start(ctx, container)
}

func productVersionTestRuntime(id string, runtimeSpec map[string]any, encryptedSecrets map[string]string) (map[string]any, error) {
	runtime := map[string]any{}
	for key, value := range runtimeSpec {
		runtime[key] = value
	}
	env := map[string]any{}
	if values, ok := runtime["env"].(map[string]any); ok {
		for key, value := range values {
			env[key] = value
		}
	}
	for key, encrypted := range encryptedSecrets {
		value, err := secrets.Decrypt("product.version.test."+id+"."+key, encrypted)
		if err != nil {
			return nil, fmt.Errorf("test secret authentication failed")
		}
		env[key] = value
	}
	if egressProxyContainer != "" && egressToken != "" {
		mac := hmac.New(sha256.New, []byte(egressToken))
		_, _ = mac.Write([]byte(id))
		proxyURL := "http://" + id + ":" + hex.EncodeToString(mac.Sum(nil)) + "@cloudmeter-egress-proxy:3128"
		for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env[key] = proxyURL
		}
		env["NO_PROXY"] = "localhost,127.0.0.1,::1"
		env["no_proxy"] = "localhost,127.0.0.1,::1"
	}
	if len(env) > 0 {
		runtime["env"] = env
	}
	runtime["testId"] = id
	return runtime, nil
}

func productVersionTestHealthy(ctx context.Context, id string, spec productVersionTestSnapshot) (bool, error) {
	container, network := productTestContainerName(id), productTestNetworkName(id)
	healthy, err := executor.Healthy(ctx, container)
	if err != nil && dockerResourceMissing(err) && runtimeScope != "" {
		container, network = legacyProductTestContainerName(id), legacyProductTestNetworkName(id)
		healthy, err = executor.Healthy(ctx, container)
	}
	if err != nil || !healthy {
		return healthy, err
	}
	if !snapshotHealthOK(mustMarshalProductTestHealth(spec.Health)) {
		return false, nil
	}
	path, ok := healthPath(spec.Health)
	if !ok {
		return true, nil
	}
	target := fmt.Sprintf("http://%s:%d%s", productTestAlias(id), routePort(spec.Route), path)
	probeName := productTestHealthContainerName(id)
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(healthTimeout(spec.Health)+5)*time.Second)
	defer cancel()
	err = executor.ProbeHTTP(probeCtx, probeName, backupHelperImage, network, target, healthTimeout(spec.Health))
	return err == nil, nil
}

func dockerResourceMissing(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such container") || strings.Contains(message, "not found")
}

func mustMarshalProductTestHealth(spec map[string]any) []byte {
	value, _ := json.Marshal(map[string]any{"health_spec": spec})
	return value
}

func completeProductVersionTest(ctx context.Context, tx pgx.Tx, id, state, message string) error {
	var lastError any
	if state == "failed" {
		lastError = trimProductTestError(message)
	}
	if _, err := tx.Exec(ctx, `UPDATE app_product_version_tests
		SET state=$2,attempts=attempts+1,last_error=$3,encrypted_secrets='{}'::jsonb,completed_at=now(),updated_at=now()
		WHERE id=$1`, id, state, lastError); err != nil {
		return err
	}
	action := "product.test.succeeded"
	if state == "failed" {
		action = "product.test.failed"
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		SELECT requested_by,$2::text,'app_product_version_test',$1::text,'worker/product-test/'||$1::text,jsonb_build_object('state',$3::text)
		FROM app_product_version_tests WHERE id=$1::uuid`, id, action, state)
	return err
}

func failProductVersionTest(ctx context.Context, db *pgxpool.Pool, id string, cause error, logger *slog.Logger) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err == nil {
		defer tx.Rollback(ctx)
		var state string
		err = tx.QueryRow(ctx, "SELECT state FROM app_product_version_tests WHERE id=$1 FOR UPDATE", id).Scan(&state)
		if err == nil && state != "succeeded" && state != "failed" {
			err = completeProductVersionTest(ctx, tx, id, "failed", cause.Error())
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
	}
	if err != nil {
		logger.Error("product test failure persistence failed", "test", id, "error", err)
	}
	cleanupProductVersionTestRuntime(ctx, id, logger)
}

func cleanupProductVersionTestRuntime(ctx context.Context, id string, logger *slog.Logger) {
	if executor == nil {
		return
	}
	for _, container := range uniqueStrings(productTestContainerName(id), legacyProductTestContainerName(id)) {
		if err := executor.RemoveIfExists(ctx, container); err != nil {
			logger.Warn("product test container cleanup failed", "test", id, "container", container, "error", err)
		}
	}
	for _, probe := range uniqueStrings(productTestHealthContainerName(id), productTestHealthLegacyContainerName(id), legacyProductTestHealthContainerName(id), legacyProductTestHealthLegacyContainerName(id)) {
		if err := executor.RemoveIfExists(ctx, probe); err != nil {
			logger.Warn("product test health probe cleanup failed", "test", id, "container", probe, "error", err)
		}
	}
	for _, network := range uniqueStrings(productTestNetworkName(id), legacyProductTestNetworkName(id)) {
		if egressProxyContainer != "" {
			if err := executor.DisconnectNetwork(ctx, network, egressProxyContainer); err != nil {
				logger.Warn("product test proxy disconnect failed", "test", id, "network", network, "error", err)
			}
		}
		if err := executor.RemoveNetwork(ctx, network); err != nil {
			logger.Warn("product test network cleanup failed", "test", id, "network", network, "error", err)
		}
	}
}

func reconcileProductTestContainers(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	if executor == nil {
		return
	}
	active, err := activeProductVersionTestIDs(ctx, db)
	if err != nil {
		logger.Warn("product test ownership scan failed", "error", err)
		return
	}
	names, err := executor.ContainerNames(ctx, productTestReconcilePrefix())
	if err != nil {
		logger.Warn("product test container reconciliation failed", "error", err)
		return
	}
	healthNames, err := executor.ContainerNames(ctx, productTestHealthReconcilePrefix())
	if err != nil {
		logger.Warn("product test health container reconciliation failed", "error", err)
		return
	}
	names = append(names, healthNames...)
	for _, name := range names {
		id, ok := productTestIDFromContainerName(name)
		if ok {
			if _, keep := active[id]; !keep {
				cleanupProductVersionTestRuntime(ctx, id, logger)
			}
			continue
		}
		if isProductTestHealthContainerName(name) && !productTestHealthOwnerActive(name, active) {
			if err := executor.RemoveIfExists(ctx, name); err != nil {
				logger.Warn("orphan product test health probe cleanup failed", "container", name, "error", err)
			} else {
				logger.Info("orphan product test health probe removed", "container", name)
			}
		}
	}

	networks, err := executor.NetworkNames(ctx, productTestNetworkReconcilePrefix())
	if err != nil {
		logger.Warn("product test network reconciliation failed", "error", err)
		return
	}
	for _, network := range networks {
		id, ok := productTestIDFromNetworkName(network)
		if !ok {
			continue
		}
		if _, keep := active[id]; !keep {
			cleanupProductVersionTestRuntime(ctx, id, logger)
		}
	}
}

func activeProductVersionTestIDs(ctx context.Context, db *pgxpool.Pool) (map[string]struct{}, error) {
	rows, err := db.Query(ctx, "SELECT id::text FROM app_product_version_tests WHERE state NOT IN ('succeeded','failed')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		active[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return active, nil
}

func trimProductTestError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2048 {
		return value[:2048]
	}
	return value
}

func uniqueStrings(values ...string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func productTestReconcilePrefix() string {
	if runtimeScope == "" {
		return "cm-test-"
	}
	return "cm-test-" + runtimeScope + "-"
}
func productTestNetworkReconcilePrefix() string {
	if runtimeScope == "" {
		return "cm-test-net-"
	}
	return "cm-test-net-" + runtimeScope + "-"
}
func productTestHealthReconcilePrefix() string {
	if runtimeScope == "" {
		return "cm-test-health-"
	}
	return "cm-test-health-" + runtimeScope + "-"
}
func productTestContainerName(id string) string       { return productTestReconcilePrefix() + id }
func legacyProductTestContainerName(id string) string { return "cm-test-" + id }
func productTestNetworkName(id string) string {
	return productTestNetworkReconcilePrefix() + strings.ReplaceAll(id, "-", "")
}
func legacyProductTestNetworkName(id string) string {
	return "cm-test-net-" + strings.ReplaceAll(id, "-", "")
}
func productTestAlias(id string) string { return "test-" + strings.ReplaceAll(id, "-", "")[:12] }
func productTestHealthContainerName(id string) string {
	return productTestHealthReconcilePrefix() + id
}
func legacyProductTestHealthContainerName(id string) string { return "cm-test-health-" + id }
func productTestHealthLegacyContainerName(id string) string {
	compact := strings.ReplaceAll(id, "-", "")
	if len(compact) > 12 {
		compact = compact[:12]
	}
	if runtimeScope == "" {
		return "cm-test-health-" + compact
	}
	return "cm-test-health-" + runtimeScope + "-" + compact
}
func legacyProductTestHealthLegacyContainerName(id string) string {
	compact := strings.ReplaceAll(id, "-", "")
	if len(compact) > 12 {
		compact = compact[:12]
	}
	return "cm-test-health-" + compact
}

func productTestIDFromContainerName(name string) (string, bool) {
	value := strings.TrimPrefix(name, productTestReconcilePrefix())
	if value == name || !regexpUUID.MatchString(value) {
		return "", false
	}
	return value, true
}

func productTestIDFromNetworkName(name string) (string, bool) {
	compact := strings.TrimPrefix(name, productTestNetworkReconcilePrefix())
	if compact == name || !regexpCompactUUID.MatchString(compact) {
		return "", false
	}
	id := compact[:8] + "-" + compact[8:12] + "-" + compact[12:16] + "-" + compact[16:20] + "-" + compact[20:]
	return id, regexpUUID.MatchString(id)
}

func isProductTestHealthContainerName(name string) bool {
	prefix := productTestHealthReconcilePrefix()
	value := strings.TrimPrefix(name, prefix)
	return value != name && (regexpUUID.MatchString(value) || regexpHealthPrefix.MatchString(value))
}

func productTestHealthOwnerActive(name string, active map[string]struct{}) bool {
	prefix := productTestHealthReconcilePrefix()
	value := strings.TrimPrefix(name, prefix)
	if value == name {
		return false
	}
	if regexpUUID.MatchString(value) {
		_, ok := active[value]
		return ok
	}
	if !regexpHealthPrefix.MatchString(value) {
		return false
	}
	for id := range active {
		if strings.HasPrefix(strings.ReplaceAll(id, "-", ""), value) {
			return true
		}
	}
	return false
}

var regexpUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var regexpCompactUUID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var regexpHealthPrefix = regexp.MustCompile(`^[0-9a-f]{12}$`)
