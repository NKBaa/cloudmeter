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
	"sort"
	"strings"
	"time"

	runtimepolicy "cloudmeter/internal/runtime"

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
		WHERE state NOT IN ('succeeded','failed','cancelled') AND available_at<=now()
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
		failProductVersionTest(ctx, db, id, productTestFailure("运行环境检查", "Docker Engine", fmt.Errorf("Docker executor is not enabled"), nil), logger)
		return
	}

	var spec productVersionTestSnapshot
	if json.Unmarshal(snapshot, &spec) != nil || strings.TrimSpace(spec.Image) == "" {
		tx.Rollback(ctx)
		failProductVersionTest(ctx, db, id, productTestFailure("读取版本配置", "版本快照", fmt.Errorf("test deployment snapshot is invalid"), nil), logger)
		return
	}
	operationCtx, cancelOperation := context.WithCancel(ctx)
	productTestOperations.Store(id, cancelOperation)
	defer func() {
		productTestOperations.Delete(id)
		cancelOperation()
	}()
	var cancellationRequested bool
	if err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_product_version_test_cancellations
		WHERE test_id=$1 AND processed_at IS NULL)`, id).Scan(&cancellationRequested); err != nil {
		logger.Error("product test cancellation check failed", "test", id, "error", err)
		return
	}
	if cancellationRequested {
		return
	}
	switch state {
	case "queued":
		if err = advanceProductVersionTest(ctx, tx, id, "pulling", 0); err != nil {
			logger.Error("product test queue transition failed", "test", id, "error", err)
			return
		}
	case "pulling":
		if _, err = pullConfiguredProductImage(operationCtx, db, spec.Image); err != nil {
			tx.Rollback(ctx)
			failProductVersionTest(ctx, db, id, productTestFailure("拉取应用镜像", spec.Image, err, productTestSecretValues(id, encryptedSecrets)), logger)
			return
		}
		if companions, companionErr := runtimepolicy.RuntimeCompanions(spec.Runtime); companionErr != nil {
			tx.Rollback(ctx)
			failProductVersionTest(ctx, db, id, productTestFailure("读取组合容器配置", "runtimeSpec.companions", companionErr, productTestSecretValues(id, encryptedSecrets)), logger)
			return
		} else {
			for _, companion := range companions {
				if _, pullErr := pullConfiguredProductImage(operationCtx, db, companion.Image); pullErr != nil {
					tx.Rollback(ctx)
					failProductVersionTest(ctx, db, id, productTestFailure("拉取组合容器镜像", companion.Image, pullErr, productTestSecretValues(id, encryptedSecrets)), logger)
					return
				}
			}
		}
		if _, needsProbe := healthPath(spec.Health); needsProbe {
			if err = executor.Pull(operationCtx, backupHelperImage); err != nil {
				tx.Rollback(ctx)
				failProductVersionTest(ctx, db, id, productTestFailure("准备健康检查组件", backupHelperImage, err, productTestSecretValues(id, encryptedSecrets)), logger)
				return
			}
		}
		if err = advanceProductVersionTest(ctx, tx, id, "starting", 0); err != nil {
			logger.Error("product test pull transition failed", "test", id, "error", err)
			return
		}
	case "starting":
		if err = startProductVersionTestContainer(operationCtx, db, id, spec, encryptedSecrets); err != nil {
			tx.Rollback(ctx)
			failProductVersionTest(ctx, db, id, productTestFailure("创建并启动测试容器", spec.Image, err, productTestSecretValues(id, encryptedSecrets)), logger)
			return
		}
		if err = advanceProductVersionTest(ctx, tx, id, "health_checking", healthInterval(snapshot)); err != nil {
			logger.Error("product test start transition failed", "test", id, "error", err)
			return
		}
	case "health_checking":
		healthy, healthDetail, healthErr := productVersionTestHealthy(operationCtx, id, spec)
		if healthErr != nil {
			tx.Rollback(ctx)
			failProductVersionTest(ctx, db, id, productTestFailure("执行健康检查", spec.Image, healthErr, productTestSecretValues(id, encryptedSecrets)), logger)
			return
		}
		healthDetail = redactProductTestDiagnostics(healthDetail, productTestSecretValues(id, encryptedSecrets))
		if healthy {
			if err = completeProductVersionTest(ctx, tx, id, "succeeded", ""); err != nil {
				logger.Error("product test completion failed", "test", id, "error", err)
				return
			}
			terminal = true
		} else if healthAttempts+1 >= productTestMaxHealthAttempts {
			message := fmt.Sprintf("测试阶段：执行健康检查\n检查对象：%s\n失败原因：健康检查在 %d 次尝试后仍未通过\n处理建议：确认容器监听端口、健康检查路径和启动耗时；需要较长预热时间时可适当增大检查间隔。", spec.Image, productTestMaxHealthAttempts)
			if healthDetail != "" {
				message += "\n技术详情（已脱敏）：\n" + healthDetail
			}
			if err = completeProductVersionTest(ctx, tx, id, "failed", message); err != nil {
				logger.Error("product test failure completion failed", "test", id, "error", err)
				return
			}
			terminal = true
		} else if _, err = tx.Exec(ctx, `UPDATE app_product_version_tests
			SET attempts=attempts+1,health_attempts=health_attempts+1,last_error=nullif($3,''),
				available_at=now()+make_interval(secs=>$2),updated_at=now()
			WHERE id=$1`, id, healthInterval(snapshot), trimProductTestError(healthDetail)); err != nil {
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

func startProductVersionTestContainer(ctx context.Context, db *pgxpool.Pool, id string, spec productVersionTestSnapshot, encryptedSecrets map[string]string) error {
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
	image, err := configuredProductImage(ctx, db, spec.Image)
	if err != nil {
		return err
	}
	if err = executor.CreateProductTest(ctx, container, image, network, []string{productTestAlias(id)}, runtimeSpec); err != nil {
		return err
	}
	companions, err := runtimepolicy.RuntimeCompanions(spec.Runtime)
	if err != nil {
		return err
	}
	for _, companion := range companions {
		companionImage, imageErr := configuredProductImage(ctx, db, companion.Image)
		if imageErr != nil {
			return imageErr
		}
		companionRuntime := runtimepolicy.CompanionRuntimeSpec(id, companion, runtimepolicy.VolumeMounts(spec.Runtime))
		companionRuntime["testId"] = id
		if err = executor.CreateProductTest(ctx, productTestContainerName(id)+"-"+companion.Key, companionImage, network, []string{companion.ServiceName}, companionRuntime); err != nil {
			return err
		}
		if err = executor.Start(ctx, productTestContainerName(id)+"-"+companion.Key); err != nil {
			return err
		}
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

func productVersionTestHealthy(ctx context.Context, id string, spec productVersionTestSnapshot) (bool, string, error) {
	container, network := productTestContainerName(id), productTestNetworkName(id)
	healthy, err := executor.Healthy(ctx, container)
	if err != nil && dockerResourceMissing(err) && runtimeScope != "" {
		container, network = legacyProductTestContainerName(id), legacyProductTestNetworkName(id)
		healthy, err = executor.Healthy(ctx, container)
	}
	if err != nil {
		return false, "", err
	}
	if !healthy {
		detail := "容器尚未进入可用状态"
		if diagnostics, diagnosticsErr := executor.ContainerDiagnostics(ctx, container, 80); diagnosticsErr == nil && strings.TrimSpace(diagnostics) != "" {
			detail += "。\n容器诊断：\n" + diagnostics
		}
		return false, detail, nil
	}
	if !snapshotHealthOK(mustMarshalProductTestHealth(spec.Health)) {
		return false, "模板要求模拟健康检查失败", nil
	}
	path, ok := healthPath(spec.Health)
	if !ok {
		return true, "", nil
	}
	target := fmt.Sprintf("http://%s:%d%s", productTestAlias(id), routePort(spec.Route), path)
	probeName := productTestHealthContainerName(id)
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(healthTimeout(spec.Health)+5)*time.Second)
	defer cancel()
	err = executor.ProbeHTTP(probeCtx, probeName, backupHelperImage, network, target, healthTimeout(spec.Health), healthAcceptedStatusCodes(spec.Health))
	if err == nil {
		return true, "", nil
	}
	detail := fmt.Sprintf("探测地址：%s\n探测失败：%s", target, err)
	if diagnostics, diagnosticsErr := executor.ContainerDiagnostics(ctx, container, 100); diagnosticsErr == nil && strings.TrimSpace(diagnostics) != "" {
		detail += "\n容器诊断：\n" + diagnostics
	}
	return false, detail, nil
}

func productTestSecretValues(id string, encryptedSecrets map[string]string) []string {
	values := make([]string, 0, len(encryptedSecrets)+2)
	for key, encrypted := range encryptedSecrets {
		if secrets == nil || encrypted == "" {
			continue
		}
		if value, err := secrets.Decrypt("product.version.test."+id+"."+key, encrypted); err == nil && value != "" {
			values = append(values, value)
		}
	}
	if egressToken != "" {
		mac := hmac.New(sha256.New, []byte(egressToken))
		_, _ = mac.Write([]byte(id))
		values = append(values, hex.EncodeToString(mac.Sum(nil)))
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

func redactProductTestDiagnostics(value string, secretValues []string) string {
	for _, secret := range secretValues {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[已脱敏]")
		}
	}
	value = regexpAuthorization.ReplaceAllString(value, "$1[已脱敏]")
	value = regexpURLCredential.ReplaceAllString(value, "$1[已脱敏]@")
	value = regexpSensitiveAssignment.ReplaceAllString(value, "$1[已脱敏]")
	return trimProductTestError(value)
}

func productTestFailure(stage, target string, cause error, secretValues []string) error {
	detail := "未返回底层错误详情"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		detail = redactProductTestDiagnostics(cause.Error(), secretValues)
	}
	lower := strings.ToLower(detail)
	reason := "容器运行时返回错误"
	suggestion := "根据下方技术详情核对产品版本配置；修正后重新执行测试部署。"
	switch {
	case strings.Contains(lower, "no such image"), strings.Contains(lower, "manifest unknown"), strings.Contains(lower, "manifest not found"):
		reason = "镜像或填写的版本号不存在"
		suggestion = "核对完整镜像地址和版本号；不要依赖已被删除的 latest 标签。私有镜像还需在“Docker 与镜像源”中配置 Registry 凭据。"
	case strings.Contains(lower, "pull access denied"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authentication required"), strings.Contains(lower, "denied"):
		reason = "Registry 拒绝访问镜像"
		suggestion = "确认镜像可见性，并在“Docker 与镜像源”中填写正确的 Registry 地址、用户名和密码。"
	case strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "timeout"), strings.Contains(lower, "timed out"):
		reason = "连接镜像库或容器服务超时"
		suggestion = "检查宿主机网络、Docker 镜像加速/代理和 DNS；必要时在“Docker 与镜像源”中提高拉取超时。"
	case strings.Contains(lower, "connection refused"):
		reason = "目标服务拒绝连接"
		suggestion = "确认容器内网监听端口与应用实际端口一致，并检查应用是否已完成启动。"
	case strings.Contains(lower, "exec format error"), strings.Contains(lower, "no matching manifest"):
		reason = "镜像不支持当前服务器架构"
		suggestion = "选择包含当前 CPU 架构的多架构镜像，或发布与服务器架构匹配的版本。"
	case strings.Contains(lower, "docker executor is not enabled"):
		reason = "Worker 尚未连接 Docker Engine"
		suggestion = "检查 Worker 的 Docker Socket 挂载和运行时配置，然后重启 Worker。"
	case strings.Contains(lower, "snapshot is invalid"):
		reason = "版本快照不完整或已损坏"
		suggestion = "重新创建该产品版本；若仍复现，请检查数据库迁移是否完整。"
	}
	target = strings.TrimSpace(redactProductTestDiagnostics(target, secretValues))
	if target == "" {
		target = "未识别"
	}
	return fmt.Errorf("测试阶段：%s\n检查对象：%s\n失败原因：%s\n处理建议：%s\n技术详情（已脱敏）：\n%s", stage, target, reason, suggestion, detail)
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
	action := map[string]string{"succeeded": "product.test.succeeded", "failed": "product.test.failed", "cancelled": "product.test.cancelled"}[state]
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
		if err == nil && state != "succeeded" && state != "failed" && state != "cancelled" {
			var cancellationRequested bool
			err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM app_product_version_test_cancellations WHERE test_id=$1 AND processed_at IS NULL)", id).Scan(&cancellationRequested)
			if err == nil && cancellationRequested {
				err = completeProductVersionTest(ctx, tx, id, "cancelled", "")
			} else if err == nil {
				err = completeProductVersionTest(ctx, tx, id, "failed", cause.Error())
			}
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

func runProductVersionTestCancellationWorker(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		processProductVersionTestCancellationOne(ctx, db, logger)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func processProductVersionTestCancellationOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	var pendingID string
	if err := db.QueryRow(ctx, `SELECT test_id::text FROM app_product_version_test_cancellations
		WHERE processed_at IS NULL ORDER BY requested_at LIMIT 1`).Scan(&pendingID); err == pgx.ErrNoRows {
		return
	} else if err != nil {
		logger.Error("product test cancellation lookup failed", "error", err)
		return
	}
	if cancel, ok := productTestOperations.Load(pendingID); ok {
		cancel.(context.CancelFunc)()
	}
	cleanupProductVersionTestRuntime(ctx, pendingID, logger)

	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		logger.Error("product test cancellation transaction failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)
	var id, state string
	err = tx.QueryRow(ctx, `SELECT c.test_id::text,t.state
		FROM app_product_version_test_cancellations c
		JOIN app_product_version_tests t ON t.id=c.test_id
		WHERE c.processed_at IS NULL
		ORDER BY c.requested_at FOR UPDATE OF c,t SKIP LOCKED LIMIT 1`).Scan(&id, &state)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("product test cancellation claim failed", "error", err)
		return
	}
	if state != "succeeded" && state != "failed" && state != "cancelled" {
		if err = completeProductVersionTest(ctx, tx, id, "cancelled", ""); err != nil {
			logger.Error("product test cancellation completion failed", "test", id, "error", err)
			return
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE app_product_version_test_cancellations SET processed_at=now() WHERE test_id=$1`, id); err != nil {
		logger.Error("product test cancellation acknowledgement failed", "test", id, "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("product test cancellation commit failed", "test", id, "error", err)
		return
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
	if names, err := executor.ContainerNames(ctx, productTestContainerName(id)+"-"); err == nil {
		for _, name := range names {
			_ = executor.RemoveIfExists(ctx, name)
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
	rows, err := db.Query(ctx, "SELECT id::text FROM app_product_version_tests WHERE state NOT IN ('succeeded','failed','cancelled')")
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
	runes := []rune(value)
	if len(runes) > 6000 {
		return string(runes[:6000]) + "\n……诊断信息已截断"
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
var regexpAuthorization = regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|basic)\s+)[^\s]+`)
var regexpURLCredential = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)
var regexpSensitiveAssignment = regexp.MustCompile(`(?i)(\b(?:password|passwd|token|secret|api[_-]?key|access[_-]?key)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
