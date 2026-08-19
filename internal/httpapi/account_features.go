package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type smtpConnectionSettings struct {
	Host, Username, Password, FromEmail, TLSMode string
	Port                                         int
}

func sendSMTP(settings smtpConnectionSettings, recipients []string, message []byte) error {
	address := net.JoinHostPort(settings.Host, fmt.Sprintf("%d", settings.Port))
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: settings.Host}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var client *smtp.Client
	var err error
	if settings.TLSMode == "tls" {
		connection, dialErr := tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
		if dialErr != nil {
			return dialErr
		}
		client, err = smtp.NewClient(connection, settings.Host)
		if err != nil {
			_ = connection.Close()
			return err
		}
	} else {
		connection, dialErr := dialer.Dial("tcp", address)
		if dialErr != nil {
			return dialErr
		}
		client, err = smtp.NewClient(connection, settings.Host)
		if err != nil {
			_ = connection.Close()
			return err
		}
		if settings.TLSMode == "starttls" {
			if ok, _ := client.Extension("STARTTLS"); !ok {
				_ = client.Close()
				return fmt.Errorf("SMTP server does not support STARTTLS")
			}
			if err = client.StartTLS(tlsConfig); err != nil {
				_ = client.Close()
				return err
			}
		}
	}
	defer client.Close()
	if settings.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)); err != nil {
			return err
		}
	}
	if err = client.Mail(settings.FromEmail); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err = client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func smtpConfigurationReady(enabled bool, host string, port int, username string, passwordConfigured bool, fromEmail, tlsMode string) bool {
	if !enabled || strings.TrimSpace(host) == "" || port < 1 || port > 65535 {
		return false
	}
	if strings.TrimSpace(username) != "" && !passwordConfigured {
		return false
	}
	if tlsMode != "none" && tlsMode != "starttls" && tlsMode != "tls" {
		return false
	}
	fromEmail = strings.ToLower(strings.TrimSpace(fromEmail))
	address, err := mail.ParseAddress(fromEmail)
	return err == nil && address.Address == fromEmail
}

func (s *Server) registrationPolicy(w http.ResponseWriter, r *http.Request) {
	var e, v, block bool
	var domains []string
	if err := s.db.QueryRow(r.Context(), "SELECT registration_enabled,email_verification_required,block_email_aliases,email_domain_whitelist FROM system_state WHERE singleton").Scan(&e, &v, &block, &domains); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"registrationEnabled": e, "emailVerificationRequired": v, "blockEmailAliases": block, "emailDomainWhitelist": domains})
}
func (s *Server) getAuthPolicy(w http.ResponseWriter, r *http.Request) { s.registrationPolicy(w, r) }
func (s *Server) updateAuthPolicy(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		RegistrationEnabled       bool     `json:"registrationEnabled"`
		EmailVerificationRequired bool     `json:"emailVerificationRequired"`
		BlockEmailAliases         bool     `json:"blockEmailAliases"`
		EmailDomainWhitelist      []string `json:"emailDomainWhitelist"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	cleaned, err := normalizeEmailDomainWhitelist(q.EmailDomainWhitelist)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_email_domain_whitelist", err.Error())
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock($1)", int64(729104503)); err != nil {
		s.internalError(w, err)
		return
	}
	var currentRegistration, currentVerification, currentBlockAliases bool
	var currentWhitelist []string
	if err = tx.QueryRow(r.Context(), "SELECT registration_enabled,email_verification_required,block_email_aliases,email_domain_whitelist FROM system_state WHERE singleton FOR UPDATE").Scan(&currentRegistration, &currentVerification, &currentBlockAliases, &currentWhitelist); err != nil {
		s.internalError(w, err)
		return
	}
	var smtpEnabled, smtpPasswordConfigured bool
	var smtpHost, smtpUsername, smtpFromEmail, smtpTLSMode string
	var smtpPort int
	if err = tx.QueryRow(r.Context(), "SELECT enabled,host,port,username,(password<>''),from_email,tls_mode FROM smtp_settings WHERE singleton FOR UPDATE").Scan(&smtpEnabled, &smtpHost, &smtpPort, &smtpUsername, &smtpPasswordConfigured, &smtpFromEmail, &smtpTLSMode); err != nil {
		s.internalError(w, err)
		return
	}
	if q.EmailVerificationRequired && !smtpConfigurationReady(smtpEnabled, smtpHost, smtpPort, smtpUsername, smtpPasswordConfigured, smtpFromEmail, smtpTLSMode) {
		writeError(w, http.StatusConflict, "smtp_required_for_email_verification", "SMTP must be enabled and configured before email verification can be required")
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE system_state SET registration_enabled=$1,email_verification_required=$2,block_email_aliases=$3,email_domain_whitelist=$4 WHERE singleton", q.RegistrationEnabled, q.EmailVerificationRequired, q.BlockEmailAliases, cleaned); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,'auth.policy.update','system_state','singleton',$2,jsonb_build_object('registration_enabled',$3::boolean,'email_verification_required',$4::boolean,'block_email_aliases',$5::boolean,'email_domain_whitelist',$6::text[],'previous_registration_enabled',$7::boolean,'previous_email_verification_required',$8::boolean,'previous_block_email_aliases',$9::boolean,'previous_email_domain_whitelist',$10::text[]))`, p.ID, requestID(r.Context()), q.RegistrationEnabled, q.EmailVerificationRequired, q.BlockEmailAliases, cleaned, currentRegistration, currentVerification, currentBlockAliases, currentWhitelist); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true})
}
func (s *Server) sendVerificationCode(w http.ResponseWriter, r *http.Request) {
	var q struct{ Email string }
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.Email = strings.ToLower(strings.TrimSpace(q.Email))
	if _, err := mail.ParseAddress(q.Email); err != nil {
		writeError(w, 400, "validation_failed", "email is invalid")
		return
	}
	var enabled, required, smtpEnabled, blockAliases bool
	var whitelist []string
	if err := s.db.QueryRow(r.Context(), "SELECT registration_enabled,email_verification_required,block_email_aliases,email_domain_whitelist FROM system_state WHERE singleton").Scan(&enabled, &required, &blockAliases, &whitelist); err != nil {
		s.internalError(w, err)
		return
	}
	if !enabled {
		writeError(w, 403, "registration_disabled", "registration is disabled")
		return
	}
	if !required {
		writeJSON(w, 200, map[string]any{"sent": false, "required": false})
		return
	}
	if normalized, policyError := validatePolicyEmail(q.Email, whitelist, blockAliases); policyError != "" {
		writeError(w, http.StatusForbidden, "email_policy_blocked", policyError)
		return
	} else {
		q.Email = normalized
	}
	var host, user, pass, from, name, mode string
	var port int
	if err := s.db.QueryRow(r.Context(), "SELECT enabled,host,port,username,password,from_email,from_name,tls_mode FROM smtp_settings WHERE singleton").Scan(&smtpEnabled, &host, &port, &user, &pass, &from, &name, &mode); err != nil {
		s.internalError(w, err)
		return
	}
	if !smtpConfigurationReady(smtpEnabled, host, port, user, pass != "", from, mode) {
		writeError(w, 503, "smtp_not_configured", "SMTP is not configured")
		return
	}
	decryptedPass, decryptErr := s.secrets.Decrypt("smtp.password", pass)
	if decryptErr != nil {
		s.internalError(w, decryptErr)
		return
	}
	pass = decryptedPass
	var codeBytes [4]byte
	if _, err := rand.Read(codeBytes[:]); err != nil {
		s.internalError(w, err)
		return
	}
	code := fmt.Sprintf("%06d", (uint32(codeBytes[0])<<24|uint32(codeBytes[1])<<16|uint32(codeBytes[2])<<8|uint32(codeBytes[3]))%1000000)
	h := sha256.Sum256([]byte(code))
	clientIP := s.requestClientIP(r)
	if clientIP == nil {
		writeError(w, http.StatusBadRequest, "invalid_request_origin", "request IP address is invalid")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended('verification-email:' || $1::text,0))", q.Email); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended('verification-ip:' || $1::text,0))", clientIP.String()); err != nil {
		s.internalError(w, err)
		return
	}
	var recentEmail, hourlyEmail, hourlyIP int
	if err = tx.QueryRow(r.Context(), "SELECT count(*) FILTER (WHERE created_at>now()-interval '60 seconds'),count(*) FILTER (WHERE created_at>now()-interval '1 hour') FROM email_verification_codes WHERE lower(email)=lower($1)", q.Email).Scan(&recentEmail, &hourlyEmail); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.QueryRow(r.Context(), "SELECT count(*) FROM email_verification_codes WHERE request_ip=$1 AND created_at>now()-interval '1 hour'", clientIP.String()).Scan(&hourlyIP); err != nil {
		s.internalError(w, err)
		return
	}
	if recentEmail > 0 || hourlyEmail >= 5 || hourlyIP >= 20 {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "verification_rate_limited", "too many verification code requests; please try again later")
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE email_verification_codes SET consumed_at=now() WHERE lower(email)=lower($1) AND consumed_at IS NULL", q.Email); err != nil {
		s.internalError(w, err)
		return
	}
	var verificationID int64
	if err = tx.QueryRow(r.Context(), "INSERT INTO email_verification_codes(email,code_hash,request_ip,expires_at) VALUES($1,$2,$3,now()+interval '10 minutes') RETURNING id", q.Email, h[:], clientIP.String()).Scan(&verificationID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	body := fmt.Sprintf("From: %s <%s>\r\nTo: %s\r\nSubject: CloudMeter verification code\r\n\r\nYour verification code is %s.\r\n", name, from, q.Email, code)
	settings := smtpConnectionSettings{Host: host, Port: port, Username: user, Password: pass, FromEmail: from, TLSMode: mode}
	if err := sendSMTP(settings, []string{q.Email}, []byte(body)); err != nil {
		_, _ = s.db.Exec(r.Context(), "DELETE FROM email_verification_codes WHERE id=$1", verificationID)
		writeError(w, 502, "smtp_send_failed", "could not send verification email")
		return
	}
	writeJSON(w, 200, map[string]any{"sent": true, "required": true})
}
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT u.id,u.email,u.display_name,u.status,u.created_at,
		coalesce(array_agg(DISTINCT r.code) FILTER (WHERE r.code IS NOT NULL),'{}'),
		coalesce(us.status,''),coalesce(p.name,''),us.ends_at,us.grace_ends_at,coalesce(w.balance_cents,0)
		FROM users u
		LEFT JOIN user_roles ur ON ur.user_id=u.id
		LEFT JOIN roles r ON r.id=ur.role_id
		LEFT JOIN user_subscriptions us ON us.user_id=u.id
		LEFT JOIN plan_versions pv ON pv.id=us.plan_version_id
		LEFT JOIN plans p ON p.id=pv.plan_id
		LEFT JOIN wallets w ON w.user_id=u.id
		GROUP BY u.id,us.status,p.name,us.ends_at,us.grace_ends_at,w.balance_cents
		ORDER BY u.created_at DESC`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, email, name, status string
		var created time.Time
		var subscriptionStatus, planName string
		var endsAt, graceEndsAt *time.Time
		var roles []string
		var balanceCents int64
		if err := rows.Scan(&id, &email, &name, &status, &created, &roles, &subscriptionStatus, &planName, &endsAt, &graceEndsAt, &balanceCents); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "email": email, "displayName": name, "status": status, "roles": roles, "createdAt": created, "balanceCents": balanceCents})
	}
	writeJSON(w, 200, map[string]any{"users": items})
}
func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct{ Email, Password, DisplayName, Role string }
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.Email = strings.ToLower(strings.TrimSpace(q.Email))
	if q.Role == "" {
		q.Role = "user"
	}
	if _, err := mail.ParseAddress(q.Email); err != nil || q.DisplayName == "" || len(q.Password) < 12 || (q.Role != "user" && q.Role != "admin") {
		writeError(w, 400, "validation_failed", "valid account fields are required")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(q.Password), bcrypt.DefaultCost)
	if err != nil {
		s.internalError(w, err)
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	slug, err := allocateUserSlug(r.Context(), tx, q.Email)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var id string
	err = tx.QueryRow(r.Context(), "INSERT INTO users(email,password_hash,display_name,slug,email_verified_at) VALUES($1,$2,$3,$4,now()) RETURNING id", q.Email, string(h), q.DisplayName, slug).Scan(&id)
	if err != nil {
		writeError(w, 409, "email_exists", "an account with this email already exists")
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code=$2", id, q.Role); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO wallets(user_id) VALUES($1)", id); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,$2::uuid,'user.admin_create','user',$2::text,$3,jsonb_build_object('role',$4::text,'slug',$5::text))`, p.ID, id, requestID(r.Context()), q.Role, slug); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "role": q.Role})
}
func (s *Server) updateUserStatus(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	userID := r.PathValue("userID")
	var q struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.Status != "active" && q.Status != "suspended" {
		writeError(w, 400, "validation_failed", "status must be active or suspended")
		return
	}
	if userID == p.ID && q.Status != "active" {
		writeError(w, 409, "cannot_suspend_self", "you cannot suspend your own account")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var current string
	var isSuperAdmin bool
	if err = tx.QueryRow(r.Context(), `SELECT u.status,EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.code='super_admin') FROM users u WHERE u.id=$1 FOR UPDATE`, userID).Scan(&current, &isSuperAdmin); err == pgx.ErrNoRows {
		writeError(w, 404, "user_not_found", "user not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if isSuperAdmin && q.Status != "active" {
		var count int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active'`).Scan(&count); err != nil {
			s.internalError(w, err)
			return
		}
		if count <= 1 {
			writeError(w, 409, "last_super_admin", "the last active super administrator cannot be suspended")
			return
		}
	}
	if _, err = tx.Exec(r.Context(), "UPDATE users SET status=$1,updated_at=now() WHERE id=$2", q.Status, userID); err != nil {
		s.internalError(w, err)
		return
	}
	if q.Status != "active" {
		if _, err = tx.Exec(r.Context(), "UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL", userID); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,$2::uuid,'user.status.update','user',($2::uuid)::text,$3,jsonb_build_object('from',$4::text,'to',$5::text))`, p.ID, userID, requestID(r.Context()), current, q.Status); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": true, "status": q.Status})
}

func (s *Server) updateUserRole(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	userID := r.PathValue("userID")
	if !validUUID(userID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "userID must be a UUID")
		return
	}
	var q struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if q.Role != "user" && q.Role != "admin" {
		writeError(w, http.StatusBadRequest, "validation_failed", "role must be user or admin")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var lockedUserID string
	err = tx.QueryRow(r.Context(), `SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedUserID)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "user_not_found", "user not found")
		return
	}
	var currentRoles []string
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(array_agg(role.code ORDER BY role.code),'{}')
		FROM user_roles ur JOIN roles role ON role.id=ur.role_id WHERE ur.user_id=$1`, userID).Scan(&currentRoles); err != nil {
		s.internalError(w, err)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	for _, role := range currentRoles {
		if role == "super_admin" {
			writeError(w, http.StatusConflict, "super_admin_role_immutable", "super administrator role cannot be changed here")
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM user_roles ur USING roles role
		WHERE ur.role_id=role.id AND ur.user_id=$1 AND role.code IN ('user','admin')`, userID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code=$2`, userID, q.Role); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1::uuid,$2::uuid,'user.role.update','user',($2::uuid)::text,$3,jsonb_build_object('from',$4::text[],'to',$5::text))`,
		p.ID, userID, requestID(r.Context()), currentRoles, q.Role); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "role": q.Role, "roles": []string{q.Role}})
}
func (s *Server) getMailSettings(w http.ResponseWriter, r *http.Request) {
	var e, passwordConfigured bool
	var h, u, f, n, m string
	var p int
	if err := s.db.QueryRow(r.Context(), "SELECT enabled,host,port,username,(password<>''),from_email,from_name,tls_mode FROM smtp_settings WHERE singleton").Scan(&e, &h, &p, &u, &passwordConfigured, &f, &n, &m); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": e, "host": h, "port": p, "username": u, "passwordConfigured": passwordConfigured, "fromEmail": f, "fromName": n, "tlsMode": m, "ready": smtpConfigurationReady(e, h, p, u, passwordConfigured, f, m)})
}
func (s *Server) updateMailSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		Enabled   bool
		Host      string
		Port      int
		Username  string
		Password  string
		FromEmail string
		FromName  string
		TLSMode   string
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.Port == 0 {
		q.Port = 587
	}
	if q.TLSMode == "" {
		q.TLSMode = "starttls"
	}
	q.Host = strings.TrimSpace(q.Host)
	q.Username = strings.TrimSpace(q.Username)
	q.FromEmail = strings.ToLower(strings.TrimSpace(q.FromEmail))
	q.FromName = strings.TrimSpace(q.FromName)
	if q.Port < 1 || q.Port > 65535 || (q.TLSMode != "none" && q.TLSMode != "starttls" && q.TLSMode != "tls") || strings.ContainsAny(q.FromName, "\r\n") {
		writeError(w, 400, "validation_failed", "valid SMTP port, TLS mode and sender name are required")
		return
	}
	if q.Enabled {
		if q.Host == "" {
			writeError(w, 400, "validation_failed", "SMTP host is required when mail is enabled")
			return
		}
		address, parseErr := mail.ParseAddress(q.FromEmail)
		if parseErr != nil || address.Address != q.FromEmail {
			writeError(w, 400, "validation_failed", "a valid sender email is required when mail is enabled")
			return
		}
	}
	encryptedPassword := ""
	var err error
	if q.Password != "" {
		encryptedPassword, err = s.secrets.Encrypt("smtp.password", q.Password)
		if err != nil {
			s.internalError(w, err)
			return
		}
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock($1)", int64(729104503)); err != nil {
		s.internalError(w, err)
		return
	}
	var verificationRequired bool
	if err = tx.QueryRow(r.Context(), "SELECT email_verification_required FROM system_state WHERE singleton FOR UPDATE").Scan(&verificationRequired); err != nil {
		s.internalError(w, err)
		return
	}
	var existingPasswordConfigured bool
	if err = tx.QueryRow(r.Context(), "SELECT password<>'' FROM smtp_settings WHERE singleton FOR UPDATE").Scan(&existingPasswordConfigured); err != nil {
		s.internalError(w, err)
		return
	}
	passwordConfigured := q.Password != "" || existingPasswordConfigured
	if q.Enabled && q.Username != "" && !passwordConfigured {
		writeError(w, http.StatusBadRequest, "validation_failed", "an SMTP password is required when a username is configured")
		return
	}
	if verificationRequired && !smtpConfigurationReady(q.Enabled, q.Host, q.Port, q.Username, passwordConfigured, q.FromEmail, q.TLSMode) {
		writeError(w, http.StatusConflict, "email_verification_requires_smtp", "SMTP cannot be disabled or made invalid while email verification is required")
		return
	}
	_, err = tx.Exec(r.Context(), "UPDATE smtp_settings SET enabled=$1,host=$2,port=$3,username=$4,password=CASE WHEN $5='' THEN password ELSE $5 END,from_email=$6,from_name=$7,tls_mode=$8,updated_at=now() WHERE singleton", q.Enabled, q.Host, q.Port, q.Username, encryptedPassword, q.FromEmail, q.FromName, q.TLSMode)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,'smtp.settings.update','smtp_settings','singleton',$2,jsonb_build_object('enabled',$3::boolean,'host',$4::text,'port',$5::int,'tls_mode',$6::text,'password_updated',$7::boolean))`, p.ID, requestID(r.Context()), q.Enabled, q.Host, q.Port, q.TLSMode, q.Password != ""); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": true, "ready": smtpConfigurationReady(q.Enabled, q.Host, q.Port, q.Username, passwordConfigured, q.FromEmail, q.TLSMode), "passwordConfigured": passwordConfigured})
}
func (s *Server) listAnnouncements(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT id,title,content,severity,starts_at,ends_at FROM announcements WHERE published AND starts_at<=now() AND (ends_at IS NULL OR ends_at>now()) ORDER BY starts_at DESC")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, t, c, v string
		var st time.Time
		var en *time.Time
		if err := rows.Scan(&id, &t, &c, &v, &st, &en); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "title": t, "content": c, "severity": v, "startsAt": st, "endsAt": en})
	}
	writeJSON(w, 200, map[string]any{"announcements": items})
}
func (s *Server) adminListAnnouncements(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT id,title,content,severity,published,starts_at,ends_at,created_at FROM announcements ORDER BY created_at DESC")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, t, c, v string
		var published bool
		var starts, created time.Time
		var ends *time.Time
		if err := rows.Scan(&id, &t, &c, &v, &published, &starts, &ends, &created); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "title": t, "content": c, "severity": v, "published": published, "startsAt": starts, "endsAt": ends, "createdAt": created})
	}
	writeJSON(w, 200, map[string]any{"announcements": items})
}
func (s *Server) createAnnouncement(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		Title, Content, Severity string
		Published                bool
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.Severity == "" {
		q.Severity = "info"
	}
	q.Title = strings.TrimSpace(q.Title)
	q.Content = strings.TrimSpace(q.Content)
	if q.Title == "" || q.Content == "" || len(q.Title) > 160 || len(q.Content) > 10000 || (q.Severity != "info" && q.Severity != "warning" && q.Severity != "critical") {
		writeError(w, http.StatusBadRequest, "validation_failed", "title, content and a valid announcement severity are required")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	if err = tx.QueryRow(r.Context(), "INSERT INTO announcements(title,content,severity,published,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id", q.Title, q.Content, q.Severity, q.Published, p.ID).Scan(&id); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,'announcement.create','announcement',$2,$3,jsonb_build_object('published',$4::boolean,'severity',$5::text))`, p.ID, id, requestID(r.Context()), q.Published, q.Severity); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}
func (s *Server) updateAnnouncement(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		Published bool `json:"published"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	id := r.PathValue("announcementID")
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), "UPDATE announcements SET published=$1,updated_at=now() WHERE id=$2", q.Published, id)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		writeError(w, 404, "announcement_not_found", "announcement not found")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'announcement.publish.update','announcement',$2,$3,jsonb_build_object('published',$4::boolean))`, p.ID, id, requestID(r.Context()), q.Published); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": true, "published": q.Published})
}

var _ = pgx.ErrNoRows

func (s *Server) testMailSettings(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.Email = strings.ToLower(strings.TrimSpace(q.Email))
	if _, err := mail.ParseAddress(q.Email); err != nil {
		writeError(w, 400, "validation_failed", "test recipient email is invalid")
		return
	}
	var enabled bool
	var host, user, pass, from, name, mode string
	var port int
	if err := s.db.QueryRow(r.Context(), "SELECT enabled,host,port,username,password,from_email,from_name,tls_mode FROM smtp_settings WHERE singleton").Scan(&enabled, &host, &port, &user, &pass, &from, &name, &mode); err != nil {
		s.internalError(w, err)
		return
	}
	if !smtpConfigurationReady(enabled, host, port, user, pass != "", from, mode) {
		writeError(w, 503, "smtp_not_configured", "SMTP is not configured")
		return
	}
	decryptedPass, decryptErr := s.secrets.Decrypt("smtp.password", pass)
	if decryptErr != nil {
		s.internalError(w, decryptErr)
		return
	}
	pass = decryptedPass
	body := fmt.Sprintf("From: %s <%s>\r\nTo: %s\r\nSubject: CloudMeter SMTP test\r\n\r\nThis is a test email from CloudMeter.\r\n", name, from, q.Email)
	settings := smtpConnectionSettings{Host: host, Port: port, Username: user, Password: pass, FromEmail: from, TLSMode: mode}
	if err := sendSMTP(settings, []string{q.Email}, []byte(body)); err != nil {
		writeError(w, 502, "smtp_send_failed", "could not send test email")
		return
	}
	writeJSON(w, 200, map[string]any{"sent": true, "recipient": q.Email})
}
