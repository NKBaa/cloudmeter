package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	runtimepolicy "cloudmeter/internal/runtime"
	"github.com/jackc/pgx/v5"
)

var appSecretKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

func validateInitialSecrets(runtimeSpec map[string]any, provided map[string]string) error {
	required, err := runtimepolicy.RuntimeSecretKeys(runtimeSpec)
	if err != nil {
		return err
	}
	requiredSet := map[string]bool{}
	for _, key := range required {
		requiredSet[key] = true
		value, exists := provided[key]
		if !exists || value == "" || len(value) > 65536 {
			return fmt.Errorf("a non-empty value up to 64 KiB is required for secret %s", key)
		}
	}
	for key, value := range provided {
		if !requiredSet[key] {
			return fmt.Errorf("secret %s is not declared by this product version", key)
		}
		if value == "" || len(value) > 65536 {
			return fmt.Errorf("a non-empty value up to 64 KiB is required for secret %s", key)
		}
	}
	return nil
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type appSecretQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func allowedAppSecretKeys(ctx context.Context, query appSecretQuerier, appID, userID string) ([]string, error) {
	var productID string
	if err := query.QueryRow(ctx, `SELECT product_id::text FROM user_apps WHERE id=$1 AND user_id=$2`, appID, userID).Scan(&productID); err != nil {
		return nil, err
	}
	rows, err := query.Query(ctx, `SELECT runtime_spec FROM app_product_versions
		WHERE product_id=$1 AND published_at IS NOT NULL ORDER BY version`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for rows.Next() {
		var runtimeSpec map[string]any
		if err = rows.Scan(&runtimeSpec); err != nil {
			return nil, err
		}
		keys, keyErr := runtimepolicy.RuntimeSecretKeys(runtimeSpec)
		if keyErr != nil {
			return nil, fmt.Errorf("published product version has invalid Secret keys: %w", keyErr)
		}
		for _, key := range keys {
			allowed[key] = true
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(allowed))
	for key := range allowed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *Server) listAppSecrets(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := strings.TrimSpace(r.PathValue("appID"))
	if !validUUID(appID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "appID must be a UUID")
		return
	}
	allowedKeys, err := allowedAppSecretKeys(r.Context(), s.db, appID, p.ID)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "app_not_found", "app not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT s.key,v.version,v.created_at
		FROM app_secrets s
		JOIN user_apps a ON a.id=s.user_app_id
		JOIN LATERAL (SELECT version,created_at FROM app_secret_versions WHERE app_secret_id=s.id ORDER BY version DESC LIMIT 1) v ON true
		WHERE s.user_app_id=$1 AND a.user_id=$2 ORDER BY s.key`, appID, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type item struct {
		Key       string    `json:"key"`
		Version   int       `json:"version"`
		CreatedAt time.Time `json:"createdAt"`
	}
	items := []item{}
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.Key, &value.Version, &value.CreatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, value)
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": items, "allowedKeys": allowedKeys})
}

func (s *Server) putAppSecret(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := strings.TrimSpace(r.PathValue("appID"))
	if !validUUID(appID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "appID must be a UUID")
		return
	}
	key := strings.ToUpper(strings.TrimSpace(r.PathValue("key")))
	var req struct {
		Value string `json:"value"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !appSecretKeyPattern.MatchString(key) || req.Value == "" || len(req.Value) > 65536 {
		writeError(w, http.StatusBadRequest, "validation_failed", "a valid key and non-empty value up to 64 KiB are required")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	allowedKeys, err := allowedAppSecretKeys(r.Context(), tx, appID, p.ID)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "app_not_found", "app not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	declared := false
	for _, allowedKey := range allowedKeys {
		if key == allowedKey {
			declared = true
			break
		}
	}
	if !declared {
		writeError(w, http.StatusBadRequest, "secret_not_declared", "Secret is not declared by a published version of this product")
		return
	}
	if _, err = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))", appID, key); err != nil {
		s.internalError(w, err)
		return
	}
	var secretID string
	err = tx.QueryRow(r.Context(), `SELECT id::text FROM app_secrets WHERE user_app_id=$1 AND key=$2`, appID, key).Scan(&secretID)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(r.Context(), `INSERT INTO app_secrets(user_app_id,key) VALUES($1,$2) RETURNING id::text`, appID, key).Scan(&secretID)
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	var version int
	if err = tx.QueryRow(r.Context(), "SELECT coalesce(max(version),0)+1 FROM app_secret_versions WHERE app_secret_id=$1", secretID).Scan(&version); err != nil {
		s.internalError(w, err)
		return
	}
	var versionID string
	if err = tx.QueryRow(r.Context(), "SELECT gen_random_uuid()::text").Scan(&versionID); err != nil {
		s.internalError(w, err)
		return
	}
	encrypted, err := s.secrets.Encrypt("app.secret.version."+versionID, req.Value)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO app_secret_versions(id,app_secret_id,version,encrypted_value) VALUES($1,$2,$3,$4)", versionID, secretID, version, encrypted); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id) VALUES($1,$1,'app.secret.rotate','user_app',$2,$3)`, p.ID, appID, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "version": version})
}
