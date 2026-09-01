package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr              string
	DatabaseURL       string
	SessionTTLHours   int
	TrustedProxyCIDRs []string
	DockerSocket      string
	DockerExecutor    bool
	RuntimeOwner      string
	RouterContainer   string
	RouterToken       string
	BackupVolume      string
	BackupStoragePath string
	BackupHelperImage string
	PublicBaseURL     string
	SecretsKey        []byte
	EgressIngestToken string
	EgressProxy       string
	CaddyAdminURL     string
	CaddyfilePath     string
	StandalonePort    int
	GatewayAccessMode string
}

func Load() (Config, error) {
	ttl, err := strconv.Atoi(env("SESSION_TTL_HOURS", "24"))
	if err != nil || ttl < 1 {
		return Config{}, fmt.Errorf("SESSION_TTL_HOURS must be a positive integer")
	}
	standalonePort, err := strconv.Atoi(env("PLATFORM_PORT", "8080"))
	if err != nil || standalonePort < 1 || standalonePort > 65535 {
		return Config{}, fmt.Errorf("PLATFORM_PORT must be an integer between 1 and 65535")
	}
	var key []byte
	if rawKey := strings.TrimSpace(os.Getenv("SECRETS_ENCRYPTION_KEY")); rawKey != "" {
		key, err = base64.RawStdEncoding.DecodeString(rawKey)
		if err != nil || len(key) != 32 {
			return Config{}, fmt.Errorf("SECRETS_ENCRYPTION_KEY must be base64 without padding for exactly 32 bytes")
		}
	}
	cfg := Config{
		Addr:              env("API_ADDR", ":8081"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		SessionTTLHours:   ttl,
		DockerSocket:      env("DOCKER_SOCKET", "/var/run/docker.sock"),
		DockerExecutor:    env("DOCKER_EXECUTOR_ENABLED", "false") == "true",
		RuntimeOwner:      env("RUNTIME_OWNER", "cloudmeter"),
		RouterContainer:   env("ROUTER_CONTAINER_NAME", "cloudmeter-app-router"),
		RouterToken:       strings.TrimSpace(os.Getenv("ROUTER_INTERNAL_TOKEN")),
		BackupVolume:      env("BACKUP_STORAGE_VOLUME", "cloudmeter_backup_data"),
		BackupStoragePath: env("BACKUP_STORAGE_PATH", "/var/lib/cloudmeter/backups"),
		BackupHelperImage: env("BACKUP_HELPER_IMAGE", "nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10"),
		PublicBaseURL:     env("PUBLIC_BASE_URL", ""),
		SecretsKey:        key,
		EgressIngestToken: env("EGRESS_INGEST_TOKEN", ""),
		EgressProxy:       env("EGRESS_PROXY_CONTAINER_NAME", "cloudmeter-egress-proxy"),
		CaddyAdminURL:     strings.TrimRight(env("CADDY_ADMIN_URL", "http://gateway:2019"), "/"),
		CaddyfilePath:     env("CADDYFILE_PATH", "/etc/cloudmeter/Caddyfile"),
		StandalonePort:    standalonePort,
		GatewayAccessMode: strings.TrimSpace(os.Getenv("GATEWAY_ACCESS_MODE")),
	}
	if cfg.GatewayAccessMode != "" && cfg.GatewayAccessMode != "apps_only" && cfg.GatewayAccessMode != "all_caddy" {
		return Config{}, fmt.Errorf("GATEWAY_ACCESS_MODE must be empty, apps_only, or all_caddy")
	}
	if raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXY_CIDRS")); raw != "" {
		cfg.TrustedProxyCIDRs = strings.Split(raw, ",")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
