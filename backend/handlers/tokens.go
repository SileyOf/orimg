package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"zhuapic/models"
	"zhuapic/utils"
)

const apiTokenPrefix = "zpic_"

type createTokenRequest struct {
	Name        string `json:"name"`
	ExpiresDays int    `json:"expires_days"`
}

func hashAPIToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (a *App) apiTokenUser(r *http.Request) (*models.User, bool) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], apiTokenPrefix) {
		return nil, false
	}
	var userID uint
	var tokenID uint
	err := a.DB.QueryRowContext(r.Context(), `SELECT id, user_id FROM api_tokens
		WHERE token_hash = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())`, hashAPIToken(parts[1])).Scan(&tokenID, &userID)
	if err != nil {
		return nil, false
	}
	user, err := a.findUserByID(userID)
	if err != nil || !user.Active {
		return nil, false
	}
	_, _ = a.DB.ExecContext(r.Context(), `UPDATE api_tokens SET last_used_at = NOW()
		WHERE id = ? AND (last_used_at IS NULL OR last_used_at < DATE_SUB(NOW(), INTERVAL 5 MINUTE))`, tokenID)
	return user, true
}

func (a *App) UserTokens(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	policy := a.uploadPolicyFor(user, true)
	if !policy.AllowAPI {
		utils.WriteError(w, http.StatusForbidden, "api access is disabled for this user group")
		return
	}
	if r.Method == http.MethodPost {
		a.createUserToken(w, r, user)
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id, user_id, name, token_prefix, last_used_at, expires_at, created_at
		FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL ORDER BY id DESC`, user.ID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load api tokens")
		return
	}
	defer rows.Close()
	tokens := []models.APIToken{}
	for rows.Next() {
		var token models.APIToken
		if err := rows.Scan(&token.ID, &token.UserID, &token.Name, &token.TokenPrefix, &token.LastUsedAt,
			&token.ExpiresAt, &token.CreatedAt); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to read api tokens")
			return
		}
		tokens = append(tokens, token)
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (a *App) createUserToken(w http.ResponseWriter, r *http.Request, user *models.User) {
	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid token payload")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) < 2 || len(req.Name) > 80 {
		utils.WriteError(w, http.StatusBadRequest, "token name must be 2 to 80 characters")
		return
	}
	if req.ExpiresDays != 0 && req.ExpiresDays != 30 && req.ExpiresDays != 90 && req.ExpiresDays != 365 {
		utils.WriteError(w, http.StatusBadRequest, "invalid token expiry")
		return
	}
	var activeCount int
	if err := a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL`, user.ID).Scan(&activeCount); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to count api tokens")
		return
	}
	if activeCount >= 10 {
		utils.WriteError(w, http.StatusConflict, "api token limit reached")
		return
	}
	random, err := utils.RandomHex(24)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create api token")
		return
	}
	rawToken := apiTokenPrefix + random
	prefix := rawToken[:min(13, len(rawToken))]
	var expiresAt any
	if req.ExpiresDays > 0 {
		expiresAt = time.Now().AddDate(0, 0, req.ExpiresDays)
	}
	res, err := a.DB.ExecContext(r.Context(), `INSERT INTO api_tokens
		(user_id, name, token_hash, token_prefix, expires_at) VALUES (?, ?, ?, ?, ?)`,
		user.ID, req.Name, hashAPIToken(rawToken), prefix, expiresAt)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to save api token")
		return
	}
	id, _ := res.LastInsertId()
	a.writeAudit(r, user, "api_token.created", "api_token", strconv.FormatInt(id, 10), map[string]any{"name": req.Name})
	utils.WriteJSON(w, http.StatusCreated, map[string]any{"token": rawToken, "id": id, "prefix": prefix})
}

func (a *App) DeleteUserToken(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		utils.WriteError(w, http.StatusBadRequest, "invalid token id")
		return
	}
	res, err := a.DB.ExecContext(r.Context(), `UPDATE api_tokens SET revoked_at = NOW() WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, id, user.ID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to revoke api token")
		return
	}
	changed, _ := res.RowsAffected()
	if changed == 0 {
		utils.WriteError(w, http.StatusNotFound, "api token not found")
		return
	}
	a.writeAudit(r, user, "api_token.revoked", "api_token", strconv.FormatUint(id, 10), nil)
	utils.WriteJSON(w, http.StatusOK, map[string]any{"revoked": true})
}
