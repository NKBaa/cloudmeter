package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultACMEDirectory = "https://acme-v02.api.letsencrypt.org/directory"

type gatewaySettings struct {
	AccessMode             string    `json:"accessMode"`
	ServerURL              string    `json:"serverUrl"`
	AppBaseDomain          string    `json:"appBaseDomain"`
	StandalonePort         int       `json:"standalonePort"`
	TLSEnabled             bool      `json:"tlsEnabled"`
	HTTPPolicy             string    `json:"httpPolicy"`
	HSTSEnabled            bool      `json:"hstsEnabled"`
	HTTP3Enabled           bool      `json:"http3Enabled"`
	ConsoleCertificateMode string    `json:"consoleCertificateMode"`
	AppCertificateMode     string    `json:"appCertificateMode"`
	ACMEEmail              string    `json:"acmeEmail"`
	ACMECA                 string    `json:"acmeCa"`
	ACMEKeyType            string    `json:"acmeKeyType"`
	RenewIntervalMinutes   int       `json:"renewIntervalMinutes"`
	AccessModeManaged      bool      `json:"accessModeManagedByEnvironment"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

type gatewayCertificateSummary struct {
	Target            string    `json:"target"`
	CommonName        string    `json:"commonName"`
	DNSNames          []string  `json:"dnsNames"`
	Issuer            string    `json:"issuer"`
	NotBefore         time.Time `json:"notBefore"`
	NotAfter          time.Time `json:"notAfter"`
	FingerprintSHA256 string    `json:"fingerprintSha256"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

func (s *Server) readGatewaySettings(ctx context.Context) (gatewaySettings, error) {
	settings := gatewaySettings{StandalonePort: s.cfg.StandalonePort}
	err := s.db.QueryRow(ctx, `SELECT access_mode,server_url,coalesce(app_base_domain,''),tls_enabled,http_policy,hsts_enabled,http3_enabled,
		console_certificate_mode,app_certificate_mode,acme_email,acme_ca,acme_key_type,
		certificate_renew_interval_minutes,updated_at
		FROM system_settings WHERE singleton`).Scan(
		&settings.AccessMode, &settings.ServerURL, &settings.AppBaseDomain, &settings.TLSEnabled,
		&settings.HTTPPolicy, &settings.HSTSEnabled, &settings.HTTP3Enabled,
		&settings.ConsoleCertificateMode, &settings.AppCertificateMode,
		&settings.ACMEEmail, &settings.ACMECA, &settings.ACMEKeyType,
		&settings.RenewIntervalMinutes, &settings.UpdatedAt,
	)
	if settings.ACMECA == "" {
		settings.ACMECA = defaultACMEDirectory
	}
	if settings.ACMEKeyType == "" {
		settings.ACMEKeyType = "p256"
	}
	if settings.RenewIntervalMinutes == 0 {
		settings.RenewIntervalMinutes = 10
	}
	if s.cfg.GatewayAccessMode != "" {
		settings.AccessMode = s.cfg.GatewayAccessMode
		settings.AccessModeManaged = true
	}
	return settings, err
}

func (s *Server) prepareGatewayAccessModeOverride(ctx context.Context) error {
	if s.cfg.GatewayAccessMode == "" {
		return nil
	}
	if _, err := s.db.Exec(ctx, `UPDATE system_settings SET access_mode=$1,updated_at=now() WHERE singleton`, s.cfg.GatewayAccessMode); err != nil {
		return err
	}
	settings, err := s.readGatewaySettings(ctx)
	if err != nil {
		return err
	}
	if err = normalizeGatewaySettings(&settings); err != nil {
		return err
	}
	content, err := s.renderGatewayCaddyfile(settings)
	if err != nil {
		return err
	}
	if err = writeRuntimeFile(s.cfg.CaddyfilePath, content, 0o640); err != nil {
		return err
	}
	go func() {
		for attempt := 0; attempt < 12; attempt++ {
			loadCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			loadErr := s.persistAndLoadCaddyfile(loadCtx, content)
			cancel()
			if loadErr == nil {
				s.logger.Info("gateway access mode override applied", "mode", s.cfg.GatewayAccessMode)
				return
			}
			time.Sleep(5 * time.Second)
		}
		s.logger.Error("gateway access mode override could not be loaded by Caddy", "mode", s.cfg.GatewayAccessMode)
	}()
	return nil
}

func (s *Server) getGatewaySettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.readGatewaySettings(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	certificates, err := s.listGatewayCertificateSummaries(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "certificates": certificates})
}

func normalizeGatewaySettings(settings *gatewaySettings) error {
	if settings.ACMEKeyType == "" {
		settings.ACMEKeyType = "p256"
	}
	if settings.RenewIntervalMinutes == 0 {
		settings.RenewIntervalMinutes = 10
	}
	settings.AccessMode = strings.TrimSpace(settings.AccessMode)
	if settings.AccessMode != "all_caddy" && settings.AccessMode != "apps_only" {
		return fmt.Errorf("访问模式无效")
	}
	serverURL, err := normalizePublicBaseURL(settings.ServerURL)
	if err != nil {
		return fmt.Errorf("控制台地址必须是仅包含协议和主机的 HTTP(S) 地址")
	}
	settings.ServerURL = serverURL
	if settings.AccessMode == "all_caddy" && settings.ServerURL == "" {
		return fmt.Errorf("全站 Caddy 模式必须填写控制台主域名")
	}
	if settings.ServerURL != "" {
		parsed, _ := url.Parse(settings.ServerURL)
		host := parsed.Hostname()
		if settings.AccessMode == "all_caddy" {
			parsed.Scheme = "http"
			if settings.TLSEnabled {
				parsed.Scheme = "https"
			}
			parsed.Host = host
		}
		settings.ServerURL = strings.TrimRight(parsed.String(), "/")
	}
	settings.AppBaseDomain, err = normalizeAppBaseDomain(settings.AppBaseDomain)
	if err != nil {
		return err
	}
	if settings.HTTPPolicy != "redirect" && settings.HTTPPolicy != "allow" && settings.HTTPPolicy != "https_only" {
		return fmt.Errorf("HTTP 访问策略无效")
	}
	if settings.ConsoleCertificateMode != "automatic" && settings.ConsoleCertificateMode != "imported" {
		return fmt.Errorf("控制台证书模式无效")
	}
	if settings.AppCertificateMode != "automatic" && settings.AppCertificateMode != "imported" {
		return fmt.Errorf("应用证书模式无效")
	}
	settings.ACMEEmail = strings.ToLower(strings.TrimSpace(settings.ACMEEmail))
	needsACME := settings.TLSEnabled && ((settings.AccessMode == "all_caddy" && settings.ConsoleCertificateMode == "automatic") || (settings.AppBaseDomain != "" && settings.AppCertificateMode == "automatic"))
	if needsACME {
		parsedEmail, parseErr := mail.ParseAddress(settings.ACMEEmail)
		if parseErr != nil || parsedEmail.Address != settings.ACMEEmail {
			return fmt.Errorf("自动证书申请需要有效的 ACME 联系邮箱")
		}
	}
	settings.ACMECA = strings.TrimSpace(settings.ACMECA)
	if settings.ACMECA == "" {
		settings.ACMECA = defaultACMEDirectory
	}
	caURL, err := url.Parse(settings.ACMECA)
	if err != nil || caURL.Scheme != "https" || caURL.Hostname() == "" || caURL.User != nil {
		return fmt.Errorf("ACME 服务地址必须是有效的 HTTPS URL")
	}
	settings.ACMEKeyType = strings.TrimSpace(settings.ACMEKeyType)
	switch settings.ACMEKeyType {
	case "ed25519", "p256", "p384", "rsa2048", "rsa4096":
	default:
		return fmt.Errorf("ACME 证书密钥类型无效")
	}
	if settings.RenewIntervalMinutes < 1 || settings.RenewIntervalMinutes > 1440 {
		return fmt.Errorf("证书续期扫描周期必须在 1 到 1440 分钟之间")
	}
	return nil
}

func gatewayConsoleHost(serverURL string) string {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func (s *Server) updateGatewaySettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	settings := gatewaySettings{StandalonePort: s.cfg.StandalonePort}
	if err := decodeJSON(r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	settings.StandalonePort = s.cfg.StandalonePort
	if s.cfg.GatewayAccessMode != "" {
		settings.AccessMode = s.cfg.GatewayAccessMode
		settings.AccessModeManaged = true
	}
	if err := normalizeGatewaySettings(&settings); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	if err := s.requireImportedCertificates(r.Context(), settings); err != nil {
		writeError(w, http.StatusConflict, "certificate_required", err.Error())
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var previousDomain string
	var previousTLS bool
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(app_base_domain,''),tls_enabled FROM system_settings WHERE singleton FOR UPDATE`).Scan(&previousDomain, &previousTLS); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE system_settings SET
		access_mode=$1,server_url=$2,app_base_domain=$3,tls_enabled=$4,http_policy=$5,hsts_enabled=$6,http3_enabled=$7,
		console_certificate_mode=$8,app_certificate_mode=$9,acme_email=$10,acme_ca=$11,acme_key_type=$12,
		certificate_renew_interval_minutes=$13,updated_at=now(),updated_by=$14
		WHERE singleton`, settings.AccessMode, settings.ServerURL, settings.AppBaseDomain, settings.TLSEnabled,
		settings.HTTPPolicy, settings.HSTSEnabled, settings.HTTP3Enabled, settings.ConsoleCertificateMode,
		settings.AppCertificateMode, settings.ACMEEmail, settings.ACMECA, settings.ACMEKeyType,
		settings.RenewIntervalMinutes, p.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE app_routes route SET public_path=CASE
		WHEN $1='' THEN '/apps/'||users.slug||'/'||app.slug
		ELSE '//'||app.route_host_label||'.'||$1||'/' END
		FROM user_apps app JOIN users ON users.id=app.user_id WHERE route.user_app_id=app.id`, settings.AppBaseDomain); err != nil {
		s.internalError(w, err)
		return
	}
	if previousDomain != settings.AppBaseDomain || previousTLS != settings.TLSEnabled {
		if _, err = tx.Exec(r.Context(), `DELETE FROM app_access_grants`); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'gateway.settings.update','gateway','caddy',$2,jsonb_build_object('access_mode',$3::text,'tls_enabled',$4::boolean))`,
		p.ID, requestID(r.Context()), settings.AccessMode, settings.TLSEnabled); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}

	settings.UpdatedAt = time.Now()
	content, err := s.renderGatewayCaddyfile(settings)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "gateway_config_invalid", err.Error())
		return
	}
	if err = s.persistAndLoadCaddyfile(r.Context(), content); err != nil {
		writeError(w, http.StatusBadGateway, "caddy_reload_failed", "设置已保存，但 Caddy 重载失败："+err.Error())
		return
	}
	certificates, _ := s.listGatewayCertificateSummaries(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "certificates": certificates, "reloaded": true})
}

func (s *Server) requireImportedCertificates(ctx context.Context, settings gatewaySettings) error {
	targets := []string{}
	if settings.TLSEnabled && settings.AccessMode == "all_caddy" && settings.ConsoleCertificateMode == "imported" {
		targets = append(targets, "console")
	}
	if settings.TLSEnabled && settings.AppBaseDomain != "" && settings.AppCertificateMode == "imported" {
		targets = append(targets, "applications")
	}
	for _, target := range targets {
		var exists bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gateway_certificates WHERE target=$1)`, target).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if target == "console" {
				return fmt.Errorf("请先导入覆盖控制台域名的证书")
			}
			return fmt.Errorf("请先导入覆盖应用泛子域名的证书")
		}
	}
	return nil
}

func (s *Server) renderGatewayCaddyfile(settings gatewaySettings) ([]byte, error) {
	consoleHost := gatewayConsoleHost(settings.ServerURL)
	if settings.AccessMode == "all_caddy" && consoleHost == "" {
		return nil, fmt.Errorf("控制台主域名未配置")
	}
	var out strings.Builder
	out.WriteString("{\n")
	if settings.TLSEnabled {
		out.WriteString("\tauto_https disable_redirects\n")
	} else {
		out.WriteString("\tauto_https off\n")
	}
	out.WriteString("\tadmin 0.0.0.0:2019\n")
	out.WriteString("\tservers {\n\t\ttrusted_proxies static {$GATEWAY_TRUSTED_PROXY_CIDRS}\n\t\ttrusted_proxies_strict\n")
	if settings.HTTP3Enabled {
		out.WriteString("\t\tprotocols h1 h2 h3\n")
	} else {
		out.WriteString("\t\tprotocols h1 h2\n")
	}
	out.WriteString("\t}\n")
	if settings.TLSEnabled && ((settings.AccessMode == "all_caddy" && settings.ConsoleCertificateMode == "automatic") || (settings.AppBaseDomain != "" && settings.AppCertificateMode == "automatic")) {
		out.WriteString("\temail " + settings.ACMEEmail + "\n")
		out.WriteString("\tacme_ca " + settings.ACMECA + "\n")
		out.WriteString("\tkey_type " + settings.ACMEKeyType + "\n")
		out.WriteString(fmt.Sprintf("\trenew_interval %dm\n", settings.RenewIntervalMinutes))
	}
	if settings.TLSEnabled && settings.AppCertificateMode == "automatic" && settings.AppBaseDomain != "" {
		out.WriteString("\ton_demand_tls {\n\t\task http://api:8081/api/internal/caddy/allow-domain\n\t}\n")
	}
	out.WriteString("}\n\n")
	out.WriteString(`(console_proxy) {
	encode zstd gzip
	log {
		output stdout
		format json
	}
	@internal_api path /api/internal/*
	handle @internal_api {
		respond "not found" 404
	}
	@api path /api/* /payments/*
	handle @api {
		reverse_proxy {
			dynamic a api 8081 {
				refresh 1s
				versions ipv4
			}
			header_up X-Forwarded-For {http.request.client_ip}
			flush_interval -1
		}
	}
	handle {
		reverse_proxy {
			dynamic a web 8080 {
				refresh 1s
				versions ipv4
			}
			header_up X-Forwarded-For {http.request.client_ip}
			header_up X-CloudMeter-Entry caddy
			header_up X-CloudMeter-Entry-Token {$ROUTER_INTERNAL_TOKEN}
		}
	}
}

(app_proxy) {
	reverse_proxy {
		dynamic a app-router 8082 {
			refresh 5s
			versions ipv4
		}
		header_up X-Forwarded-For {http.request.client_ip}
		header_up X-CloudMeter-Router-Token {$ROUTER_INTERNAL_TOKEN}
		header_up X-CloudMeter-Route-Mode subdomain
	}
}

`)
	if settings.AccessMode == "all_caddy" {
		s.writeConsoleSites(&out, settings, consoleHost)
	}
	if settings.AppBaseDomain != "" {
		s.writeApplicationSites(&out, settings)
	} else {
		out.WriteString(":80 {\n\trespond \"application domain is not configured\" 421\n}\n")
	}
	return []byte(out.String()), nil
}

func (s *Server) writeConsoleSites(out *strings.Builder, settings gatewaySettings, host string) {
	if settings.TLSEnabled {
		out.WriteString("https://" + host + " {\n")
		if settings.ConsoleCertificateMode == "imported" {
			out.WriteString("\ttls /etc/cloudmeter/runtime/console.crt /etc/cloudmeter/runtime/console.key\n")
		}
		if settings.HSTSEnabled {
			out.WriteString("\theader Strict-Transport-Security \"max-age=31536000; includeSubDomains\"\n")
		}
		out.WriteString("\timport console_proxy\n}\n\n")
		out.WriteString("http://" + host + " {\n")
		s.writeHTTPPolicy(out, settings.HTTPPolicy, "console_proxy", "")
		out.WriteString("}\n\n")
		return
	}
	out.WriteString("http://" + host + " {\n\timport console_proxy\n}\n\n")
}

func (s *Server) writeApplicationSites(out *strings.Builder, settings gatewaySettings) {
	matcher := "*." + settings.AppBaseDomain
	if settings.TLSEnabled {
		out.WriteString("https:// {\n")
		if settings.AppCertificateMode == "imported" {
			out.WriteString("\ttls /etc/cloudmeter/runtime/applications.crt /etc/cloudmeter/runtime/applications.key\n")
		} else {
			out.WriteString("\ttls {\n\t\ton_demand\n\t}\n")
		}
		if settings.HSTSEnabled {
			out.WriteString("\theader Strict-Transport-Security \"max-age=31536000; includeSubDomains\"\n")
		}
		out.WriteString("\t@app host " + matcher + "\n\thandle @app {\n\t\timport app_proxy\n\t}\n\thandle {\n\t\trespond \"misdirected request\" 421\n\t}\n}\n\n")
		out.WriteString("http:// {\n\t@app host " + matcher + "\n\thandle @app {\n")
		s.writeHTTPPolicy(out, settings.HTTPPolicy, "app_proxy", "\t")
		out.WriteString("\t}\n\thandle {\n\t\trespond \"misdirected request\" 421\n\t}\n}\n")
		return
	}
	out.WriteString("http:// {\n\t@app host " + matcher + "\n\thandle @app {\n\t\timport app_proxy\n\t}\n\thandle {\n\t\trespond \"misdirected request\" 421\n\t}\n}\n")
}

func (s *Server) writeHTTPPolicy(out *strings.Builder, policy, proxySnippet, indent string) {
	switch policy {
	case "allow":
		out.WriteString(indent + "\timport " + proxySnippet + "\n")
	case "https_only":
		out.WriteString(indent + "\tabort\n")
	default:
		out.WriteString(indent + "\tredir https://{host}{uri} permanent\n")
	}
}

func (s *Server) persistAndLoadCaddyfile(ctx context.Context, content []byte) error {
	if err := writeRuntimeFile(s.cfg.CaddyfilePath, content, 0o640); err != nil {
		return fmt.Errorf("写入托管 Caddyfile 失败")
	}
	if _, _, err := s.caddyAdminRequest(ctx, http.MethodPost, "/adapt", content, "text/caddyfile"); err != nil {
		return err
	}
	headers := http.Header{"Cache-Control": []string{"must-revalidate"}}
	if _, _, err := s.caddyAdminRequestWithHeaders(ctx, http.MethodPost, "/load", content, "text/caddyfile", headers); err != nil {
		return err
	}
	return nil
}

func writeRuntimeFile(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".cloudmeter-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err = temporary.Chmod(mode); err == nil {
		_, err = temporary.Write(content)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (s *Server) listGatewayCertificateSummaries(ctx context.Context) ([]gatewayCertificateSummary, error) {
	rows, err := s.db.Query(ctx, `SELECT target,common_name,dns_names,issuer,not_before,not_after,fingerprint_sha256,updated_at FROM gateway_certificates ORDER BY target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []gatewayCertificateSummary{}
	for rows.Next() {
		var item gatewayCertificateSummary
		var names []byte
		if err = rows.Scan(&item.Target, &item.CommonName, &names, &item.Issuer, &item.NotBefore, &item.NotAfter, &item.FingerprintSHA256, &item.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(names, &item.DNSNames)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) importGatewayCertificate(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var request struct {
		Target         string `json:"target"`
		CertificatePEM string `json:"certificatePem"`
		PrivateKeyPEM  string `json:"privateKeyPem"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Target != "console" && request.Target != "applications" {
		writeError(w, http.StatusBadRequest, "validation_failed", "证书目标无效")
		return
	}
	request.CertificatePEM = strings.TrimSpace(request.CertificatePEM) + "\n"
	request.PrivateKeyPEM = strings.TrimSpace(request.PrivateKeyPEM) + "\n"
	if _, err := tls.X509KeyPair([]byte(request.CertificatePEM), []byte(request.PrivateKeyPEM)); err != nil {
		writeError(w, http.StatusBadRequest, "certificate_invalid", "证书与私钥不是有效且匹配的 PEM 内容")
		return
	}
	block, _ := pem.Decode([]byte(request.CertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		writeError(w, http.StatusBadRequest, "certificate_invalid", "未找到 PEM 证书")
		return
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "certificate_invalid", "证书无法解析")
		return
	}
	settings, err := s.readGatewaySettings(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	expectedHost := gatewayConsoleHost(settings.ServerURL)
	if request.Target == "applications" && settings.AppBaseDomain != "" {
		expectedHost = "route-check." + settings.AppBaseDomain
	}
	if expectedHost != "" && certificate.VerifyHostname(expectedHost) != nil {
		writeError(w, http.StatusBadRequest, "certificate_domain_mismatch", "证书未覆盖当前"+map[string]string{"console": "控制台域名", "applications": "应用泛子域名"}[request.Target])
		return
	}
	certificateEncrypted, err := s.secrets.Encrypt("gateway.certificate."+request.Target, request.CertificatePEM)
	if err != nil {
		s.internalError(w, err)
		return
	}
	keyEncrypted, err := s.secrets.Encrypt("gateway.private_key."+request.Target, request.PrivateKeyPEM)
	if err != nil {
		s.internalError(w, err)
		return
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	names, _ := json.Marshal(certificate.DNSNames)
	issuer := certificate.Issuer.CommonName
	if issuer == "" {
		issuer = certificate.Issuer.String()
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `INSERT INTO gateway_certificates(target,certificate_pem,private_key_pem,common_name,dns_names,issuer,not_before,not_after,fingerprint_sha256,updated_at,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,now(),$10)
		ON CONFLICT(target) DO UPDATE SET certificate_pem=$2,private_key_pem=$3,common_name=$4,dns_names=$5,issuer=$6,not_before=$7,not_after=$8,fingerprint_sha256=$9,updated_at=now(),updated_by=$10`,
		request.Target, certificateEncrypted, keyEncrypted, certificate.Subject.CommonName, names, issuer,
		certificate.NotBefore, certificate.NotAfter, hex.EncodeToString(fingerprint[:]), p.ID); err != nil {
		s.internalError(w, err)
		return
	}
	column := "console_certificate_mode"
	if request.Target == "applications" {
		column = "app_certificate_mode"
	}
	if _, err = tx.Exec(r.Context(), "UPDATE system_settings SET "+column+"='imported',updated_at=now(),updated_by=$1 WHERE singleton", p.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	if err = s.writeGatewayCertificateFiles(request.Target, request.CertificatePEM, request.PrivateKeyPEM); err != nil {
		s.internalError(w, err)
		return
	}
	settings, _ = s.readGatewaySettings(r.Context())
	if settings.TLSEnabled && ((request.Target == "console" && settings.AccessMode == "all_caddy") || (request.Target == "applications" && settings.AppBaseDomain != "")) {
		if content, renderErr := s.renderGatewayCaddyfile(settings); renderErr == nil {
			if err = s.persistAndLoadCaddyfile(r.Context(), content); err != nil {
				writeError(w, http.StatusBadGateway, "caddy_reload_failed", "证书已保存，但 Caddy 重载失败："+err.Error())
				return
			}
		}
	}
	certificates, _ := s.listGatewayCertificateSummaries(r.Context())
	writeJSON(w, http.StatusCreated, map[string]any{"imported": true, "certificates": certificates})
}

func (s *Server) writeGatewayCertificateFiles(target, certificatePEM, privateKeyPEM string) error {
	dir := filepath.Dir(s.cfg.CaddyfilePath)
	if err := writeRuntimeFile(filepath.Join(dir, target+".crt"), []byte(certificatePEM), 0o640); err != nil {
		return err
	}
	return writeRuntimeFile(filepath.Join(dir, target+".key"), []byte(privateKeyPEM), 0o600)
}

func (s *Server) restoreGatewayCertificates(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT target,certificate_pem,private_key_pem FROM gateway_certificates`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var target, certificateEncrypted, keyEncrypted string
		if err = rows.Scan(&target, &certificateEncrypted, &keyEncrypted); err != nil {
			return err
		}
		certificatePEM, decryptErr := s.secrets.Decrypt("gateway.certificate."+target, certificateEncrypted)
		if decryptErr != nil {
			return fmt.Errorf("decrypt gateway certificate %s: %w", target, decryptErr)
		}
		keyPEM, decryptErr := s.secrets.Decrypt("gateway.private_key."+target, keyEncrypted)
		if decryptErr != nil {
			return fmt.Errorf("decrypt gateway private key %s: %w", target, decryptErr)
		}
		if err = s.writeGatewayCertificateFiles(target, certificatePEM, keyPEM); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Server) consoleEntryGate(w http.ResponseWriter, r *http.Request) {
	var mode string
	if s.cfg.GatewayAccessMode != "" {
		mode = s.cfg.GatewayAccessMode
	} else if err := s.db.QueryRow(r.Context(), `SELECT access_mode FROM system_settings WHERE singleton`).Scan(&mode); err != nil {
		http.Error(w, "entry mode unavailable", http.StatusServiceUnavailable)
		return
	}
	entry := strings.TrimSpace(r.Header.Get("X-CloudMeter-Entry"))
	token := strings.TrimSpace(r.Header.Get("X-CloudMeter-Entry-Token"))
	caddyTokenValid := len(token) == len(s.cfg.RouterToken) && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.RouterToken)) == 1
	if (mode == "all_caddy" && entry == "caddy" && caddyTokenValid) || (mode == "apps_only" && entry != "caddy") {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "console entry denied", http.StatusForbidden)
}

func (s *Server) allowCaddyDomain(w http.ResponseWriter, r *http.Request) {
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("domain")), "."))
	if domain == "" {
		http.Error(w, "domain required", http.StatusBadRequest)
		return
	}
	settings, err := s.readGatewaySettings(r.Context())
	if err != nil {
		http.Error(w, "settings unavailable", http.StatusServiceUnavailable)
		return
	}
	if settings.AccessMode == "all_caddy" && domain == gatewayConsoleHost(settings.ServerURL) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if settings.AppBaseDomain == "" || !strings.HasSuffix(domain, "."+settings.AppBaseDomain) {
		http.Error(w, "domain denied", http.StatusForbidden)
		return
	}
	var allowed bool
	err = s.db.QueryRow(r.Context(), `SELECT EXISTS(
		SELECT 1 FROM app_routes route
		JOIN user_apps app ON app.id=route.user_app_id AND app.instance_id=route.instance_id
		JOIN users ON users.id=app.user_id AND users.status='active'
		WHERE app.status IN ('running','updating') AND app.last_successful_release_id=route.release_id
		AND lower(app.route_host_label||'.'||$1)=$2)`, settings.AppBaseDomain, domain).Scan(&allowed)
	if err != nil || !allowed {
		http.Error(w, "domain denied", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) listGatewayRoutes(w http.ResponseWriter, r *http.Request) {
	settings, err := s.readGatewaySettings(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	items := []map[string]any{}
	rows, err := s.db.Query(r.Context(), `SELECT app.id::text,app.instance_id::text,app.slug,app.service_slug,app.status,
		users.slug,users.email,route.public_path,route.upstream_host,route.upstream_port,coalesce(route.host_port,0),route.updated_at,
		app.domain_refresh_days,app.domain_next_refresh_at
		FROM app_routes route JOIN user_apps app ON app.id=route.user_app_id AND app.instance_id=route.instance_id
		JOIN users ON users.id=app.user_id ORDER BY app.created_at DESC`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, instanceID, slug, serviceSlug, status, userSlug, email, publicPath, upstream string
		var upstreamPort, hostPort int
		var updatedAt time.Time
		var refreshDays *int
		var nextRefreshAt *time.Time
		if err = rows.Scan(&id, &instanceID, &slug, &serviceSlug, &status, &userSlug, &email, &publicPath, &upstream, &upstreamPort, &hostPort, &updatedAt, &refreshDays, &nextRefreshAt); err != nil {
			s.internalError(w, err)
			return
		}
		publicURL := publicPath
		if strings.HasPrefix(publicPath, "//") {
			scheme := "http:"
			if settings.TLSEnabled {
				scheme = "https:"
			}
			publicURL = scheme + publicPath
		}
		host := ""
		if parsed, parseErr := url.Parse(publicURL); parseErr == nil {
			host = parsed.Host
		}
		items = append(items, map[string]any{"id": id, "instanceId": instanceID, "slug": slug, "serviceSlug": serviceSlug,
			"status": status, "ownerSlug": userSlug, "ownerEmail": email, "publicPath": publicPath, "publicUrl": publicURL,
			"host": host, "upstream": upstream, "upstreamPort": upstreamPort, "hostPort": hostPort, "updatedAt": updatedAt,
			"domainRefreshDays": refreshDays, "domainNextRefreshAt": nextRefreshAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["publicUrl"].(string) < items[j]["publicUrl"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"accessMode": settings.AccessMode, "tlsEnabled": settings.TLSEnabled, "routes": items})
}
