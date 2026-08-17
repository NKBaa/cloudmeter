package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type oauthUser struct{ ID, Username, DisplayName, Email string }

type linuxDoOAuthProfile struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Active     bool   `json:"active"`
	Silenced   bool   `json:"silenced"`
	TrustLevel int    `json:"trust_level"`
}

func (s *Server) getOAuthSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT provider,enabled,client_id,scopes,(client_secret<>''),minimum_trust_level FROM oauth_settings ORDER BY provider")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var provider, clientID, scopes string
		var enabled, configured bool
		var minimumTrustLevel int
		if err := rows.Scan(&provider, &enabled, &clientID, &scopes, &configured, &minimumTrustLevel); err != nil {
			s.internalError(w, err)
			return
		}
		callbackURL := ""
		if s.cfg.PublicBaseURL != "" {
			callbackURL = s.cfg.PublicBaseURL + "/api/auth/oauth/" + provider + "/callback"
		}
		items = append(items, map[string]any{
			"provider": provider, "enabled": enabled, "clientId": clientID, "scopes": scopes,
			"secretConfigured": configured, "minimumTrustLevel": minimumTrustLevel,
			"publicBaseUrlConfigured": s.cfg.PublicBaseURL != "", "callbackUrl": callbackURL,
		})
	}
	writeJSON(w, 200, map[string]any{"providers": items})
}

func (s *Server) updateOAuthSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	provider := r.PathValue("provider")
	if !validOAuthProvider(provider) {
		writeError(w, 404, "provider_not_found", "unsupported OAuth provider")
		return
	}
	var q struct {
		Enabled           bool   `json:"enabled"`
		ClientID          string `json:"clientId"`
		ClientSecret      string `json:"clientSecret"`
		Scopes            string `json:"scopes"`
		MinimumTrustLevel int    `json:"minimumTrustLevel"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	enabled, clientID, secret, scopes := q.Enabled, q.ClientID, q.ClientSecret, q.Scopes
	clientID, scopes = strings.TrimSpace(clientID), strings.TrimSpace(scopes)
	if q.MinimumTrustLevel < 0 || q.MinimumTrustLevel > 4 {
		writeError(w, 400, "validation_failed", "minimum trust level must be between 0 and 4")
		return
	}
	if provider != "linuxdo" {
		q.MinimumTrustLevel = 0
	}
	if enabled && s.cfg.PublicBaseURL == "" {
		writeError(w, 409, "oauth_public_base_url_required", "PUBLIC_BASE_URL must be configured before OAuth can be enabled")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var existingSecret string
	if err = tx.QueryRow(r.Context(), "SELECT client_secret FROM oauth_settings WHERE provider=$1 FOR UPDATE", provider).Scan(&existingSecret); err != nil {
		s.internalError(w, err)
		return
	}
	if enabled && (clientID == "" || (secret == "" && existingSecret == "")) {
		writeError(w, 400, "validation_failed", "enabled OAuth requires client ID and client secret")
		return
	}
	encryptedSecret := ""
	if secret != "" {
		encryptedSecret, err = s.secrets.Encrypt("oauth.client_secret."+provider, secret)
		if err != nil {
			s.internalError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), "UPDATE oauth_settings SET enabled=$1,client_id=$2,client_secret=CASE WHEN $3='' THEN client_secret ELSE $3 END,scopes=CASE WHEN $4='' THEN scopes ELSE $4 END,minimum_trust_level=$5,updated_at=now() WHERE provider=$6", enabled, clientID, encryptedSecret, scopes, q.MinimumTrustLevel, provider); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,'oauth.settings.update','oauth_settings',$2,$3,jsonb_build_object('enabled',$4::boolean,'client_id',$5::text,'scopes',$6::text,'secret_updated',$7::boolean,'minimum_trust_level',$8::int))`, p.ID, provider, requestID(r.Context()), enabled, clientID, scopes, secret != "", q.MinimumTrustLevel); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": true})
}

func (s *Server) oauthProviders(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PublicBaseURL == "" {
		writeJSON(w, 200, map[string]any{"providers": []string{}})
		return
	}
	rows, err := s.db.Query(r.Context(), "SELECT provider FROM oauth_settings WHERE enabled AND client_id<>'' AND client_secret<>'' ORDER BY provider")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	providers := []string{}
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			s.internalError(w, err)
			return
		}
		providers = append(providers, provider)
	}
	writeJSON(w, 200, map[string]any{"providers": providers})
}

func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if !validOAuthProvider(provider) {
		writeError(w, 404, "provider_not_found", "unsupported OAuth provider")
		return
	}
	if s.cfg.PublicBaseURL == "" {
		writeError(w, 503, "oauth_public_base_url_required", "PUBLIC_BASE_URL must be configured before OAuth can be used")
		return
	}
	var enabled bool
	var clientID, secret, scopes string
	if err := s.db.QueryRow(r.Context(), "SELECT enabled,client_id,client_secret,scopes FROM oauth_settings WHERE provider=$1", provider).Scan(&enabled, &clientID, &secret, &scopes); err != nil {
		s.internalError(w, err)
		return
	}
	if !enabled || clientID == "" || secret == "" {
		writeError(w, 503, "oauth_not_configured", provider+" OAuth is not configured")
		return
	}
	if _, decryptErr := s.secrets.Decrypt("oauth.client_secret."+provider, secret); decryptErr != nil {
		s.internalError(w, decryptErr)
		return
	}
	state, err := randomToken(32)
	if err != nil {
		s.internalError(w, err)
		return
	}
	stateHash := sha256.Sum256([]byte(state))
	redirectURI := s.cfg.PublicBaseURL + "/api/auth/oauth/" + provider + "/callback"
	if _, err := s.db.Exec(r.Context(), "INSERT INTO oauth_flows(state_hash,provider,redirect_uri,expires_at) VALUES($1,$2,$3,$4)", stateHash[:], provider, redirectURI, time.Now().Add(10*time.Minute)); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"authorizationUrl": oauthAuthorizeURL(provider, clientID, scopes, redirectURI, state)})
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	provider, state, code := r.PathValue("provider"), r.URL.Query().Get("state"), r.URL.Query().Get("code")
	if !validOAuthProvider(provider) || state == "" || code == "" {
		s.oauthRedirectError(w, r, "OAuth 请求无效或已取消")
		return
	}
	stateHash := sha256.Sum256([]byte(state))
	var redirectURI string
	tag, err := s.db.Exec(r.Context(), "UPDATE oauth_flows SET consumed_at=now() WHERE state_hash=$1 AND provider=$2 AND consumed_at IS NULL AND expires_at>now()", stateHash[:], provider)
	if err != nil || tag.RowsAffected() != 1 {
		s.oauthRedirectError(w, r, "OAuth 登录请求已失效，请重试")
		return
	}
	if err := s.db.QueryRow(r.Context(), "SELECT redirect_uri FROM oauth_flows WHERE state_hash=$1", stateHash[:]).Scan(&redirectURI); err != nil {
		s.oauthRedirectError(w, r, "OAuth 登录请求无效")
		return
	}
	user, err := s.fetchOAuthUser(r.Context(), provider, code, redirectURI)
	if err != nil {
		s.logger.Warn("oauth provider failed", "provider", provider, "error", err)
		s.oauthRedirectError(w, r, "无法从 OAuth 服务获取账户信息")
		return
	}
	userID, err := s.resolveOAuthUser(r.Context(), provider, user)
	if err != nil {
		s.oauthRedirectError(w, r, err.Error())
		return
	}
	result, err := randomToken(32)
	if err != nil {
		s.oauthRedirectError(w, r, "无法创建登录会话")
		return
	}
	resultHash := sha256.Sum256([]byte(result))
	if _, err := s.db.Exec(r.Context(), "INSERT INTO oauth_login_results(code_hash,user_id,expires_at) VALUES($1,$2,$3)", resultHash[:], userID, time.Now().Add(2*time.Minute)); err != nil {
		s.oauthRedirectError(w, r, "无法创建登录会话")
		return
	}
	http.Redirect(w, r, s.oauthFrontendRedirect(url.Values{"result": {result}}), http.StatusFound)
}

func (s *Server) oauthExchange(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Result string `json:"result"`
	}
	if err := decodeJSON(r, &q); err != nil || q.Result == "" {
		writeError(w, 400, "invalid_request", "OAuth result is required")
		return
	}
	resultHash := sha256.Sum256([]byte(q.Result))
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var id, email, displayName, status string
	err = tx.QueryRow(r.Context(), `SELECT u.id,u.email,u.display_name,u.status FROM oauth_login_results o JOIN users u ON u.id=o.user_id WHERE o.code_hash=$1 AND o.consumed_at IS NULL AND o.expires_at>now() FOR UPDATE OF o`, resultHash[:]).Scan(&id, &email, &displayName, &status)
	if err != nil || status != "active" {
		writeError(w, 401, "oauth_result_invalid", "OAuth login result is invalid or expired")
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE oauth_login_results SET consumed_at=now() WHERE code_hash=$1", resultHash[:]); err != nil {
		s.internalError(w, err)
		return
	}
	token, err := randomToken(32)
	if err != nil {
		s.internalError(w, err)
		return
	}
	tokenHash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(time.Duration(s.cfg.SessionTTLHours) * time.Hour)
	if _, err = tx.Exec(r.Context(), "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,$2,$3)", id, tokenHash[:], expires); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "expiresAt": expires, "user": map[string]string{"id": id, "email": email, "displayName": displayName}})
}

func (s *Server) resolveOAuthUser(ctx context.Context, provider string, ou oauthUser) (string, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var userID, status string
	err = tx.QueryRow(ctx, `SELECT u.id,u.status FROM oauth_bindings b JOIN users u ON u.id=b.user_id WHERE b.provider=$1 AND b.provider_user_id=$2 FOR UPDATE OF b,u`, provider, ou.ID).Scan(&userID, &status)
	if err == nil {
		if status != "active" {
			return "", fmt.Errorf("该账户已被停用")
		}
		if _, err = tx.Exec(ctx, "UPDATE oauth_bindings SET provider_username=$1,updated_at=now() WHERE provider=$2 AND provider_user_id=$3", ou.Username, provider, ou.ID); err != nil {
			return "", err
		}
		return userID, tx.Commit(ctx)
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	var registrationEnabled, blockAliases bool
	var whitelist []string
	if err = tx.QueryRow(ctx, "SELECT registration_enabled,block_email_aliases,email_domain_whitelist FROM system_state WHERE singleton FOR UPDATE").Scan(&registrationEnabled, &blockAliases, &whitelist); err != nil {
		return "", err
	}
	if !registrationEnabled {
		return "", fmt.Errorf("当前未开放新账户注册")
	}
	email := strings.ToLower(strings.TrimSpace(ou.Email))
	if provider == "github" {
		if email == "" {
			return "", fmt.Errorf("GitHub 账户没有可用的已验证邮箱")
		}
		var policyError string
		email, policyError = validatePolicyEmail(email, whitelist, blockAliases)
		if policyError != "" {
			return "", fmt.Errorf("%s", policyError)
		}
	} else {
		if email != "" {
			var policyError string
			email, policyError = validatePolicyEmail(email, whitelist, blockAliases)
			if policyError != "" {
				return "", fmt.Errorf("%s", policyError)
			}
		} else if len(whitelist) > 0 {
			return "", fmt.Errorf("LinuxDo OAuth 未提供可用于域名白名单校验的邮箱")
		} else {
			email = "linuxdo-" + ou.ID + "@oauth.local"
		}
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=lower($1))", email).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return "", fmt.Errorf("该邮箱已有账户，请先使用原方式登录")
	}
	password, err := randomToken(32)
	if err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(ou.DisplayName)
	if name == "" {
		name = ou.Username
	}
	if name == "" {
		name = provider + " user"
	}
	slug, err := allocateUserSlug(ctx, tx, provider+"-"+ou.ID+"@oauth.local")
	if err != nil {
		return "", err
	}
	if err = tx.QueryRow(ctx, "INSERT INTO users(email,password_hash,display_name,slug,email_verified_at) VALUES($1,$2,$3,$4,now()) RETURNING id", email, string(hash), name, slug).Scan(&userID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='user'", userID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO wallets(user_id) VALUES($1)", userID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO oauth_bindings(provider,provider_user_id,user_id,provider_username) VALUES($1,$2,$3,$4)", provider, ou.ID, userID, ou.Username); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,$1::uuid,'user.oauth_create','user',($1::uuid)::text,$2,jsonb_build_object('provider',$3::text,'provider_user_id',$4::text,'provider_username',$5::text))`, userID, requestID(ctx), provider, ou.ID, ou.Username); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return userID, nil
}

func (s *Server) fetchOAuthUser(ctx context.Context, provider, code, redirectURI string) (oauthUser, error) {
	var clientID, secret string
	var minimumTrustLevel int
	if err := s.db.QueryRow(ctx, "SELECT client_id,client_secret,minimum_trust_level FROM oauth_settings WHERE provider=$1 AND enabled", provider).Scan(&clientID, &secret, &minimumTrustLevel); err != nil {
		return oauthUser{}, err
	}
	secret, err := s.secrets.Decrypt("oauth.client_secret."+provider, secret)
	if err != nil {
		return oauthUser{}, err
	}
	token, err := exchangeOAuthCode(ctx, provider, clientID, secret, code, redirectURI)
	if err != nil {
		return oauthUser{}, err
	}
	return getOAuthUser(ctx, provider, token, minimumTrustLevel)
}

func exchangeOAuthCode(ctx context.Context, provider, clientID, secret, code, redirectURI string) (string, error) {
	endpoint := "https://github.com/login/oauth/access_token"
	var body io.Reader
	headers := map[string]string{"Accept": "application/json"}
	if provider == "github" {
		data, _ := json.Marshal(map[string]string{"client_id": clientID, "client_secret": secret, "code": code, "redirect_uri": redirectURI})
		body = bytes.NewReader(data)
		headers["Content-Type"] = "application/json"
	} else {
		endpoint = "https://connect.linux.do/oauth2/token"
		values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}}
		body = strings.NewReader(values.Encode())
		headers["Content-Type"] = "application/x-www-form-urlencoded"
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(clientID+":"+secret))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("token endpoint returned %d", res.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("invalid token response")
	}
	return out.AccessToken, nil
}

func getOAuthUser(ctx context.Context, provider, token string, minimumTrustLevel int) (oauthUser, error) {
	endpoint := "https://api.github.com/user"
	if provider == "linuxdo" {
		endpoint = "https://connect.linux.do/api/user"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return oauthUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return oauthUser{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return oauthUser{}, fmt.Errorf("user endpoint returned %d", res.StatusCode)
	}
	if provider == "linuxdo" {
		var u linuxDoOAuthProfile
		if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&u); err != nil || u.ID == 0 {
			return oauthUser{}, fmt.Errorf("invalid user response")
		}
		return validateLinuxDoOAuthProfile(u, minimumTrustLevel)
	}
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&u); err != nil || u.ID == 0 {
		return oauthUser{}, fmt.Errorf("invalid user response")
	}
	u.Email, _ = githubVerifiedEmail(ctx, token)
	return oauthUser{ID: strconv.FormatInt(u.ID, 10), Username: u.Login, DisplayName: u.Name, Email: u.Email}, nil
}

func validateLinuxDoOAuthProfile(u linuxDoOAuthProfile, minimumTrustLevel int) (oauthUser, error) {
	if !u.Active {
		return oauthUser{}, fmt.Errorf("inactive LinuxDo account")
	}
	if u.Silenced {
		return oauthUser{}, fmt.Errorf("silenced LinuxDo account")
	}
	if u.TrustLevel < minimumTrustLevel {
		return oauthUser{}, fmt.Errorf("LinuxDo trust level %d is below required level %d", u.TrustLevel, minimumTrustLevel)
	}
	return oauthUser{ID: strconv.FormatInt(u.ID, 10), Username: u.Username, DisplayName: u.Name, Email: u.Email}, nil
}

func githubVerifiedEmail(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("email endpoint returned %d", res.StatusCode)
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&emails); err != nil {
		return "", err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	for _, e := range emails {
		if e.Verified {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("no verified email")
}
func validOAuthProvider(p string) bool { return p == "github" || p == "linuxdo" }
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func (s *Server) requestOrigin(r *http.Request) string {
	if configured := strings.TrimRight(strings.TrimSpace(s.cfg.PublicBaseURL), "/"); configured != "" {
		return configured
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if s.isTrustedProxy(r.RemoteAddr) {
		forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
		if forwarded == "http" || forwarded == "https" {
			scheme = forwarded
		}
	}
	return scheme + "://" + r.Host
}

func normalizePublicBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("PUBLIC_BASE_URL must be an HTTP(S) origin without a path, query or fragment")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (s *Server) isTrustedProxy(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, raw := range s.cfg.TrustedProxyCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) requestClientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	remoteIP := net.ParseIP(host)
	if s.isTrustedProxy(r.RemoteAddr) {
		forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
		if forwardedIP := net.ParseIP(forwarded); forwardedIP != nil {
			return forwardedIP
		}
	}
	return remoteIP
}
func oauthAuthorizeURL(provider, clientID, scopes, redirectURI, state string) string {
	endpoint := "https://github.com/login/oauth/authorize"
	if provider == "linuxdo" {
		endpoint = "https://connect.linux.do/oauth2/authorize"
	}
	q := url.Values{"client_id": {clientID}, "redirect_uri": {redirectURI}, "response_type": {"code"}, "scope": {scopes}, "state": {state}}
	return endpoint + "?" + q.Encode()
}
func (s *Server) oauthRedirectError(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, s.oauthFrontendRedirect(url.Values{"error": {message}}), http.StatusFound)
}

func (s *Server) oauthFrontendRedirect(values url.Values) string {
	path := "/oauth/callback?" + values.Encode()
	if s.cfg.PublicBaseURL == "" {
		return path
	}
	return s.cfg.PublicBaseURL + path
}
