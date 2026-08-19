package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type workerDockerSettings struct {
	DefaultRegistry  string
	RegistryUsername string
	RegistryPassword string
	PullTimeout      time.Duration
}

func loadWorkerDockerSettings(ctx context.Context, db *pgxpool.Pool) (workerDockerSettings, error) {
	var settings workerDockerSettings
	var encryptedPassword string
	var timeoutSeconds int
	err := db.QueryRow(ctx, `SELECT default_registry,registry_username,registry_password,pull_timeout_seconds
		FROM docker_runtime_settings WHERE singleton`).Scan(
		&settings.DefaultRegistry, &settings.RegistryUsername, &encryptedPassword, &timeoutSeconds,
	)
	if err != nil {
		return workerDockerSettings{}, err
	}
	settings.DefaultRegistry = strings.Trim(strings.TrimSpace(settings.DefaultRegistry), "/")
	settings.PullTimeout = time.Duration(timeoutSeconds) * time.Second
	if settings.PullTimeout < 30*time.Second || settings.PullTimeout > 30*time.Minute {
		settings.PullTimeout = 5 * time.Minute
	}
	if encryptedPassword != "" {
		password, decryptErr := secrets.Decrypt("docker.registry.password", encryptedPassword)
		if decryptErr != nil {
			return workerDockerSettings{}, fmt.Errorf("Docker Registry credential authentication failed: %w", decryptErr)
		}
		settings.RegistryPassword = password
	}
	return settings, nil
}

func resolveConfiguredImage(image, defaultRegistry string) string {
	image = strings.TrimSpace(image)
	defaultRegistry = strings.Trim(strings.TrimSpace(defaultRegistry), "/")
	if image == "" || defaultRegistry == "" || imageHasRegistry(image) {
		return image
	}
	return defaultRegistry + "/" + image
}

func imageHasRegistry(image string) bool {
	first := image
	if slash := strings.IndexByte(first, '/'); slash >= 0 {
		first = first[:slash]
	} else {
		return false
	}
	return first == "localhost" || strings.Contains(first, ".") || strings.Contains(first, ":")
}

func imageRegistryHost(image string) string {
	first := image
	if slash := strings.IndexByte(first, '/'); slash >= 0 {
		first = first[:slash]
	}
	if first == "localhost" || strings.Contains(first, ".") || strings.Contains(first, ":") {
		return first
	}
	return "docker.io"
}

func pullConfiguredProductImage(ctx context.Context, db *pgxpool.Pool, image string) (string, error) {
	settings, err := loadWorkerDockerSettings(ctx, db)
	if err != nil {
		return "", err
	}
	resolved := resolveConfiguredImage(image, settings.DefaultRegistry)
	pullCtx, cancel := context.WithTimeout(ctx, settings.PullTimeout)
	defer cancel()
	username, password, serverAddress := "", "", ""
	if settings.DefaultRegistry != "" && imageRegistryHost(resolved) == strings.Split(settings.DefaultRegistry, "/")[0] {
		username, password = settings.RegistryUsername, settings.RegistryPassword
		serverAddress = imageRegistryHost(resolved)
	}
	if err = executor.PullWithRegistry(pullCtx, resolved, username, password, serverAddress); err != nil {
		if pullCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("镜像拉取超过管理员设置的 %s 超时：%s", settings.PullTimeout, resolved)
		}
		return "", err
	}
	return resolved, nil
}

func configuredProductImage(ctx context.Context, db *pgxpool.Pool, image string) (string, error) {
	settings, err := loadWorkerDockerSettings(ctx, db)
	if err != nil {
		return "", err
	}
	return resolveConfiguredImage(image, settings.DefaultRegistry), nil
}

func syncDockerDaemonSettings(ctx context.Context, db *pgxpool.Pool) {
	if executor == nil {
		return
	}
	actual, err := executor.DaemonSettings(ctx)
	if err != nil {
		_, _ = db.Exec(ctx, `UPDATE docker_runtime_settings SET last_checked_at=now(),last_check_error=$1 WHERE singleton`, trimProductTestError(err.Error()))
		return
	}
	for i, mirror := range actual.RegistryMirrors {
		actual.RegistryMirrors[i] = strings.TrimRight(strings.TrimSpace(mirror), "/")
	}
	_, _ = db.Exec(ctx, `UPDATE docker_runtime_settings SET detected_registry_mirrors=$1,detected_http_proxy=$2,
		detected_https_proxy=$3,detected_no_proxy=$4,last_checked_at=now(),last_check_error='' WHERE singleton`,
		actual.RegistryMirrors, actual.HTTPProxy, actual.HTTPSProxy, actual.NoProxy)
}
