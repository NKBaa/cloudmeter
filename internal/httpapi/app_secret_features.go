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

type secretValidationError struct {
	Status  int
	Code    string
	Message string
}

func (e *secretValidationError) Error() string { return e.Message }

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

// applyReleaseSecretUpdates rotates only values explicitly supplied by the
// user while an application is being updated. The selected product version is
// the authority for both declaration and editability. Every value is
// encrypted before it reaches PostgreSQL; plaintext is never part of the
// release snapshot or audit metadata.
func (s *Server) applyReleaseSecretUpdates(ctx context.Context, tx pgx.Tx, appID string, runtimeSpec map[string]any, provided map[string]string) error {
	if len(provided) == 0 {
		return nil
	}
	options, err := runtimepolicy.RuntimeSecretOptions(runtimeSpec)
	if err != nil {
		return &secretValidationError{Status: http.StatusBadRequest, Code: "invalid_secret_metadata", Message: err.Error()}
	}
	byKey := make(map[string]runtimepolicy.SecretOption, len(options))
	for _, option := range options {
		byKey[option.Key] = option
	}
	normalized := make(map[string]string, len(provided))
	for rawKey, value := range provided {
		key := strings.ToUpper(strings.TrimSpace(rawKey))
		if !appSecretKeyPattern.MatchString(key) || key != rawKey {
			return &secretValidationError{Status: http.StatusBadRequest, Code: "validation_failed", Message: "Secret key must use uppercase letters, digits, and underscores"}
		}
		if _, exists := normalized[key]; exists {
			return &secretValidationError{Status: http.StatusBadRequest, Code: "validation_failed", Message: fmt.Sprintf("Secret %s is specified more than once", key)}
		}
		option, declared := byKey[key]
		if !declared {
			return &secretValidationError{Status: http.StatusBadRequest, Code: "secret_not_declared", Message: fmt.Sprintf("Secret %s is not declared by the selected product version", key)}
		}
		if !option.Editable {
			return &secretValidationError{Status: http.StatusForbidden, Code: "secret_not_editable", Message: fmt.Sprintf("管理员未开放 Secret %s 的用户修改权限", key)}
		}
		if strings.TrimSpace(value) == "" || len(value) > 65536 {
			return &secretValidationError{Status: http.StatusBadRequest, Code: "validation_failed", Message: fmt.Sprintf("Secret %s must be non-empty and no larger than 64 KiB", key)}
		}
		normalized[key] = value
	}
	for _, key := range mapKeys(normalized) {
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))", appID, key); err != nil {
			return err
		}
		var secretID string
		err = tx.QueryRow(ctx, `SELECT id::text FROM app_secrets WHERE user_app_id=$1 AND key=$2`, appID, key).Scan(&secretID)
		if err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `INSERT INTO app_secrets(user_app_id,key) VALUES($1,$2) RETURNING id::text`, appID, key).Scan(&secretID)
		}
		if err != nil {
			return err
		}
		var version int
		if err = tx.QueryRow(ctx, `SELECT coalesce(max(version),0)+1 FROM app_secret_versions WHERE app_secret_id=$1`, secretID).Scan(&version); err != nil {
			return err
		}
		var versionID string
		if err = tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&versionID); err != nil {
			return err
		}
		encrypted, encryptErr := s.secrets.Encrypt("app.secret.version."+versionID, normalized[key])
		if encryptErr != nil {
			return encryptErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO app_secret_versions(id,app_secret_id,version,encrypted_value) VALUES($1,$2,$3,$4)`, versionID, secretID, version, encrypted); err != nil {
			return err
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
	options, err := allowedAppSecretOptions(ctx, query, appID, userID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(options))
	for _, option := range options {
		keys = append(keys, option.Key)
	}
	return keys, nil
}

// allowedAppSecretOptions returns the union declared by published versions.
// Product versions are read in ascending order, so a later version's
// description/permission becomes authoritative when a key is retained across
// versions. Secret values are never queried here.
func allowedAppSecretOptions(ctx context.Context, query appSecretQuerier, appID, userID string) ([]runtimepolicy.SecretOption, error) {
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
	allowed := map[string]runtimepolicy.SecretOption{}
	for rows.Next() {
		var runtimeSpec map[string]any
		if err = rows.Scan(&runtimeSpec); err != nil {
			return nil, err
		}
		options, keyErr := runtimepolicy.RuntimeSecretOptions(runtimeSpec)
		if keyErr != nil {
			return nil, fmt.Errorf("published product version has invalid Secret keys: %w", keyErr)
		}
		for _, option := range options {
			allowed[option.Key] = option
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	options := make([]runtimepolicy.SecretOption, 0, len(allowed))
	for _, option := range allowed {
		options = append(options, option)
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Key < options[j].Key })
	return options, nil
}

func (s *Server) listAppSecrets(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := strings.TrimSpace(r.PathValue("appID"))
	if !validUUID(appID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "appID must be a UUID")
		return
	}
	options, err := allowedAppSecretOptions(r.Context(), s.db, appID, p.ID)
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
	configured := map[string]bool{}
	for _, value := range items {
		configured[value.Key] = true
	}
	optionViews := make([]map[string]any, 0, len(options))
	for _, option := range options {
		optionViews = append(optionViews, map[string]any{
			"key": option.Key, "description": option.Description,
			"editable": option.Editable, "configured": configured[option.Key],
		})
	}
	allowedKeys := make([]string, 0, len(options))
	editableKeys := make([]string, 0, len(options))
	for _, option := range options {
		allowedKeys = append(allowedKeys, option.Key)
		if option.Editable {
			editableKeys = append(editableKeys, option.Key)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": items, "allowedKeys": allowedKeys, "editableKeys": editableKeys, "options": optionViews})
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
	options, err := allowedAppSecretOptions(r.Context(), tx, appID, p.ID)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "app_not_found", "app not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	declared, editable := false, false
	for _, option := range options {
		if key == option.Key {
			declared, editable = true, option.Editable
			break
		}
	}
	if !declared {
		writeError(w, http.StatusBadRequest, "secret_not_declared", "Secret is not declared by a published version of this product")
		return
	}
	if !editable {
		writeError(w, http.StatusForbidden, "secret_not_editable", "管理员未开放此 Secret 的用户修改权限")
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
