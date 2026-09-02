package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"cloudmeter/internal/config"
	runtimepolicy "cloudmeter/internal/runtime"
	"cloudmeter/internal/secretbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	db      *pgxpool.Pool
	cfg     config.Config
	logger  *slog.Logger
	mux     *http.ServeMux
	secrets *secretbox.Box
}

type principal struct {
	ID, Email, DisplayName string
	SessionID              string `json:"-"`
	Roles                  []string
	ActorID                string `json:"ActorID,omitempty"`
	ActorEmail             string `json:"ActorEmail,omitempty"`
	ActorDisplayName       string `json:"ActorDisplayName,omitempty"`
	Impersonating          bool   `json:"Impersonating"`
	ImpersonationReadOnly  bool   `json:"ImpersonationReadOnly"`
}

func (p principal) auditActorID() string {
	if p.Impersonating && p.ActorID != "" {
		return p.ActorID
	}
	return p.ID
}

type contextKey string

const principalKey contextKey = "principal"

func New(ctx context.Context, db *pgxpool.Pool, cfg config.Config, logger *slog.Logger) (*Server, error) {
	box, err := secretbox.New(cfg.SecretsKey)
	if err != nil {
		return nil, fmt.Errorf("configure credential encryption: %w", err)
	}
	s := &Server{db: db, cfg: cfg, logger: logger, mux: http.NewServeMux(), secrets: box}
	if err = s.migrateStoredCredentials(ctx); err != nil {
		return nil, fmt.Errorf("migrate stored credentials: %w", err)
	}
	if err = s.restoreGatewayCertificates(ctx); err != nil {
		return nil, fmt.Errorf("restore gateway certificates: %w", err)
	}
	if err = s.prepareGatewayAccessModeOverride(ctx); err != nil {
		return nil, fmt.Errorf("apply gateway access mode override: %w", err)
	}
	s.routes()
	return s, nil
}

func (s *Server) migrateStoredCredentials(ctx context.Context) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(729104503)); err != nil {
		return err
	}
	var smtpPassword string
	if err = tx.QueryRow(ctx, "SELECT password FROM smtp_settings WHERE singleton FOR UPDATE").Scan(&smtpPassword); err != nil {
		return err
	}
	if smtpPassword != "" && !secretbox.IsEncrypted(smtpPassword) {
		encrypted, encryptErr := s.secrets.Encrypt("smtp.password", smtpPassword)
		if encryptErr != nil {
			return encryptErr
		}
		if _, err = tx.Exec(ctx, "UPDATE smtp_settings SET password=$1 WHERE singleton", encrypted); err != nil {
			return err
		}
	} else if smtpPassword != "" {
		if _, err = s.secrets.Decrypt("smtp.password", smtpPassword); err != nil {
			return fmt.Errorf("validate SMTP password: %w", err)
		}
	}
	for _, table := range []struct {
		name, keyColumn, secretColumn, contextPrefix string
	}{
		{"oauth_settings", "provider", "client_secret", "oauth.client_secret."},
		{"payment_provider_configs", "provider", "secret", "payment.secret."},
	} {
		query := fmt.Sprintf("SELECT %s,%s FROM %s FOR UPDATE", table.keyColumn, table.secretColumn, table.name)
		rows, queryErr := tx.Query(ctx, query)
		if queryErr != nil {
			return queryErr
		}
		values := map[string]string{}
		for rows.Next() {
			var key, value string
			if scanErr := rows.Scan(&key, &value); scanErr != nil {
				rows.Close()
				return scanErr
			}
			values[key] = value
		}
		rows.Close()
		for key, value := range values {
			if value == "" {
				continue
			}
			if secretbox.IsEncrypted(value) {
				if _, decryptErr := s.secrets.Decrypt(table.contextPrefix+key, value); decryptErr != nil {
					return fmt.Errorf("validate %s credential %s: %w", table.name, key, decryptErr)
				}
				continue
			}
			encrypted, encryptErr := s.secrets.Encrypt(table.contextPrefix+key, value)
			if encryptErr != nil {
				return encryptErr
			}
			update := fmt.Sprintf("UPDATE %s SET %s=$1 WHERE %s=$2", table.name, table.secretColumn, table.keyColumn)
			if _, err = tx.Exec(ctx, update, encrypted, key); err != nil {
				return err
			}
		}
	}
	rows, err := tx.Query(ctx, "SELECT id::text,encrypted_value FROM app_secret_versions FOR UPDATE")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, value string
		if err = rows.Scan(&id, &value); err != nil {
			rows.Close()
			return err
		}
		if !secretbox.IsEncrypted(value) {
			rows.Close()
			return fmt.Errorf("application secret %s is not encrypted", id)
		}
		if _, err = s.secrets.Decrypt("app.secret.version."+id, value); err != nil {
			rows.Close()
			return fmt.Errorf("validate application secret %s: %w", id, err)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Server) Handler() http.Handler {
	return s.recoverPanic(s.requestID(s.securityHeaders(s.mux)))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/healthz", s.health)
	s.mux.HandleFunc("GET /api/setup/status", s.setupStatus)
	s.mux.HandleFunc("GET /api/system/settings", s.getSystemSettingsPublic)
	s.mux.HandleFunc("GET /api/pricing", s.getPricingPublic)
	s.mux.HandleFunc("GET /api/pricing/catalog", s.getPricingCatalogPublic)
	s.mux.HandleFunc("GET /api/homepage", s.getHomepage)
	s.mux.HandleFunc("POST /api/setup/initialize", s.initialize)
	s.mux.HandleFunc("POST /api/auth/login", s.login)
	s.mux.HandleFunc("POST /api/auth/register", s.register)
	s.mux.HandleFunc("GET /api/auth/registration-policy", s.registrationPolicy)
	s.mux.HandleFunc("POST /api/auth/verification-code", s.sendVerificationCode)
	s.mux.HandleFunc("GET /api/auth/oauth/providers", s.oauthProviders)
	s.mux.HandleFunc("POST /api/auth/oauth/{provider}/start", s.oauthStart)
	s.mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", s.oauthCallback)
	s.mux.HandleFunc("POST /api/auth/oauth/exchange", s.oauthExchange)
	s.mux.Handle("POST /api/auth/logout", s.authenticate(http.HandlerFunc(s.logout)))
	s.mux.Handle("GET /api/me", s.authenticate(http.HandlerFunc(s.me)))
	s.mux.Handle("DELETE /api/impersonation", s.authenticate(http.HandlerFunc(s.endImpersonation)))
	s.mux.Handle("GET /api/announcements", s.authenticate(http.HandlerFunc(s.listAnnouncements)))
	s.mux.Handle("GET /api/faqs", s.authenticate(http.HandlerFunc(s.listFAQs)))
	s.mux.Handle("GET /api/sidebar-visibility", s.authenticate(http.HandlerFunc(s.sidebarVisibility)))
	s.mux.Handle("GET /api/notifications", s.authenticate(http.HandlerFunc(s.listNotifications)))
	s.mux.Handle("GET /api/settings/balance-alert", s.authenticate(http.HandlerFunc(s.getBalanceAlertSettings)))
	s.mux.Handle("PUT /api/settings/balance-alert", s.authenticate(http.HandlerFunc(s.updateBalanceAlertSettings)))
	s.mux.Handle("POST /api/invites", s.authenticate(http.HandlerFunc(s.createInvite)))
	s.mux.Handle("POST /api/invites/redeem", s.authenticate(http.HandlerFunc(s.redeemInvite)))
	s.mux.Handle("PATCH /api/notifications/{notificationID}/read", s.authenticate(http.HandlerFunc(s.readNotification)))
	s.mux.Handle("GET /api/tickets", s.authenticate(http.HandlerFunc(s.listTickets)))
	s.mux.Handle("POST /api/tickets", s.authenticate(http.HandlerFunc(s.createTicket)))
	s.mux.Handle("GET /api/tickets/{ticketID}", s.authenticate(http.HandlerFunc(s.getTicket)))
	s.mux.Handle("POST /api/tickets/{ticketID}/messages", s.authenticate(http.HandlerFunc(s.replyTicket)))
	s.mux.Handle("POST /api/tickets/{ticketID}/escalate", s.authenticate(http.HandlerFunc(s.escalateTicket)))
	s.mux.Handle("POST /api/tickets/{ticketID}/close", s.authenticate(http.HandlerFunc(s.closeTicket)))
	s.mux.Handle("GET /api/admin/summary", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.adminSummary))))
	s.mux.Handle("GET /api/admin/host-metrics", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.hostMetrics))))
	s.mux.Handle("GET /api/admin/audit-logs", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.adminAuditLogs))))
	s.mux.Handle("GET /api/products", s.authenticate(http.HandlerFunc(s.listProducts)))
	s.mux.Handle("GET /api/apps", s.authenticate(http.HandlerFunc(s.listApps)))
	s.mux.Handle("GET /api/apps/runtime-metrics", s.authenticate(http.HandlerFunc(s.appRuntimeMetrics)))
	s.mux.Handle("GET /api/apps/{appID}/deployments", s.authenticate(http.HandlerFunc(s.appDeployments)))
	s.mux.Handle("GET /api/apps/{appID}/releases", s.authenticate(http.HandlerFunc(s.appReleases)))
	s.mux.Handle("GET /api/apps/{appID}/configuration", s.authenticate(http.HandlerFunc(s.appConfiguration)))
	s.mux.Handle("GET /api/apps/{appID}/versions", s.authenticate(http.HandlerFunc(s.appVersions)))
	s.mux.Handle("GET /api/apps/{appID}/secrets", s.authenticate(http.HandlerFunc(s.listAppSecrets)))
	s.mux.Handle("PUT /api/apps/{appID}/secrets/{key}", s.authenticate(http.HandlerFunc(s.putAppSecret)))
	s.mux.Handle("GET /api/apps/{appID}/route", s.authenticate(http.HandlerFunc(s.appRoute)))
	s.mux.Handle("POST /api/apps/{appID}/access", s.authenticate(http.HandlerFunc(s.createAppAccess)))
	s.mux.Handle("GET /api/apps/{appID}/logs", s.authenticate(http.HandlerFunc(s.getAppLogs)))
	s.mux.Handle("POST /api/apps/{appID}/logs/refresh", s.authenticate(http.HandlerFunc(s.requestAppLogRefresh)))
	s.mux.Handle("POST /api/apps/{appID}/stop", s.authenticate(http.HandlerFunc(s.stopApp)))
	s.mux.Handle("POST /api/apps/{appID}/start", s.authenticate(http.HandlerFunc(s.startApp)))
	s.mux.Handle("GET /api/apps/{appID}/backups", s.authenticate(http.HandlerFunc(s.listAppBackups)))
	s.mux.Handle("POST /api/apps/{appID}/backups", s.authenticate(http.HandlerFunc(s.createAppBackup)))
	s.mux.Handle("DELETE /api/apps/{appID}/backups/{backupID}", s.authenticate(http.HandlerFunc(s.deleteAppBackup)))
	s.mux.Handle("POST /api/apps/{appID}/backups/{backupID}/restore", s.authenticate(http.HandlerFunc(s.restoreAppBackup)))
	s.mux.Handle("GET /api/apps/{appID}/backups/{backupID}/export", s.authenticate(http.HandlerFunc(s.exportAppBackup)))
	s.mux.Handle("POST /api/apps/{appID}/backups/import", s.authenticate(http.HandlerFunc(s.importAppBackup)))
	s.mux.Handle("GET /api/billing/summary", s.authenticate(http.HandlerFunc(s.billingSummary)))
	s.mux.Handle("GET /api/billing/ledger", s.authenticate(http.HandlerFunc(s.billingLedger)))
	s.mux.Handle("GET /api/billing/usage", s.authenticate(http.HandlerFunc(s.billingUsage)))
	s.mux.Handle("GET /api/billing/bills", s.authenticate(http.HandlerFunc(s.listBillingStatements)))
	s.mux.Handle("GET /api/billing/daily-bills", s.authenticate(http.HandlerFunc(s.listDailyBills)))
	s.mux.Handle("GET /api/billing/bills/{billID}", s.authenticate(http.HandlerFunc(s.billingStatementDetail)))
	s.mux.Handle("GET /api/billing/bills/{billID}/export", s.authenticate(http.HandlerFunc(s.exportBillingStatement)))
	s.mux.Handle("GET /api/billing/credits", s.authenticate(http.HandlerFunc(s.listCredits)))
	s.mux.Handle("GET /api/checkin", s.authenticate(http.HandlerFunc(s.checkinSummary)))
	s.mux.Handle("POST /api/checkin", s.authenticate(http.HandlerFunc(s.performCheckin)))
	s.mux.Handle("GET /api/subscriptions/plans", s.authenticate(http.HandlerFunc(s.subscriptionPlans)))
	s.mux.Handle("POST /api/subscriptions/purchases", s.authenticate(http.HandlerFunc(s.purchaseSubscription)))
	s.mux.Handle("GET /api/payments/orders", s.authenticate(http.HandlerFunc(s.listPaymentOrders)))
	s.mux.Handle("GET /api/payments/providers", s.authenticate(http.HandlerFunc(s.listPaymentProviders)))
	s.mux.Handle("POST /api/payments/orders", s.authenticate(http.HandlerFunc(s.createPaymentOrder)))
	s.mux.HandleFunc("POST /api/payments/epay/callback", s.epayCallback)
	s.mux.HandleFunc("GET /api/payments/epay/callback", s.epayCallback)
	s.mux.HandleFunc("POST /api/internal/egress/{appID}", s.ingestEgressSample)
	s.mux.HandleFunc("GET /api/internal/console-entry", s.consoleEntryGate)
	s.mux.HandleFunc("GET /api/internal/caddy/allow-domain", s.allowCaddyDomain)
	s.mux.Handle("POST /api/admin/payments/orders/{orderID}/mark-paid", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.markPaymentPaid))))
	s.mux.Handle("POST /api/admin/payments/orders/{orderID}/refund", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.refundPayment))))
	s.mux.Handle("GET /api/admin/payments/orders", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.adminPaymentOrders))))
	s.mux.Handle("GET /api/admin/payments/refunds", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.adminRefunds))))
	s.mux.Handle("POST /api/admin/payments/orders/{orderID}/query", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.queryPayment))))
	s.mux.Handle("POST /api/admin/payments/orders/{orderID}/close", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.closePayment))))
	s.mux.Handle("GET /api/admin/settings/payments", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getPaymentSettings))))
	s.mux.Handle("GET /api/admin/settings/checkin", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getCheckinSettings))))
	// Docker runtime settings are operational controls for administrators.
	// Keep the permission check on the API (rather than relying on the menu)
	// so an administrator can configure image sources, registries and proxies
	// while financial/platform-owner settings remain super-admin only.
	s.mux.Handle("GET /api/admin/settings/docker", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.getDockerSettings))))
	s.mux.Handle("PUT /api/admin/settings/docker", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.updateDockerSettings))))
	s.mux.Handle("GET /api/admin/docker/images", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.listDockerImages))))
	s.mux.Handle("DELETE /api/admin/docker/images/{imageID}", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.deleteDockerImage))))
	s.mux.Handle("GET /api/admin/settings/system", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.getSystemSettings))))
	s.mux.Handle("PUT /api/admin/settings/system", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateSystemSettings))))
	s.mux.Handle("GET /api/admin/settings/llm-api-key", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getLLMAPIKey))))
	s.mux.Handle("POST /api/admin/settings/llm-api-key/rotate", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.rotateLLMAPIKey))))
	s.mux.Handle("DELETE /api/admin/settings/llm-api-key", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.revokeLLMAPIKey))))
	s.mux.Handle("GET /api/llm/v1/analysis", s.authenticateLLM(http.HandlerFunc(s.llmSystemAnalysis)))
	s.mux.Handle("GET /api/llm/v1/settings/system", s.authenticateLLM(http.HandlerFunc(s.llmGetSystemSettings)))
	s.mux.Handle("PATCH /api/llm/v1/settings/system", s.authenticateLLM(http.HandlerFunc(s.llmPatchSystemSettings)))
	s.mux.Handle("GET /api/llm/v1/users", s.authenticateLLM(http.HandlerFunc(s.llmListUsers)))
	s.mux.Handle("PATCH /api/llm/v1/users/{userID}", s.authenticateLLM(http.HandlerFunc(s.llmPatchUser)))
	s.mux.Handle("GET /api/llm/v1/logs/audit", s.authenticateLLM(http.HandlerFunc(s.llmListAuditLogs)))
	s.mux.Handle("POST /api/llm/v1/logs/runtime/clear", s.authenticateLLM(http.HandlerFunc(s.llmClearRuntimeLogs)))
	s.mux.Handle("GET /api/llm/v1/database/summary", s.authenticateLLM(http.HandlerFunc(s.llmDatabaseSummary)))
	s.mux.Handle("POST /api/llm/v1/database/maintenance", s.authenticateLLM(http.HandlerFunc(s.llmDatabaseMaintenance)))
	// Canonical tool and automation API. The /api/llm/v1 routes above remain as compatibility aliases.
	s.mux.Handle("GET /api/automation/v1/analysis", s.authenticateLLM(http.HandlerFunc(s.llmSystemAnalysis)))
	s.mux.Handle("GET /api/automation/v1/settings/system", s.authenticateLLM(http.HandlerFunc(s.llmGetSystemSettings)))
	s.mux.Handle("PATCH /api/automation/v1/settings/system", s.authenticateLLM(http.HandlerFunc(s.llmPatchSystemSettings)))
	s.mux.Handle("GET /api/automation/v1/users", s.authenticateLLM(http.HandlerFunc(s.llmListUsers)))
	s.mux.Handle("PATCH /api/automation/v1/users/{userID}", s.authenticateLLM(http.HandlerFunc(s.llmPatchUser)))
	s.mux.Handle("GET /api/automation/v1/logs/audit", s.authenticateLLM(http.HandlerFunc(s.llmListAuditLogs)))
	s.mux.Handle("POST /api/automation/v1/logs/runtime/clear", s.authenticateLLM(http.HandlerFunc(s.llmClearRuntimeLogs)))
	s.mux.Handle("GET /api/automation/v1/database/summary", s.authenticateLLM(http.HandlerFunc(s.llmDatabaseSummary)))
	s.mux.Handle("POST /api/automation/v1/database/maintenance", s.authenticateLLM(http.HandlerFunc(s.llmDatabaseMaintenance)))
	s.mux.Handle("GET /api/admin/caddy/overview", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getCaddyOverview))))
	s.mux.Handle("GET /api/admin/caddy/settings", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getGatewaySettings))))
	s.mux.Handle("PUT /api/admin/caddy/settings", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateGatewaySettings))))
	s.mux.Handle("GET /api/admin/caddy/routes", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.listGatewayRoutes))))
	s.mux.Handle("POST /api/admin/caddy/certificates/import", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.importGatewayCertificate))))
	s.mux.Handle("POST /api/admin/caddy/validate", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.validateCaddyConfig))))
	s.mux.Handle("POST /api/admin/caddy/reload", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.reloadCaddyConfig))))
	s.mux.Handle("PUT /api/admin/settings/checkin", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateCheckinSettings))))
	s.mux.Handle("PUT /api/admin/settings/payments/{provider}", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updatePaymentSettings))))
	s.mux.Handle("GET /api/admin/pricing", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.adminPricing))))
	s.mux.Handle("GET /api/admin/plans", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.adminPlans))))
	s.mux.Handle("POST /api/admin/plans", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.createPlan))))
	s.mux.Handle("PATCH /api/admin/plans/{planID}/availability", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updatePlanAvailability))))
	s.mux.Handle("POST /api/admin/plans/{planID}/versions", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.createPlanVersion))))
	s.mux.Handle("PUT /api/admin/users/{userID}/subscription", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.assignSubscription))))
	s.mux.Handle("POST /api/admin/pricing/items", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.createPricingItem))))
	s.mux.Handle("DELETE /api/admin/pricing/items/{itemID}", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.deletePricingItem))))
	s.mux.Handle("POST /api/admin/pricing/items/{itemID}/versions", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.createPricingVersion))))
	s.mux.Handle("POST /api/apps", s.authenticate(http.HandlerFunc(s.createApp)))
	s.mux.Handle("DELETE /api/apps/{appID}", s.authenticate(http.HandlerFunc(s.deleteApp)))
	s.mux.Handle("POST /api/apps/{appID}/releases", s.authenticate(http.HandlerFunc(s.createAppRelease)))
	s.mux.Handle("POST /api/apps/{appID}/rollback", s.authenticate(http.HandlerFunc(s.rollbackApp)))
	s.mux.Handle("POST /api/admin/products", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.createProduct))))
	s.mux.Handle("GET /api/admin/products", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.adminListProducts))))
	s.mux.Handle("PATCH /api/admin/products/{productID}", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.updateProduct))))
	s.mux.Handle("PATCH /api/admin/products/{productID}/availability", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.updateProductAvailability))))
	s.mux.Handle("DELETE /api/admin/products/{productID}", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.deleteProduct))))
	s.mux.Handle("POST /api/admin/products/{productID}/versions", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.createProductVersion))))
	s.mux.Handle("DELETE /api/admin/products/{productID}/versions/{versionID}", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.deleteProductVersion))))
	s.mux.Handle("POST /api/admin/products/{productID}/versions/{versionID}/tests", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.startProductVersionTest))))
	s.mux.Handle("POST /api/admin/products/{productID}/versions/{versionID}/tests/{testID}/cancel", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.cancelProductVersionTest))))
	s.mux.Handle("POST /api/admin/products/{productID}/versions/{versionID}/publish", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.publishProductVersion))))
	s.mux.Handle("GET /api/admin/users", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.listUsers))))
	s.mux.Handle("POST /api/admin/users/{userID}/impersonation", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.startImpersonation))))
	s.mux.Handle("POST /api/admin/users", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.adminCreateUser))))
	s.mux.Handle("PATCH /api/admin/users/{userID}/status", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateUserStatus))))
	s.mux.Handle("PATCH /api/admin/users/{userID}/role", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.updateUserRole))))
	s.mux.Handle("POST /api/admin/users/{userID}/wallet/adjust", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.adjustWallet))))
	s.mux.Handle("POST /api/admin/users/{userID}/credits", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.grantCredit))))
	s.mux.Handle("GET /api/admin/settings/mail", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getMailSettings))))
	s.mux.Handle("PUT /api/admin/settings/mail", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateMailSettings))))
	s.mux.Handle("POST /api/admin/settings/mail/test", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.testMailSettings))))
	s.mux.Handle("GET /api/admin/announcements", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.adminListAnnouncements))))
	s.mux.Handle("POST /api/admin/announcements", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.createAnnouncement))))
	s.mux.Handle("PATCH /api/admin/announcements/{announcementID}", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.updateAnnouncement))))
	s.mux.Handle("GET /api/admin/faqs", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.adminListFAQs))))
	s.mux.Handle("POST /api/admin/faqs", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.createFAQ))))
	s.mux.Handle("PUT /api/admin/faqs/{faqID}", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.updateFAQ))))
	s.mux.Handle("DELETE /api/admin/faqs/{faqID}", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.deleteFAQ))))
	s.mux.Handle("PUT /api/admin/settings/sidebar-visibility", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateSidebarVisibility))))
	s.mux.Handle("GET /api/admin/settings/auth", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getAuthPolicy))))
	s.mux.Handle("PUT /api/admin/settings/auth", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateAuthPolicy))))
	s.mux.Handle("GET /api/admin/settings/turnstile", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getTurnstileSettings))))
	s.mux.Handle("GET /api/admin/settings/quota", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getQuotaSettings))))
	s.mux.Handle("PUT /api/admin/settings/quota", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateQuotaSettings))))
	s.mux.Handle("GET /api/admin/settings/logs", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getLogRetentionSettings))))
	s.mux.Handle("PUT /api/admin/settings/logs", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateLogRetentionSettings))))
	s.mux.Handle("GET /api/admin/settings/ai-support", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getAISupportSettings))))
	s.mux.Handle("POST /api/admin/settings/ai-support/test", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.testAISupportSettings))))
	s.mux.Handle("PUT /api/admin/settings/ai-support", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateAISupportSettings))))
	s.mux.Handle("POST /api/admin/logs/clear", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.clearRuntimeLogs))))
	s.mux.Handle("POST /api/admin/audit-logs/clear", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.clearAuditLogs))))
	s.mux.Handle("PUT /api/admin/settings/turnstile", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateTurnstileSettings))))
	s.mux.Handle("GET /api/admin/settings/oauth", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.getOAuthSettings))))
	s.mux.Handle("PUT /api/admin/settings/oauth/{provider}", s.authenticate(s.requireRoles("super_admin")(http.HandlerFunc(s.updateOAuthSettings))))
	s.mux.Handle("GET /api/admin/tickets", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.adminListTickets))))
	s.mux.Handle("GET /api/admin/tickets/{ticketID}", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.adminGetTicket))))
	s.mux.Handle("POST /api/admin/tickets/{ticketID}/messages", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.adminReplyTicket))))
	s.mux.Handle("PATCH /api/admin/tickets/{ticketID}/status", s.authenticate(s.requireRoles("admin", "super_admin")(http.HandlerFunc(s.adminUpdateTicketStatus))))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	var initialized *time.Time
	var platformName string
	var superAdmins int
	err := s.db.QueryRow(r.Context(), "SELECT initialized_at, platform_name, (SELECT count(*) FROM user_roles ur JOIN roles r ON r.id=ur.role_id JOIN users u ON u.id=ur.user_id WHERE r.code='super_admin' AND u.status='active') FROM system_state WHERE singleton").Scan(&initialized, &platformName, &superAdmins)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_not_ready", "database migrations are not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"initialized":  initialized != nil && superAdmins > 0,
		"hasAdmin":     superAdmins > 0,
		"platformName": platformName,
		"checks":       map[string]bool{"database": true, "migrations": true},
	})
}

type initializeRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
	SystemName  string `json:"systemName"`
}

func (s *Server) initialize(w http.ResponseWriter, r *http.Request) {
	var req initializeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.SystemName = strings.TrimSpace(req.SystemName)
	if req.DisplayName == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "display name and password are required")
		return
	}
	if req.SystemName == "" {
		req.SystemName = "CloudMeter"
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "email is invalid")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		s.internalError(w, err)
		return
	}

	// The advisory lock serializes setup attempts. Read Committed ensures a
	// waiter sees the winner's committed initialized_at after acquiring it.
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock($1)", int64(729104501)); err != nil {
		s.internalError(w, err)
		return
	}
	var initialized *time.Time
	if err = tx.QueryRow(r.Context(), "SELECT initialized_at FROM system_state WHERE singleton FOR UPDATE").Scan(&initialized); err != nil {
		s.internalError(w, err)
		return
	}
	if initialized != nil {
		writeError(w, http.StatusConflict, "already_initialized", "setup has already completed")
		return
	}
	slug := "admin"
	var userID string
	err = tx.QueryRow(r.Context(), `INSERT INTO users (email,password_hash,display_name,slug) VALUES ($1,$2,$3,$4) RETURNING id`,
		req.Email, string(hash), req.DisplayName, slug).Scan(&userID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='super_admin'`, userID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO wallets(user_id) VALUES ($1)`, userID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO plans(code,name) VALUES ('free','Free')`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO plan_versions(plan_id,version,cycle_price_cents,entitlements,effective_at)
		SELECT id,1,0,jsonb_build_object(
			'apps',1,'cpuCores',1,'memoryGiB',1,'systemDiskGiB',5,'dataDiskGiB',0,
			'backupStorageGiB',0,'backupOperationsPerMonth',0,'concurrentDeployments',1,
			'publicIngresses',1,'ingressOverageEnabled',false,
			'egressGiB',1,'egressOverageEnabled',false,'creditGrantCents',0,'allowedProductIds','[]'::jsonb
		),now() FROM plans WHERE code='free'`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO user_subscriptions(user_id,plan_version_id,entitlements_snapshot,cycle_price_cents_snapshot) SELECT $1,pv.id,pv.entitlements,pv.cycle_price_cents FROM plans p JOIN plan_versions pv ON pv.plan_id=p.id WHERE p.code='free' ORDER BY pv.version DESC LIMIT 1`, userID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE system_state SET initialized_at=now(), initialized_by=$1, installation_id=gen_random_uuid(), platform_name=$2, registration_enabled=false, email_verification_required=false WHERE singleton`, userID, req.SystemName)
	if err != nil {
		s.internalError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO system_settings(singleton, system_name, updated_at, updated_by) VALUES (true, $1, now(), $2) ON CONFLICT (singleton) DO UPDATE SET system_name=$1, updated_at=now(), updated_by=$2", req.SystemName, userID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id) VALUES($1,$1,'system.initialize','system_state','singleton',$2)`, userID, requestID(r.Context()))
	if err != nil {
		s.internalError(w, err)
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		s.internalError(w, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(time.Duration(s.cfg.SessionTTLHours) * time.Hour)
	if _, err = tx.Exec(r.Context(), "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,$2,$3)", userID, tokenHash[:], expires); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"initialized": true, "token": token, "expiresAt": expires})
}

type loginRequest struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	TurnstileToken string `json:"turnstileToken"`
}

type registerRequest struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	DisplayName    string `json:"displayName"`
	Code           string `json:"code"`
	TurnstileToken string `json:"turnstileToken"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.verifyTurnstile(r, req.TurnstileToken, "registration"); err != nil {
		writeError(w, http.StatusForbidden, "turnstile_verification_failed", err.Error())
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if _, err := mail.ParseAddress(req.Email); err != nil || req.DisplayName == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "valid email, display name and a password are required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var enabled, initialized, verificationRequired, blockAliases, passwordRegistrationEnabled bool
	var whitelist []string
	if err = tx.QueryRow(r.Context(), "SELECT registration_enabled, initialized_at IS NOT NULL, email_verification_required, email_domain_whitelist, block_email_aliases, password_registration_enabled FROM system_state WHERE singleton FOR UPDATE").Scan(&enabled, &initialized, &verificationRequired, &whitelist, &blockAliases, &passwordRegistrationEnabled); err != nil {
		s.internalError(w, err)
		return
	}
	if !initialized {
		writeError(w, http.StatusConflict, "not_initialized", "platform setup is not complete")
		return
	}
	if !enabled {
		writeError(w, http.StatusForbidden, "registration_disabled", "registration is disabled by the administrator")
		return
	}
	if !passwordRegistrationEnabled {
		writeError(w, http.StatusForbidden, "password_registration_disabled", "password registration has been disabled by the administrator")
		return
	}
	if normalized, policyError := validatePolicyEmail(req.Email, whitelist, blockAliases); policyError != "" {
		writeError(w, http.StatusForbidden, "email_policy_blocked", policyError)
		return
	} else {
		req.Email = normalized
	}
	if verificationRequired {
		codeHash := sha256.Sum256([]byte(strings.TrimSpace(req.Code)))
		var verificationID int64
		var storedHash []byte
		var attemptCount int
		err = tx.QueryRow(r.Context(), "SELECT id,code_hash,attempt_count FROM email_verification_codes WHERE lower(email)=lower($1) AND purpose='register' AND consumed_at IS NULL AND expires_at>now() ORDER BY created_at DESC LIMIT 1 FOR UPDATE", req.Email).Scan(&verificationID, &storedHash, &attemptCount)
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusBadRequest, "invalid_verification_code", "verification code is invalid or expired")
			return
		} else if err != nil {
			s.internalError(w, err)
			return
		}
		if len(storedHash) != len(codeHash) || subtle.ConstantTimeCompare(storedHash, codeHash[:]) != 1 {
			attemptCount++
			if _, err = tx.Exec(r.Context(), "UPDATE email_verification_codes SET attempt_count=$2::smallint,consumed_at=CASE WHEN $2::smallint>=5 THEN now() ELSE consumed_at END WHERE id=$1", verificationID, attemptCount); err != nil {
				s.internalError(w, err)
				return
			}
			if err = tx.Commit(r.Context()); err != nil {
				s.internalError(w, err)
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_verification_code", "verification code is invalid or expired")
			return
		}
		if _, err = tx.Exec(r.Context(), `UPDATE email_verification_codes SET consumed_at=now() WHERE id=$1`, verificationID); err != nil {
			s.internalError(w, err)
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "password must be at most 72 bytes")
		return
	}
	slug, err := allocateUserSlug(r.Context(), tx, req.Email)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var userID string
	err = tx.QueryRow(r.Context(), `INSERT INTO users(email,password_hash,display_name,slug,email_verified_at) VALUES($1,$2,$3,$4,CASE WHEN $5 THEN now() ELSE NULL END) RETURNING id`, req.Email, string(hash), req.DisplayName, slug, verificationRequired).Scan(&userID)
	if err != nil {
		if strings.Contains(err.Error(), "users_email_unique") {
			writeError(w, http.StatusConflict, "email_exists", "an account with this email already exists")
			return
		}
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='user'`, userID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO wallets(user_id) VALUES($1)`, userID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id) VALUES($1,$1,'user.register','user',$2::text,$3)`, userID, userID, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"registered": true})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if err := s.verifyTurnstile(r, req.TurnstileToken, "login"); err != nil {
		writeError(w, http.StatusForbidden, "turnstile_verification_failed", err.Error())
		return
	}
	var id, email, displayName, passwordHash, status string
	err := s.db.QueryRow(r.Context(), `SELECT id,email,display_name,password_hash,status FROM users WHERE lower(email)=lower($1)`, strings.TrimSpace(req.Email)).Scan(&id, &email, &displayName, &passwordHash, &status)
	if err != nil || status != "active" || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		return
	}
	var passwordLoginEnabled bool
	if err = s.db.QueryRow(r.Context(), "SELECT password_login_enabled FROM system_state WHERE singleton").Scan(&passwordLoginEnabled); err != nil {
		s.internalError(w, err)
		return
	}
	if !passwordLoginEnabled {
		writeError(w, http.StatusForbidden, "password_login_disabled", "password login has been disabled by the administrator")
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		s.internalError(w, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(time.Duration(s.cfg.SessionTTLHours) * time.Hour)
	_, err = s.db.Exec(r.Context(), `INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,$2,$3)`, id, tokenHash[:], expires)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expiresAt": expires, "user": map[string]string{"id": id, "email": email, "displayName": displayName}})
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(auth, "Bearer ") {
			writeError(w, 401, "unauthenticated", "authentication required")
			return
		}
		hash := sha256.Sum256([]byte(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))))
		var p principal
		var roles []string
		var actorID, actorEmail, actorName *string
		var impersonationWrite *bool
		err := s.db.QueryRow(r.Context(), `SELECT session.id,u.id,u.email,u.display_name,coalesce(array_agg(role.code) FILTER (WHERE role.code IS NOT NULL),'{}'),
		i.actor_user_id,actor.email,actor.display_name,i.write_enabled
		FROM sessions session JOIN users u ON u.id=session.user_id
		LEFT JOIN user_roles ur ON ur.user_id=u.id LEFT JOIN roles role ON role.id=ur.role_id
		LEFT JOIN impersonation_sessions i ON i.session_id=session.id LEFT JOIN users actor ON actor.id=i.actor_user_id
		WHERE session.token_hash=$1 AND session.revoked_at IS NULL AND session.expires_at>now() AND u.status='active'
		AND (i.session_id IS NULL OR actor.status='active')
		GROUP BY session.id,u.id,i.actor_user_id,actor.email,actor.display_name,i.write_enabled`, hash[:]).Scan(&p.SessionID, &p.ID, &p.Email, &p.DisplayName, &roles, &actorID, &actorEmail, &actorName, &impersonationWrite)
		if err != nil {
			writeError(w, 401, "unauthenticated", "session is invalid or expired")
			return
		}
		p.Roles = roles
		if actorID != nil {
			p.ActorID, p.ActorEmail, p.ActorDisplayName = *actorID, *actorEmail, *actorName
			p.Impersonating = true
			p.ImpersonationReadOnly = impersonationWrite == nil || !*impersonationWrite
		}
		isSessionTermination := isSessionTerminationRequest(r)
		if p.Impersonating && p.ImpersonationReadOnly && !isSessionTermination && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			writeError(w, http.StatusForbidden, "impersonation_read_only", "this impersonation session is read-only")
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, p)
		next.ServeHTTP(w, r.WithContext(ctx))
		if p.Impersonating && !p.ImpersonationReadOnly && !isSessionTermination && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			_, _ = s.db.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
				VALUES($1,$2,'impersonation.write','http_request',$3,$4,jsonb_build_object('method',$5::text))`, p.ActorID, p.ID, r.URL.Path, requestID(r.Context()), r.Method)
		}
	})
}

func (s *Server) requireRoles(allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _ := r.Context().Value(principalKey).(principal)
			for _, actual := range p.Roles {
				for _, expected := range allowed {
					if actual == expected {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
			writeError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
		})
	}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, r.Context().Value(principalKey))
}

func (s *Server) adminSummary(w http.ResponseWriter, r *http.Request) {
	var users, products, deployments int64
	if err := s.db.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM app_products),(SELECT count(*) FROM deployment_jobs WHERE state NOT IN ('succeeded','failed'))`).Scan(&users, &products, &deployments); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]int64{"users": users, "products": products, "activeDeployments": deployments})
}

type createProductRequest struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	IconURL string `json:"iconUrl"`
}
type createVersionRequest struct {
	VersionLabel string         `json:"versionLabel"`
	ImageDigest  string         `json:"imageDigest"`
	RuntimeSpec  map[string]any `json:"runtimeSpec"`
	RouteSpec    map[string]any `json:"routeSpec"`
	HealthSpec   map[string]any `json:"healthSpec"`
	UpdateSpec   map[string]any `json:"updateSpec"`
}

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	rows, err := s.db.Query(r.Context(), `SELECT p.id,p.slug,p.name,p.icon_url,pv.id,pv.version,coalesce(pv.version_label,''),pv.image_digest,pv.runtime_spec,pv.route_spec,pv.health_spec,pv.update_spec
		FROM app_products p LEFT JOIN app_product_versions pv ON pv.product_id=p.id AND pv.published_at IS NOT NULL AND pv.deleted_at IS NULL
		WHERE p.status='published' AND p.deleted_at IS NULL ORDER BY p.name,pv.version DESC`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type versionItem struct {
		VersionID           string         `json:"versionId"`
		Version             int            `json:"version"`
		VersionLabel        string         `json:"versionLabel"`
		ImageDigest         string         `json:"imageDigest"`
		RuntimeSpec         map[string]any `json:"runtimeSpec"`
		RouteSpec           map[string]any `json:"routeSpec"`
		HealthSpec          map[string]any `json:"healthSpec"`
		UpdateSpec          map[string]any `json:"updateSpec"`
		Deployable          bool           `json:"deployable"`
		MissingDependencies []string       `json:"missingDependencies"`
	}
	type productItem struct {
		ID       string        `json:"id"`
		Slug     string        `json:"slug"`
		Name     string        `json:"name"`
		IconURL  string        `json:"iconUrl"`
		Versions []versionItem `json:"versions"`
	}
	order := make([]string, 0)
	groups := make(map[string]*productItem)
	for rows.Next() {
		var id, slug, name, icon string
		var versionID *string
		var version int
		var versionLabel string
		var imageDigest string
		var runtimeSpec, routeSpec, healthSpec, updateSpec map[string]any
		if err := rows.Scan(&id, &slug, &name, &icon, &versionID, &version, &versionLabel, &imageDigest, &runtimeSpec, &routeSpec, &healthSpec, &updateSpec); err != nil {
			s.internalError(w, err)
			return
		}
		product, ok := groups[id]
		if !ok {
			product = &productItem{ID: id, Slug: slug, Name: name, IconURL: icon, Versions: []versionItem{}}
			groups[id] = product
			order = append(order, id)
		}
		if versionID == nil {
			continue
		}
		v := versionItem{VersionID: *versionID, Version: version, VersionLabel: versionLabel, ImageDigest: imageDigest, RuntimeSpec: runtimeSpec, RouteSpec: routeSpec, HealthSpec: healthSpec, UpdateSpec: updateSpec, Deployable: true}
		missing, dependencyErr := missingRuntimeDependencies(r.Context(), s.db, p.ID, "", runtimeSpec)
		if dependencyErr != nil {
			s.internalError(w, dependencyErr)
			return
		}
		v.MissingDependencies = dependencyLabels(missing)
		v.Deployable = len(missing) == 0
		product.Versions = append(product.Versions, v)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	items := make([]productItem, 0, len(order))
	for _, id := range order {
		items = append(items, *groups[id])
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": items})
}

type createAppRequest struct {
	ProductID         string                   `json:"productId"`
	VersionID         string                   `json:"versionId"`
	Slug              string                   `json:"slug"`
	DomainRefreshDays *int                     `json:"domainRefreshDays"`
	IdempotencyKey    string                   `json:"idempotencyKey"`
	Secrets           map[string]string        `json:"secrets"`
	Resources         selectedResources        `json:"resources"`
	Access            appAccessSettingsRequest `json:"access"`
}

type selectedResources struct {
	CPUCores           *float64             `json:"cpuCores"`
	MemoryMiB          *float64             `json:"memoryMiB"`
	DataVolumeGiB      *float64             `json:"dataVolumeGiB"`
	VolumeSizes        map[string]float64   `json:"volumeSizes"`
	Command            []string             `json:"command"`
	Environment        map[string]string    `json:"environment"`
	Dependencies       []selectedDependency `json:"dependencies"`
	PortMappingEnabled *bool                `json:"portMappingEnabled"`
	ContainerPort      *int                 `json:"containerPort"`
}

func editableRuntimeOption(spec map[string]any, key string, legacyDefault bool) bool {
	options, ok := spec["editableOptions"].(map[string]any)
	if !ok {
		return legacyDefault
	}
	value, exists := options[key]
	if !exists {
		return false
	}
	enabled, _ := value.(bool)
	return enabled
}

type selectedDependency struct {
	Key         string `json:"key"`
	ProductID   string `json:"productId"`
	ServiceSlug string `json:"serviceSlug"`
	Required    bool   `json:"required"`
}

func selectedRuntimeSpec(template map[string]any, selected selectedResources) (map[string]any, error) {
	encoded, err := json.Marshal(template)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err = json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	minimumCompute, err := runtimepolicy.RuntimeResources(result, true)
	if err != nil {
		return nil, err
	}
	cpu, memory := minimumCompute.CPUCores, minimumCompute.MemoryMiB
	if selected.CPUCores != nil && !editableRuntimeOption(result, "cpu", true) {
		return nil, fmt.Errorf("CPU is not editable for this product")
	}
	if selected.CPUCores != nil {
		cpu = *selected.CPUCores
	}
	if selected.MemoryMiB != nil && !editableRuntimeOption(result, "memory", true) {
		return nil, fmt.Errorf("memory is not editable for this product")
	}
	if selected.MemoryMiB != nil {
		memory = *selected.MemoryMiB
	}
	if cpu < minimumCompute.CPUCores || memory < minimumCompute.MemoryMiB {
		return nil, fmt.Errorf("selected CPU and memory must meet the product minimum")
	}
	result["cpuCores"], result["memoryMiB"], result["systemDiskGiB"] = cpu, memory, runtimepolicy.DefaultSystemDiskGiB
	minimumVolume, _ := runtimepolicy.RuntimeDataVolumeGiB(result, false)
	chosenVolume := minimumVolume
	// Editable-data-volume semantics must match the DB parentage guard, which
	// only treats a volume as overridable when editableOptions.dataVolume is
	// explicitly true. Without an explicit declaration the release snapshot
	// must stay byte-for-byte equal to the immutable product version.
	dataVolumeEditable := editableRuntimeOption(result, "dataVolume", false)
	if selected.DataVolumeGiB != nil {
		if !dataVolumeEditable {
			return nil, fmt.Errorf("data volume capacity is not editable for this product")
		}
		chosenVolume = *selected.DataVolumeGiB
	}
	// Accept the old per-volume request shape during rolling upgrades, but fold
	// it into the one shared application volume capacity.
	if len(selected.VolumeSizes) > 0 && !dataVolumeEditable {
		return nil, fmt.Errorf("data volume capacity is not editable for this product")
	}
	for _, chosen := range selected.VolumeSizes {
		if chosen > chosenVolume {
			chosenVolume = chosen
		}
	}
	if chosenVolume < minimumVolume {
		return nil, fmt.Errorf("data volume capacity must be at least %.0f GiB", minimumVolume)
	}
	if dataVolumeEditable {
		if volumes, ok := result["volumes"].([]any); ok && len(volumes) > 0 {
			for _, raw := range volumes {
				volume, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				volume["sizeGiB"] = chosenVolume
			}
			result["dataVolumeGiB"] = chosenVolume
		} else {
			// A zero-capacity data volume is not a real resource and must not
			// be added to the snapshot, otherwise the parentage guard rejects
			// deployment.
			delete(result, "dataVolumeGiB")
		}
	}
	if selected.Command != nil {
		if !editableRuntimeOption(result, "command", false) {
			return nil, fmt.Errorf("command is not editable for this product")
		}
		result["command"] = selected.Command
	}
	if selected.Environment != nil {
		if !editableRuntimeOption(result, "environment", false) {
			return nil, fmt.Errorf("environment variables are not editable for this product")
		}
		environment, _ := result["env"].(map[string]any)
		if environment == nil {
			environment = map[string]any{}
		}
		allowed := map[string]bool{}
		if rawKeys, ok := result["editableEnvKeys"].([]any); ok {
			for _, rawKey := range rawKeys {
				if key, ok := rawKey.(string); ok {
					allowed[key] = true
				}
			}
		}
		for key, value := range selected.Environment {
			if !allowed[key] {
				return nil, fmt.Errorf("environment variable %s is not editable for this product", key)
			}
			environment[key] = value
		}
		result["env"] = environment
	}
	if selected.Dependencies != nil {
		if !editableRuntimeOption(result, "dependencies", false) {
			return nil, fmt.Errorf("dependencies are not editable for this product")
		}
		dependencies := make([]any, 0, len(selected.Dependencies))
		for _, dependency := range selected.Dependencies {
			dependencies = append(dependencies, map[string]any{"key": dependency.Key, "productId": dependency.ProductID, "serviceSlug": dependency.ServiceSlug, "required": dependency.Required})
		}
		result["dependencies"] = dependencies
	}
	if err = runtimepolicy.ValidateRuntimeSpec(result); err != nil {
		return nil, err
	}
	return result, nil
}

func selectedRouteSpec(template map[string]any, selected selectedResources) (map[string]any, error) {
	encoded, err := json.Marshal(template)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err = json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	// The internal listener is part of the published product contract.
	// User resource overrides may not change the port used by Gateway/health checks.
	if err = normalizeRouteSpec(result); err != nil {
		return nil, err
	}
	return result, nil
}

func validateInternalRoutePort(runtimeSpec, routeSpec map[string]any) error {
	port, ok := exactInteger(routeSpec["containerPort"])
	if !ok || port < 1 || port > 65535 {
		return fmt.Errorf("container port must be between 1 and 65535")
	}
	return runtimepolicy.ValidateRuntimeSpec(runtimeSpec)
}

var appSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var invalidUserSlugChars = regexp.MustCompile(`[^a-z0-9-]+`)
var repeatedHyphens = regexp.MustCompile(`-+`)

func userSlugBase(email string) string {
	local := strings.SplitN(strings.ToLower(strings.TrimSpace(email)), "@", 2)[0]
	local = repeatedHyphens.ReplaceAllString(invalidUserSlugChars.ReplaceAllString(local, "-"), "-")
	local = strings.Trim(local, "-")
	if local == "" {
		local = "user"
	}
	if len(local) > 56 {
		local = strings.TrimRight(local[:56], "-")
	}
	return local
}

func allocateUserSlug(ctx context.Context, tx pgx.Tx, email string) (string, error) {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(729104502)); err != nil {
		return "", err
	}
	base := userSlugBase(email)
	for suffix := 0; suffix < 10000; suffix++ {
		candidate := base
		if suffix > 0 {
			candidate = fmt.Sprintf("%s-%d", base, suffix+1)
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE lower(slug)=lower($1))", candidate).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate user slug")
}

func (s *Server) estimatedMonthlyAppCents(ctx context.Context, userID, appID string, runtimeSpec map[string]any, hasIngress bool) (int64, bool) {
	resources, err := runtimepolicy.RuntimeTotalResources(runtimeSpec)
	if err != nil {
		return 0, false
	}
	storage, err := runtimepolicy.RuntimeStorage(runtimeSpec, false)
	if err != nil {
		return 0, false
	}
	quantities := []struct {
		code, unit string
		quantity   float64
	}{
		{"app.runtime.minutes", "minute", 730 * 60},
		{"cpu.core_hours", "core_hour", resources.CPUCores * 730},
		{"memory.gib_hours", "GiB_hour", resources.MemoryMiB / 1024 * 730},
		{"storage.data.gib_days", "GiB_day", storage.DataDiskGiB * 30},
	}
	if hasIngress {
		quantities = append(quantities, struct {
			code, unit string
			quantity   float64
		}{"network.public_ingress", "ingress", 1})
	}
	total, complete := float64(0), true
	for _, item := range quantities {
		if item.quantity <= 0 {
			continue
		}
		var price int64
		err := s.db.QueryRow(ctx, `SELECT pv.unit_price_micros FROM pricing_versions pv WHERE pv.id=resolve_pricing_version($1,$2,$3,$4,now())`, userID, appID, item.code, item.unit).Scan(&price)
		if err != nil {
			complete = false
			continue
		}
		total += item.quantity * float64(price) / 1000000
	}
	return int64(math.Round(total)), complete
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, _ := r.Context().Value(principalKey).(principal)
	rows, err := s.db.Query(r.Context(), "SELECT a.id,a.instance_id,a.slug,a.service_slug,a.status,coalesce(a.suspension_reason,''),p.slug,coalesce(a.last_successful_release_id::text,''),coalesce(prev.id::text,''),coalesce(j.id::text,''),coalesce(j.state::text,''),coalesce(j.last_error,''),coalesce(j.updated_at,a.created_at),coalesce(ar.public_path,''),coalesce(ar.host_port,0),a.port_mapping_enabled,a.route_host_label,a.domain_refresh_days,a.domain_next_refresh_at,coalesce(nullif(current_release.immutable_snapshot->'route_spec'->>'port','')::int,nullif(current_release.immutable_snapshot->'route_spec'->>'containerPort','')::int,8080),coalesce(current_release.immutable_snapshot->'runtime_spec','{}'::jsonb) FROM user_apps a JOIN app_products p ON p.id=a.product_id LEFT JOIN app_releases current_release ON current_release.id=a.last_successful_release_id LEFT JOIN app_routes ar ON ar.user_app_id=a.id AND ar.release_id=a.last_successful_release_id LEFT JOIN LATERAL (SELECT id FROM app_releases WHERE user_app_id=a.id AND state IN ('active','superseded') AND release_number < coalesce(current_release.release_number,2147483647) ORDER BY release_number DESC LIMIT 1) prev ON true LEFT JOIN LATERAL (SELECT id,state,last_error,updated_at FROM deployment_jobs WHERE user_app_id=a.id ORDER BY created_at DESC LIMIT 1) j ON true WHERE a.user_id=$1 AND a.deleted_at IS NULL ORDER BY a.created_at DESC", p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type appView struct {
		ID                    string     `json:"id"`
		InstanceID            string     `json:"instanceId"`
		Slug                  string     `json:"slug"`
		ServiceSlug           string     `json:"serviceSlug"`
		Status                string     `json:"status"`
		SuspensionReason      string     `json:"suspensionReason,omitempty"`
		ProductSlug           string     `json:"productSlug"`
		LastReleaseID         string     `json:"lastSuccessfulReleaseId"`
		PreviousReleaseID     string     `json:"previousReleaseId"`
		JobID                 string     `json:"jobId"`
		JobState              string     `json:"jobState"`
		JobLastError          string     `json:"jobLastError,omitempty"`
		UpdatedAt             time.Time  `json:"updatedAt"`
		PublicPath            string     `json:"publicPath,omitempty"`
		HostPort              int        `json:"hostPort"`
		PortMappingEnabled    bool       `json:"portMappingEnabled"`
		RouteHostLabel        string     `json:"routeHostLabel"`
		DomainRefreshDays     *int       `json:"domainRefreshDays"`
		DomainNextRefreshAt   *time.Time `json:"domainNextRefreshAt,omitempty"`
		ContainerPort         int        `json:"containerPort"`
		CPUCores              float64    `json:"cpuCores"`
		MemoryMiB             float64    `json:"memoryMiB"`
		CPUUsageCores         float64    `json:"cpuUsageCores"`
		MemoryUsageMiB        float64    `json:"memoryUsageMiB"`
		MetricsSampledAt      *time.Time `json:"metricsSampledAt,omitempty"`
		EstimatedMonthlyCents int64      `json:"estimatedMonthlyCents"`
		EstimateComplete      bool       `json:"estimateComplete"`
	}
	items := make([]appView, 0)
	for rows.Next() {
		var item appView
		var runtimeSpec map[string]any
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.Slug, &item.ServiceSlug, &item.Status, &item.SuspensionReason, &item.ProductSlug, &item.LastReleaseID, &item.PreviousReleaseID, &item.JobID, &item.JobState, &item.JobLastError, &item.UpdatedAt, &item.PublicPath, &item.HostPort, &item.PortMappingEnabled, &item.RouteHostLabel, &item.DomainRefreshDays, &item.DomainNextRefreshAt, &item.ContainerPort, &runtimeSpec); err != nil {
			s.internalError(w, err)
			return
		}
		if resources, resourceErr := runtimepolicy.RuntimeTotalResources(runtimeSpec); resourceErr == nil {
			item.CPUCores, item.MemoryMiB = resources.CPUCores, resources.MemoryMiB
		}
		if item.Status == "running" {
			_ = s.db.QueryRow(r.Context(), "SELECT coalesce(cpu_usage_cores,0)::float8,coalesce(memory_usage_bytes,0)::float8/1048576,sampled_at FROM app_runtime_metrics WHERE user_app_id=$1 AND sampled_at>=now()-interval '15 seconds'", item.ID).Scan(&item.CPUUsageCores, &item.MemoryUsageMiB, &item.MetricsSampledAt)
		}
		item.EstimatedMonthlyCents, item.EstimateComplete = s.estimatedMonthlyAppCents(r.Context(), p.ID, item.ID, runtimeSpec, item.PublicPath != "")
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"apps": items})
}

// nextAppSlugCandidate produces the candidate for a given attempt index. The
// first attempt keeps the base slug; later attempts append a numeric suffix
// while staying within the slug length limit and never ending on a dash.
func nextAppSlugCandidate(base string, attempt int) string {
	if attempt <= 0 {
		return base
	}
	suffix := fmt.Sprintf("%d", attempt+1)
	core := strings.TrimRight(base, "-")
	maxCore := 63 - len(suffix) - 1
	if len(core) > maxCore {
		core = strings.TrimRight(core[:maxCore], "-")
	}
	return core + "-" + suffix
}

// allocateUserAppSlug returns a slug unique for the user across both the slug
// and service_slug columns. When the requested base slug is already taken it
// appends the next available numeric suffix, so the same user can deploy the
// same product multiple times (e.g. sillytavern, sillytavern-2, sillytavern-3).
func allocateUserAppSlug(ctx context.Context, tx pgx.Tx, userID, base string) (string, error) {
	for attempt := 0; attempt < 512; attempt++ {
		candidate := nextAppSlugCandidate(base, attempt)
		var taken bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM user_apps WHERE user_id=$1 AND (slug=$2 OR service_slug=$2) AND deleted_at IS NULL)", userID, candidate).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no available application slug for %q", base)
}

func allocateRouteHostLabel(ctx context.Context, tx pgx.Tx, appSlug, userSlug string) (string, error) {
	base := strings.Trim(strings.ToLower(appSlug+"-"+userSlug), "-")
	if len(base) <= 63 {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_apps WHERE lower(route_host_label)=lower($1) AND deleted_at IS NULL)`, base).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return base, nil
		}
	}
	base = strings.TrimRight(appSlug, "-")
	if len(base) > 50 {
		base = strings.TrimRight(base[:50], "-")
	}
	for attempt := 0; attempt < 32; attempt++ {
		raw := make([]byte, 6)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		candidate := fmt.Sprintf("%s-%x", base, raw)
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_apps WHERE lower(route_host_label)=lower($1) AND deleted_at IS NULL)`, candidate).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate application route hostname")
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var req createAppRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	req.ProductID = strings.ToLower(strings.TrimSpace(req.ProductID))
	req.VersionID = strings.ToLower(strings.TrimSpace(req.VersionID))
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if !validUUID(req.ProductID) || !validUUID(req.VersionID) || !appSlugPattern.MatchString(req.Slug) || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128 {
		writeError(w, 400, "validation_failed", "product, version, slug and idempotency key are required")
		return
	}
	if req.DomainRefreshDays != nil && *req.DomainRefreshDays < 1 {
		writeError(w, 400, "validation_failed", "domain refresh days must be at least 1 or omitted for permanent")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var existingJobID, existingAppID string
	err = tx.QueryRow(r.Context(), `SELECT j.id,j.user_app_id FROM deployment_jobs j JOIN user_apps a ON a.id=j.user_app_id WHERE j.idempotency_key=$1 AND a.user_id=$2`, req.IdempotencyKey, p.ID).Scan(&existingJobID, &existingAppID)
	if err == nil {
		_ = tx.Commit(r.Context())
		writeJSON(w, 200, map[string]any{"appId": existingAppID, "jobId": existingJobID, "idempotent": true})
		return
	}
	if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	if err = enforceDeploymentConcurrency(r.Context(), tx, p.ID); err != nil {
		if quota, ok := err.(resourceQuotaError); ok {
			writeError(w, 409, quota.Code, quota.Message)
			return
		}
		s.internalError(w, err)
		return
	}
	var productSlug, imageDigest string
	var runtimeSpec, routeSpec, healthSpec, updateSpec map[string]any
	err = tx.QueryRow(r.Context(), `SELECT p.slug,pv.image_digest,pv.runtime_spec,pv.route_spec,pv.health_spec,pv.update_spec FROM app_products p JOIN app_product_versions pv ON pv.product_id=p.id WHERE p.id=$1 AND pv.id=$2 AND p.status='published' AND pv.published_at IS NOT NULL AND pv.archived_at IS NULL AND pv.deleted_at IS NULL`, req.ProductID, req.VersionID).Scan(&productSlug, &imageDigest, &runtimeSpec, &routeSpec, &healthSpec, &updateSpec)
	if err != nil {
		writeError(w, 404, "template_unavailable", "published product version not found")
		return
	}
	runtimeSpec, err = selectedRuntimeSpec(runtimeSpec, req.Resources)
	if err != nil {
		writeError(w, 400, "invalid_resource_selection", err.Error())
		return
	}
	routeSpec, err = selectedRouteSpec(routeSpec, req.Resources)
	if err != nil {
		writeError(w, 400, "invalid_deployment_configuration", err.Error())
		return
	}
	if err = validateInternalRoutePort(runtimeSpec, routeSpec); err != nil {
		writeError(w, 400, "invalid_deployment_configuration", err.Error())
		return
	}
	if err = enforceRuntimeEntitlements(r.Context(), tx, p.ID, "", runtimeSpec); err != nil {
		if quota, ok := err.(resourceQuotaError); ok {
			writeError(w, 409, quota.Code, quota.Message)
			return
		}
		s.internalError(w, err)
		return
	}
	if missing, dependencyErr := missingRuntimeDependencies(r.Context(), tx, p.ID, "", runtimeSpec); dependencyErr != nil {
		s.internalError(w, dependencyErr)
		return
	} else if len(missing) > 0 {
		writeError(w, 409, "required_dependency_unavailable", "deploy required dependencies first: "+strings.Join(dependencyLabels(missing), ", "))
		return
	}
	if err = validateInitialSecrets(runtimeSpec, req.Secrets); err != nil {
		writeError(w, 400, "validation_failed", err.Error())
		return
	}
	appSlug, slugErr := allocateUserAppSlug(r.Context(), tx, p.ID, req.Slug)
	if slugErr != nil {
		s.internalError(w, slugErr)
		return
	}
	var userSlug string
	if err = tx.QueryRow(r.Context(), `SELECT slug FROM users WHERE id=$1`, p.ID).Scan(&userSlug); err != nil {
		s.internalError(w, err)
		return
	}
	routeHostLabel, labelErr := allocateRouteHostLabel(r.Context(), tx, appSlug, userSlug)
	if labelErr != nil {
		s.internalError(w, labelErr)
		return
	}
	portMappingEnabled := false
	if req.Resources.PortMappingEnabled != nil {
		portMappingEnabled = *req.Resources.PortMappingEnabled
	}
	if portMappingEnabled && !routePortMappingAvailable(routeSpec) {
		writeError(w, 400, "invalid_deployment_configuration", "port mapping is not available for this product version")
		return
	}
	accessPolicy, accessErr := resolveAppAccessPolicy(&req.Access, appAccessPolicy{})
	if accessErr != nil {
		writeError(w, 400, "invalid_access_settings", accessErr.Error())
		return
	}
	if portMappingEnabled && accessPolicy.Enabled {
		writeError(w, 400, "invalid_access_settings", "密码访问不能与端口直连同时开启，端口直连会绕过域名网关")
		return
	}
	var appID string
	err = tx.QueryRow(r.Context(), `INSERT INTO user_apps(user_id,product_id,slug,service_slug,status,port_mapping_enabled,route_host_label,domain_refresh_days,domain_next_refresh_at,access_password_enabled,access_username,access_password_hash) VALUES($1,$2,$3,$4,'deploying',$5,$6,$7,CASE WHEN $7::integer IS NULL THEN NULL ELSE now()+make_interval(days=>$7::integer) END,$8,$9,$10) RETURNING id`, p.ID, req.ProductID, appSlug, appSlug, portMappingEnabled, routeHostLabel, req.DomainRefreshDays, accessPolicy.Enabled, accessPolicy.Username, accessPolicy.Hash).Scan(&appID)
	if err != nil {
		if strings.Contains(err.Error(), "user_apps_user_id_slug_key") || strings.Contains(err.Error(), "user_apps_user_id_service_slug_key") {
			writeError(w, 409, "slug_contention", "并发部署发生应用标识冲突，请重试")
			return
		}
		s.internalError(w, err)
		return
	}
	secretVersions := map[string]string{}
	for key, value := range req.Secrets {
		var secretID, versionID string
		if err = tx.QueryRow(r.Context(), `INSERT INTO app_secrets(user_app_id,key) VALUES($1,$2) RETURNING id`, appID, key).Scan(&secretID); err != nil {
			s.internalError(w, err)
			return
		}
		if err = tx.QueryRow(r.Context(), `SELECT gen_random_uuid()::text`).Scan(&versionID); err != nil {
			s.internalError(w, err)
			return
		}
		encrypted, encryptErr := s.secrets.Encrypt("app.secret.version."+versionID, value)
		if encryptErr != nil {
			s.internalError(w, encryptErr)
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO app_secret_versions(id,app_secret_id,version,encrypted_value) VALUES($1,$2,1,$3)`, versionID, secretID, encrypted); err != nil {
			s.internalError(w, err)
			return
		}
		secretVersions[key] = versionID
	}
	var releaseID string
	err = tx.QueryRow(r.Context(), `INSERT INTO app_releases(user_app_id,product_version_id,release_number,immutable_snapshot,state) VALUES($1,$2,1,jsonb_build_object('product_slug',$3::text,'image_digest',$4::text,'runtime_spec',$5::jsonb,'route_spec',$6::jsonb,'health_spec',$7::jsonb,'update_spec',$8::jsonb,'secret_versions',$9::jsonb),'created') RETURNING id`, appID, req.VersionID, productSlug, imageDigest, runtimeSpec, routeSpec, healthSpec, updateSpec, secretVersions).Scan(&releaseID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,$1,'app.create','user_app',$2,$3,jsonb_build_object('product_id',$4::text,'product_version_id',$5::text,'secret_keys',$6::text[]))`, p.ID, appID, requestID(r.Context()), req.ProductID, req.VersionID, mapKeys(req.Secrets)); err != nil {
		s.internalError(w, err)
		return
	}
	var jobID string
	err = tx.QueryRow(r.Context(), `INSERT INTO deployment_jobs(user_app_id,release_id,idempotency_key) VALUES($1,$2,$3) RETURNING id`, appID, releaseID, req.IdempotencyKey).Scan(&jobID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"appId": appID, "releaseId": releaseID, "jobId": jobID, "status": "deploying", "slug": appSlug})
}

func (s *Server) billingSummary(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var balance, version int64
	if err := s.db.QueryRow(r.Context(), "SELECT balance_cents,version FROM wallets WHERE user_id=$1", p.ID).Scan(&balance, &version); err != nil {
		s.internalError(w, err)
		return
	}
	var planCode, planName string
	var cycle int64
	var entitlements map[string]any
	_ = s.db.QueryRow(r.Context(), "SELECT p.code,p.name,us.cycle_price_cents_snapshot,us.entitlements_snapshot FROM user_subscriptions us JOIN plan_versions pv ON pv.id=us.plan_version_id JOIN plans p ON p.id=pv.plan_id WHERE us.user_id=$1 AND (us.status='grace_period' OR (us.status='active' AND (us.ends_at IS NULL OR us.ends_at+interval '3 days'>now())))", p.ID).Scan(&planCode, &planName, &cycle, &entitlements)
	writeJSON(w, 200, map[string]any{"balanceCents": balance, "version": version, "plan": map[string]any{"code": planCode, "name": planName, "cyclePriceCents": cycle, "entitlements": entitlements}})
}

func (s *Server) billingLedger(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	rows, err := s.db.Query(r.Context(), "SELECT e.id,e.business_type,e.business_ref,e.amount_cents,e.balance_after_cents,e.metadata,e.created_at FROM wallet_ledger_entries e JOIN wallets w ON w.id=e.wallet_id WHERE w.user_id=$1 ORDER BY e.created_at DESC LIMIT 100", p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type entry struct {
		ID                int64          `json:"id"`
		BusinessType      string         `json:"businessType"`
		BusinessRef       string         `json:"businessRef"`
		AmountCents       int64          `json:"amountCents"`
		BalanceAfterCents int64          `json:"balanceAfterCents"`
		Metadata          map[string]any `json:"metadata"`
		CreatedAt         time.Time      `json:"createdAt"`
	}
	items := []entry{}
	for rows.Next() {
		var v entry
		if err := rows.Scan(&v.ID, &v.BusinessType, &v.BusinessRef, &v.AmountCents, &v.BalanceAfterCents, &v.Metadata, &v.CreatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, v)
	}
	writeJSON(w, 200, map[string]any{"entries": items})
}

func (s *Server) billingUsage(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	rows, err := s.db.Query(r.Context(), `SELECT a.user_app_id::text,app.slug,product.slug,app.deleted_at IS NOT NULL,a.usage_code,a.unit,a.window_start,a.window_end,a.quantity::text,a.sealed_at,a.billing_disposition,c.amount_cents,coalesce(c.pricing_version_id,a.price_version_id)::text,price.unit_price_micros
		FROM usage_aggregates a LEFT JOIN usage_charges c ON c.user_id=a.user_id AND c.user_app_id IS NOT DISTINCT FROM a.user_app_id AND c.usage_code=a.usage_code AND c.window_start=a.window_start AND c.window_end=a.window_end AND (a.user_app_id IS NULL OR c.pricing_version_id IS NOT DISTINCT FROM a.price_version_id)
		LEFT JOIN user_apps app ON app.id=a.user_app_id
		LEFT JOIN app_products product ON product.id=app.product_id
		LEFT JOIN pricing_versions price ON price.id=coalesce(c.pricing_version_id,a.price_version_id)
        WHERE a.user_id=$1 ORDER BY a.window_start DESC LIMIT 100`, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var code, unit string
		var appID, appSlug, productSlug *string
		var appDeleted bool
		var start, end time.Time
		var quantity string
		var sealed *time.Time
		var disposition string
		var amount, unitPriceMicros *int64
		var pricingVersionID *string
		if err := rows.Scan(&appID, &appSlug, &productSlug, &appDeleted, &code, &unit, &start, &end, &quantity, &sealed, &disposition, &amount, &pricingVersionID, &unitPriceMicros); err != nil {
			s.internalError(w, err)
			return
		}
		amountCents := int64(0)
		if amount != nil && disposition != "unpriced" {
			amountCents = *amount
		}
		items = append(items, map[string]any{"appId": appID, "appSlug": appSlug, "productSlug": productSlug, "appDeleted": appDeleted, "usageCode": code, "unit": unit, "windowStart": start, "windowEnd": end, "quantity": quantity, "sealedAt": sealed, "billingDisposition": disposition, "amountCents": amountCents, "pricingVersionId": pricingVersionID, "unitPriceMicros": unitPriceMicros})
	}
	writeJSON(w, 200, map[string]any{"usage": items})
}

func (s *Server) adjustWallet(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	userID := r.PathValue("userID")
	var q struct {
		AmountCents int64  `json:"amountCents"`
		BusinessRef string `json:"businessRef"`
		Note        string `json:"note"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.BusinessRef = strings.TrimSpace(q.BusinessRef)
	q.Note = strings.TrimSpace(q.Note)
	if q.AmountCents == 0 || q.BusinessRef == "" || len(q.BusinessRef) > 128 {
		writeError(w, 400, "validation_failed", "non-zero amount and business reference are required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var walletID string
	var balance int64
	var version int64
	if err = tx.QueryRow(r.Context(), "SELECT id,balance_cents,version FROM wallets WHERE user_id=$1 FOR UPDATE", userID).Scan(&walletID, &balance, &version); err == pgx.ErrNoRows {
		writeError(w, 404, "user_not_found", "wallet not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	newBalance := balance + q.AmountCents
	if newBalance < 0 {
		writeError(w, 409, "insufficient_balance", "wallet balance cannot become negative")
		return
	}
	var existingID, existingAmount int64
	if err = tx.QueryRow(r.Context(), "SELECT id,amount_cents FROM wallet_ledger_entries WHERE wallet_id=$1 AND business_type='adjustment' AND business_ref=$2", walletID, q.BusinessRef).Scan(&existingID, &existingAmount); err == nil {
		if existingAmount != q.AmountCents {
			writeError(w, 409, "idempotency_conflict", "business reference is already used with a different amount")
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"entryId": existingID, "balanceCents": balance, "version": version, "idempotent": true})
		return
	} else if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	var entryID int64
	err = tx.QueryRow(r.Context(), "INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,metadata) VALUES($1,'adjustment',$2,$3,$4,jsonb_build_object('note',$5::text,'actor_user_id',$6::text)) RETURNING id", walletID, q.BusinessRef, q.AmountCents, newBalance, q.Note, p.ID).Scan(&entryID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE wallets SET balance_cents=$1,version=version+1 WHERE id=$2 AND balance_cents=$3", newBalance, walletID, balance); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,$2::uuid,'wallet.adjustment','wallet',($2::uuid)::text,$3,jsonb_build_object('amount_cents',$4::bigint,'business_ref',$5::text,'entry_id',$6::bigint))", p.ID, userID, requestID(r.Context()), q.AmountCents, q.BusinessRef, entryID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"entryId": entryID, "balanceCents": newBalance, "version": version + 1})
}

func (s *Server) appDeployments(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	var exists bool
	if err := s.db.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM user_apps WHERE id=$1 AND user_id=$2)", appID, p.ID).Scan(&exists); err != nil {
		s.internalError(w, err)
		return
	}
	if !exists {
		writeError(w, 404, "app_not_found", "app not found")
		return
	}
	rows, err := s.db.Query(r.Context(), "SELECT j.id,j.state,j.attempts,j.last_error,j.created_at,j.updated_at,e.id,e.from_state,e.to_state,e.message,e.created_at FROM deployment_jobs j LEFT JOIN deployment_events e ON e.deployment_job_id=j.id WHERE j.user_app_id=$1 ORDER BY j.created_at DESC,e.created_at", appID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type event struct {
		ID        *int64     `json:"id"`
		FromState *string    `json:"fromState"`
		ToState   *string    `json:"toState"`
		Message   *string    `json:"message"`
		CreatedAt *time.Time `json:"createdAt"`
	}
	type job struct {
		ID        string    `json:"id"`
		State     string    `json:"state"`
		Attempts  int       `json:"attempts"`
		LastError *string   `json:"lastError"`
		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
		Events    []event   `json:"events"`
	}
	items := []job{}
	indexes := map[string]int{}
	for rows.Next() {
		var id, state string
		var attempts int
		var last *string
		var created, updated time.Time
		var ev event
		if err := rows.Scan(&id, &state, &attempts, &last, &created, &updated, &ev.ID, &ev.FromState, &ev.ToState, &ev.Message, &ev.CreatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		idx, ok := indexes[id]
		if !ok {
			idx = len(items)
			indexes[id] = idx
			items = append(items, job{ID: id, State: state, Attempts: attempts, LastError: last, CreatedAt: created, UpdatedAt: updated, Events: []event{}})
		}
		if ev.ID != nil {
			items[idx].Events = append(items[idx].Events, ev)
		}
	}
	writeJSON(w, 200, map[string]any{"deployments": items})
}

func (s *Server) createProduct(w http.ResponseWriter, r *http.Request) {
	var req createProductRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	var err error
	req.Slug, req.Name, err = normalizeProductDetails(req.Slug, req.Name)
	if err != nil {
		writeError(w, 400, "validation_failed", err.Error())
		return
	}
	p, _ := r.Context().Value(principalKey).(principal)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	req.IconURL, err = normalizeProductIconURL(req.IconURL)
	if err != nil {
		writeError(w, 400, "validation_failed", err.Error())
		return
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO app_products(slug,name,icon_url) VALUES($1,$2,$3) RETURNING id`, req.Slug, req.Name, req.IconURL).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "app_products_slug_key") {
			writeError(w, 409, "slug_exists", "product slug already exists")
			return
		}
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,'product.create','app_product',$2,$3,jsonb_build_object('slug',$4::text,'name',$5::text))`, p.ID, id, requestID(r.Context()), req.Slug, req.Name); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "slug": req.Slug, "name": req.Name, "status": "draft"})
}

func (s *Server) adminListProducts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT p.id,p.slug,p.name,p.icon_url,p.status,p.created_at,pv.id,pv.version,pv.version_label,pv.image_digest,
		coalesce(pv.runtime_spec,'{}'::jsonb),coalesce(pv.route_spec,'{}'::jsonb),coalesce(pv.health_spec,'{}'::jsonb),coalesce(pv.update_spec,'{}'::jsonb),pv.published_at,
		(SELECT count(*) FROM app_releases r WHERE r.product_version_id=pv.id),
		t.id::text,t.state,t.attempts,t.last_error,t.created_at,t.updated_at,t.completed_at
		FROM app_products p
		LEFT JOIN app_product_versions pv ON pv.product_id=p.id AND pv.archived_at IS NULL AND pv.deleted_at IS NULL
		LEFT JOIN LATERAL (SELECT t0.id,CASE WHEN c.test_id IS NOT NULL AND c.processed_at IS NULL THEN 'cancelling' ELSE t0.state END AS state,t0.attempts,t0.last_error,t0.created_at,t0.updated_at,t0.completed_at FROM app_product_version_tests t0 LEFT JOIN app_product_version_test_cancellations c ON c.test_id=t0.id WHERE t0.product_version_id=pv.id ORDER BY t0.created_at DESC LIMIT 1) t ON true
		WHERE p.deleted_at IS NULL ORDER BY p.created_at DESC,pv.version DESC`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type version struct {
		ID           string         `json:"id"`
		Version      int            `json:"version"`
		VersionLabel string         `json:"versionLabel"`
		ImageDigest  string         `json:"imageDigest"`
		RuntimeSpec  map[string]any `json:"runtimeSpec"`
		RouteSpec    map[string]any `json:"routeSpec"`
		HealthSpec   map[string]any `json:"healthSpec"`
		UpdateSpec   map[string]any `json:"updateSpec"`
		PublishedAt  *time.Time     `json:"publishedAt"`
		ReleaseCount int            `json:"releaseCount"`
		LatestTest   *struct {
			ID          string     `json:"id"`
			State       string     `json:"state"`
			Attempts    int        `json:"attempts"`
			LastError   *string    `json:"lastError"`
			CreatedAt   time.Time  `json:"createdAt"`
			UpdatedAt   time.Time  `json:"updatedAt"`
			CompletedAt *time.Time `json:"completedAt"`
		} `json:"latestTest,omitempty"`
	}
	type product struct {
		ID        string    `json:"id"`
		Slug      string    `json:"slug"`
		Name      string    `json:"name"`
		IconURL   string    `json:"iconUrl"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"createdAt"`
		Versions  []version `json:"versions"`
	}
	items := []product{}
	indexes := map[string]int{}
	for rows.Next() {
		var id, slug, name, iconURL, status string
		var created time.Time
		var versionID, versionLabel, digest *string
		var number *int
		var runtimeSpec, routeSpec, healthSpec, updateSpec map[string]any
		var published *time.Time
		var releaseCount *int
		var testID, testState, testError *string
		var testAttempts *int
		var testCreated, testUpdated, testCompleted *time.Time
		if err := rows.Scan(&id, &slug, &name, &iconURL, &status, &created, &versionID, &number, &versionLabel, &digest, &runtimeSpec, &routeSpec, &healthSpec, &updateSpec, &published, &releaseCount, &testID, &testState, &testAttempts, &testError, &testCreated, &testUpdated, &testCompleted); err != nil {
			s.internalError(w, err)
			return
		}
		idx, ok := indexes[id]
		if !ok {
			idx = len(items)
			indexes[id] = idx
			items = append(items, product{ID: id, Slug: slug, Name: name, IconURL: iconURL, Status: status, CreatedAt: created, Versions: []version{}})
		}
		if versionID != nil {
			releaseReferences := 0
			if releaseCount != nil {
				releaseReferences = *releaseCount
			}
			item := version{ID: *versionID, Version: *number, VersionLabel: *versionLabel, ImageDigest: *digest, RuntimeSpec: runtimeSpec, RouteSpec: routeSpec, HealthSpec: healthSpec, UpdateSpec: updateSpec, PublishedAt: published, ReleaseCount: releaseReferences}
			if testID != nil {
				item.LatestTest = &struct {
					ID          string     `json:"id"`
					State       string     `json:"state"`
					Attempts    int        `json:"attempts"`
					LastError   *string    `json:"lastError"`
					CreatedAt   time.Time  `json:"createdAt"`
					UpdatedAt   time.Time  `json:"updatedAt"`
					CompletedAt *time.Time `json:"completedAt"`
				}{ID: *testID, State: *testState, Attempts: *testAttempts, LastError: testError, CreatedAt: *testCreated, UpdatedAt: *testUpdated, CompletedAt: testCompleted}
			}
			items[idx].Versions = append(items[idx].Versions, item)
		}
	}
	writeJSON(w, 200, map[string]any{"products": items})
}

func (s *Server) createProductVersion(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("productID")
	if !validUUID(productID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "productID must be a UUID")
		return
	}
	var req createVersionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	req.VersionLabel = strings.TrimSpace(req.VersionLabel)
	if utf8.RuneCountInString(req.VersionLabel) > 64 {
		writeError(w, 400, "validation_failed", "versionLabel may contain at most 64 characters")
		return
	}
	if err := runtimepolicy.ValidateImageDigest(req.ImageDigest); err != nil {
		writeError(w, 400, "validation_failed", err.Error())
		return
	}
	if req.RuntimeSpec == nil {
		req.RuntimeSpec = map[string]any{}
	}
	// The writable container layer is platform-managed. Keep a fixed internal
	// limit for runtime/database compatibility, but never expose or bill it.
	req.RuntimeSpec["systemDiskGiB"] = runtimepolicy.DefaultSystemDiskGiB
	if err := runtimepolicy.ValidateRuntimeSpec(req.RuntimeSpec); err != nil {
		writeError(w, 400, "validation_failed", err.Error())
		return
	}
	if req.RouteSpec == nil {
		req.RouteSpec = map[string]any{}
	}
	if req.HealthSpec == nil {
		req.HealthSpec = map[string]any{}
	}
	if req.UpdateSpec == nil {
		req.UpdateSpec = map[string]any{}
	}
	if err := normalizeProductVersionSpecs(req.RuntimeSpec, req.RouteSpec, req.HealthSpec, req.UpdateSpec); err != nil {
		writeError(w, 400, "validation_failed", err.Error())
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if err = lockProductDependencyGraph(r.Context(), tx); err != nil {
		s.internalError(w, err)
		return
	}
	var productStatus string
	if err = tx.QueryRow(r.Context(), "SELECT status FROM app_products WHERE id=$1 FOR UPDATE", productID).Scan(&productStatus); err == pgx.ErrNoRows {
		writeError(w, 404, "product_not_found", "product not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if productStatus == "retired" {
		writeError(w, http.StatusConflict, "product_retired", "restore the product before creating a new version")
		return
	}
	if err = validateProductDependencies(r.Context(), tx, productID, req.RuntimeSpec); err != nil {
		writeError(w, 400, "validation_failed", err.Error())
		return
	}
	var version int
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(max(version),0)+1 FROM app_product_versions WHERE product_id=$1`, productID).Scan(&version); err != nil {
		s.internalError(w, err)
		return
	}
	var id string
	if err = tx.QueryRow(r.Context(), `INSERT INTO app_product_versions(product_id,version,version_label,image_digest,runtime_spec,route_spec,health_spec,update_spec) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, productID, version, req.VersionLabel, req.ImageDigest, req.RuntimeSpec, req.RouteSpec, req.HealthSpec, req.UpdateSpec).Scan(&id); err != nil {
		s.internalError(w, err)
		return
	}
	p, _ := r.Context().Value(principalKey).(principal)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,'product.version.create','app_product_version',$2,$3,jsonb_build_object('product_id',$4::text,'version',$5::int,'image_digest',$6::text))`, p.ID, id, requestID(r.Context()), productID, version, req.ImageDigest); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "version": version, "status": "testing"})
}

func (s *Server) publishProductVersion(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("productID")
	versionID := r.PathValue("versionID")
	if !validUUID(productID) || !validUUID(versionID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "productID and versionID must be UUIDs")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if err = lockProductDependencyGraph(r.Context(), tx); err != nil {
		s.internalError(w, err)
		return
	}
	var slug, productStatus string
	if err = tx.QueryRow(r.Context(), `SELECT slug,status FROM app_products WHERE id=$1 FOR UPDATE`, productID).Scan(&slug, &productStatus); err != nil {
		writeError(w, 404, "product_not_found", "product not found")
		return
	}
	if productStatus == "retired" {
		writeError(w, http.StatusConflict, "product_retired", "restore the product before publishing a version")
		return
	}
	var digest string
	var runtimeSpec map[string]any
	var publishedAt *time.Time
	if err = tx.QueryRow(r.Context(), `SELECT image_digest,runtime_spec,published_at FROM app_product_versions WHERE id=$1 AND product_id=$2 FOR UPDATE`, versionID, productID).Scan(&digest, &runtimeSpec, &publishedAt); err != nil {
		writeError(w, 404, "version_not_found", "version not found")
		return
	}
	successfulTestID := ""
	alreadyPublished := publishedAt != nil
	if !alreadyPublished {
		if err = validateProductDependencies(r.Context(), tx, productID, runtimeSpec); err != nil {
			writeError(w, http.StatusConflict, "product_dependency_conflict", err.Error())
			return
		}
		if err = tx.QueryRow(r.Context(), `SELECT id FROM app_product_version_tests WHERE product_version_id=$1 AND state='succeeded' ORDER BY completed_at DESC LIMIT 1`, versionID).Scan(&successfulTestID); err == pgx.ErrNoRows {
			writeError(w, http.StatusConflict, "successful_test_required", "a successful test deployment is required before publishing this version")
			return
		} else if err != nil {
			s.internalError(w, err)
			return
		}
	}
	if !alreadyPublished {
		if _, err = tx.Exec(r.Context(), `UPDATE app_products SET status='published' WHERE id=$1`, productID); err != nil {
			s.internalError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), `UPDATE app_product_versions SET published_at=now() WHERE id=$1`, versionID); err != nil {
			s.internalError(w, err)
			return
		}
		if p, ok := r.Context().Value(principalKey).(principal); ok {
			if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'product.publish','app_product_version',$2::text,$3,jsonb_build_object('product_slug',$4::text,'image_digest',$5::text,'test_id',$6::text))`, p.ID, versionID, requestID(r.Context()), slug, digest, successfulTestID); err != nil {
				s.internalError(w, err)
				return
			}
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"published": true, "alreadyPublished": alreadyPublished, "versionId": versionID})
}

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.logger.Error("request failed", "error", err)
	writeError(w, 500, "internal_error", "internal server error")
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 12)
			_, _ = rand.Read(b)
			id = base64.RawURLEncoding.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey("request_id"), id)))
	})
}
func requestID(ctx context.Context) string {
	id, _ := ctx.Value(contextKey("request_id")).(string)
	return id
}
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.internalError(w, errors.New("panic recovered"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
