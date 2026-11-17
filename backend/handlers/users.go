package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"zhuapic/models"
	"zhuapic/utils"
)

type updateMeRequest struct {
	Username        string `json:"username"`
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// Serialize security-sensitive edits with one-time account actions.
func (a *App) updateAccountRow(ctx context.Context, user *models.User, revoke bool, query string, args ...any) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version uint64
	if err = tx.QueryRowContext(ctx, `SELECT auth_version FROM users WHERE id=? FOR UPDATE`, user.ID).Scan(&version); err != nil {
		return err
	}
	if version != user.AuthVersion {
		return errors.New("账号已变化，请刷新后重试")
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if revoke {
		if _, err = tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=NOW() WHERE user_id=? AND revoked_at IS NULL`, user.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type saveUserRequest struct {
	Username   string          `json:"username"`
	Email      string          `json:"email"`
	Password   string          `json:"password"`
	Role       string          `json:"role"`
	GroupID    uint            `json:"group_id"`
	Active     *bool           `json:"active"`
	QuotaBytes json.RawMessage `json:"quota_bytes"`
}

func (a *App) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	policy := a.uploadPolicyFor(user, true)
	public := publicPolicy(policy)
	if _, def, err := a.uploadChoices(r, policy, user.Role == "admin"); err == nil && def != nil {
		public["storage_name"] = def.Name
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"user": userResponse(user), "upload_policy": public, "upload_usage": a.uploadUsageFor(user),
	})
}

func (a *App) UpdateMe(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req updateMeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid profile payload")
		return
	}
	username, ok := cleanUsername(req.Username)
	if !ok {
		utils.WriteError(w, http.StatusBadRequest, "invalid username")
		return
	}
	email, ok := cleanEmail(req.Email)
	if !ok {
		utils.WriteError(w, http.StatusBadRequest, "invalid email")
		return
	}
	emailChanged := !strings.EqualFold(email, user.Email)
	if emailChanged {
		utils.WriteError(w, http.StatusBadRequest, "请通过邮箱验证更换登录邮箱；邮件未启用时请联系管理员")
		return
	}
	passwordChanged := req.NewPassword != ""
	nextEmailVerified := user.EmailVerified && !emailChanged
	if emailChanged || passwordChanged {
		stored, err := a.findUserByID(user.ID)
		if err != nil || !utils.VerifyPassword(stored.Password, req.CurrentPassword) {
			utils.WriteError(w, http.StatusUnauthorized, "current password is wrong")
			return
		}
	}
	if passwordChanged {
		if len(req.NewPassword) < 10 || len(req.NewPassword) > 128 {
			utils.WriteError(w, http.StatusBadRequest, "密码需要 10 至 128 位")
			return
		}
		hash, err := utils.HashPassword(req.NewPassword)
		if err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		if err := a.updateAccountRow(r.Context(), user, true, `UPDATE users SET username = ?, email = ?, email_verified = ?, password = ?, auth_version=auth_version+1 WHERE id = ?`, username, email, nextEmailVerified, hash, user.ID); err != nil {
			utils.WriteError(w, http.StatusBadRequest, duplicateEmailMessage(err))
			return
		}
	} else {
		if err := a.updateAccountRow(r.Context(), user, false, `UPDATE users SET username = ?, email = ?, email_verified = ? WHERE id = ?`, username, email, nextEmailVerified, user.ID); err != nil {
			utils.WriteError(w, http.StatusBadRequest, duplicateEmailMessage(err))
			return
		}
	}
	updated, _ := a.findUserByID(user.ID)
	targetEmail := user.Email
	if updated != nil {
		targetEmail = updated.Email
	}
	a.writeAudit(r, updated, "profile.updated", "user", strings.TrimSpace(targetEmail), map[string]any{"password_changed": passwordChanged})
	utils.WriteJSON(w, http.StatusOK, map[string]any{"user": userResponse(updated), "requires_login": passwordChanged})
}

func (a *App) AdminUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.ListUsers(w, r)
	case http.MethodPost:
		a.CreateUser(w, r)
	default:
		utils.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) AdminUser(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		a.UpdateUser(w, r)
	case http.MethodDelete:
		a.DeleteUser(w, r)
	default:
		utils.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) ListUsers(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 && parsed <= 100 {
		limit = parsed
	}
	before := uint(0)
	if parsed, err := strconv.ParseUint(r.URL.Query().Get("before"), 10, 32); err == nil {
		before = uint(parsed)
	}
	query := `SELECT users.id, users.username, users.email, users.email_verified, users.password, users.role, COALESCE(users.group_id, 0), COALESCE(user_groups.name, ''), users.active, users.created_at, users.updated_at, users.avatar_url, users.quota_bytes,
			COUNT(images.id) AS image_count
			FROM users LEFT JOIN user_groups ON user_groups.id = users.group_id LEFT JOIN images ON images.user_id = users.id
			WHERE 1 = 1`
	args := []any{}
	if before > 0 {
		query += ` AND users.id < ?`
		args = append(args, before)
	}
	if search := strings.TrimSpace(r.URL.Query().Get("q")); search != "" {
		query += ` AND (users.username LIKE ? OR users.email LIKE ?)`
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern)
	}
	if group := strings.TrimSpace(r.URL.Query().Get("group")); group != "" {
		groupID, err := strconv.ParseUint(group, 10, 32)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, "invalid group filter")
			return
		}
		query += ` AND COALESCE(users.group_id,0) = ?`
		args = append(args, groupID)
	}
	if active := strings.TrimSpace(r.URL.Query().Get("active")); active != "" {
		if active != "true" && active != "false" {
			utils.WriteError(w, http.StatusBadRequest, "invalid active filter")
			return
		}
		query += ` AND users.active = ?`
		args = append(args, active == "true")
	}
	query += ` GROUP BY users.id, users.username, users.email, users.email_verified, users.password, users.role, users.group_id, user_groups.name, users.active, users.created_at, users.updated_at, users.avatar_url, users.quota_bytes
		ORDER BY users.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := a.DB.Query(query, args...)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load users")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	lastID := uint(0)
	hasMore := false
	for rows.Next() {
		var user models.User
		var imageCount int
		if err := rows.Scan(&user.ID, &user.Username, &user.Email, &user.EmailVerified, &user.Password, &user.Role, &user.GroupID, &user.GroupName, &user.Active, &user.CreatedAt, &user.UpdatedAt, &user.AvatarURL, &user.QuotaBytes, &imageCount); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to read users")
			return
		}
		if len(out) == limit {
			hasMore = true
			break
		}
		item := userResponse(&user)
		item["image_count"] = imageCount
		out = append(out, item)
		lastID = user.ID
	}
	nextBefore := uint(0)
	if hasMore {
		nextBefore = lastID
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"users": out, "has_more": hasMore, "next_before": nextBefore})
}

func (a *App) CreateUser(w http.ResponseWriter, r *http.Request) {
	unlock, lockErr := a.Storage.LockConfig(r.Context())
	if lockErr != nil {
		utils.WriteError(w, 409, lockErr.Error())
		return
	}
	defer unlock()
	var req saveUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user payload")
		return
	}
	username, ok := cleanUsername(req.Username)
	if !ok || len(req.Password) < 6 {
		utils.WriteError(w, http.StatusBadRequest, "invalid username or password")
		return
	}
	email, ok := cleanEmail(req.Email)
	if !ok {
		utils.WriteError(w, http.StatusBadRequest, "invalid email")
		return
	}
	role := cleanRole(req.Role)
	groupID, err := a.validGroupID(req.GroupID, role)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user group")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	hash, err := utils.HashPassword(req.Password)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}
	quota, err := parseUserQuota(req.QuotaBytes, nil)
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	res, err := a.DB.Exec(`INSERT INTO users (username, email, email_verified, password, role, group_id, active, quota_bytes) VALUES (?, ?, 0, ?, ?, NULLIF(?,0), ?, ?)`, username, email, hash, role, groupID, active, quota)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, duplicateEmailMessage(err))
		return
	}
	id, _ := res.LastInsertId()
	user, _ := a.findUserByID(uint(id))
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "user.created", "user", email, map[string]any{"role": role})
	utils.WriteJSON(w, http.StatusCreated, map[string]any{"user": userResponse(user)})
}

func (a *App) UpdateUser(w http.ResponseWriter, r *http.Request) {
	unlock, lockErr := a.Storage.LockConfig(r.Context())
	if lockErr != nil {
		utils.WriteError(w, 409, lockErr.Error())
		return
	}
	defer unlock()
	id, err := utils.UserIDFromString(r.PathValue("id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	user, err := a.findUserByID(id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "user not found")
		return
	}
	var req saveUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user payload")
		return
	}
	username, ok := cleanUsername(req.Username)
	if !ok {
		utils.WriteError(w, http.StatusBadRequest, "invalid username")
		return
	}
	email, ok := cleanEmail(req.Email)
	if !ok {
		utils.WriteError(w, http.StatusBadRequest, "invalid email")
		return
	}
	role := cleanRole(req.Role)
	groupID, err := a.validGroupID(req.GroupID, role)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user group")
		return
	}
	active := user.Active
	if req.Active != nil {
		active = *req.Active
	}
	if err := a.preventLastAdminLoss(user.ID, role, active); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	nextEmailVerified := user.EmailVerified && strings.EqualFold(email, user.Email)
	quota, err := parseUserQuota(req.QuotaBytes, user.QuotaBytes)
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	if req.Password != "" {
		if len(req.Password) < 6 {
			utils.WriteError(w, http.StatusBadRequest, "password must be at least 6 characters")
			return
		}
		hash, err := utils.HashPassword(req.Password)
		if err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		err = a.updateAccountRow(r.Context(), user, true, `UPDATE users SET username = ?, email = ?, email_verified = ?, password = ?, role = ?, group_id = NULLIF(?,0), active = ?, quota_bytes = ?, auth_version=auth_version+1 WHERE id = ?`, username, email, nextEmailVerified, hash, role, groupID, active, quota, id)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, duplicateEmailMessage(err))
			return
		}
	} else {
		revoke := email != user.Email || active != user.Active || role != user.Role
		err = a.updateAccountRow(r.Context(), user, revoke, `UPDATE users SET username = ?, email = ?, email_verified = ?, role = ?, group_id = NULLIF(?,0), active = ?, quota_bytes = ?, auth_version=auth_version+? WHERE id = ?`, username, email, nextEmailVerified, role, groupID, active, quota, revoke, id)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, duplicateEmailMessage(err))
			return
		}
	}
	updated, _ := a.findUserByID(id)
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "user.updated", "user", email, map[string]any{"role": role, "active": active})
	utils.WriteJSON(w, http.StatusOK, map[string]any{"user": userResponse(updated)})
}

func (a *App) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := utils.UserIDFromString(r.PathValue("id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	user, err := a.findUserByID(id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "user not found")
		return
	}
	if err := a.preventLastAdminLoss(user.ID, "user", false); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := a.DB.Exec(`UPDATE images SET user_id = NULL WHERE user_id = ?`, id); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to detach images")
		return
	}
	if _, err := a.DB.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "user.deleted", "user", strings.TrimSpace(user.Email), nil)
	utils.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (a *App) findUserByID(id uint) (*models.User, error) {
	return scanUser(a.DB.QueryRow(`SELECT users.id, users.username, users.email, users.email_verified, users.password, users.role, COALESCE(users.group_id, 0), COALESCE(user_groups.name, ''), users.active, users.created_at, users.updated_at, users.avatar_url, users.quota_bytes, users.auth_version FROM users LEFT JOIN user_groups ON user_groups.id = users.group_id WHERE users.id = ?`, id))
}

func scanUser(row *sql.Row) (*models.User, error) {
	var user models.User
	err := row.Scan(&user.ID, &user.Username, &user.Email, &user.EmailVerified, &user.Password, &user.Role, &user.GroupID, &user.GroupName, &user.Active, &user.CreatedAt, &user.UpdatedAt, &user.AvatarURL, &user.QuotaBytes, &user.AuthVersion)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func userResponse(user *models.User) map[string]any {
	if user == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":             user.ID,
		"username":       user.Username,
		"email":          user.Email,
		"email_verified": user.EmailVerified,
		"role":           user.Role,
		"group_id":       user.GroupID,
		"group_name":     user.GroupName,
		"avatar_url":     user.AvatarURL,
		"quota_bytes":    user.QuotaBytes,
		"active":         user.Active,
		"created_at":     user.CreatedAt.Format(time.RFC3339),
		"updated_at":     user.UpdatedAt.Format(time.RFC3339),
	}
}

func parseUserQuota(raw json.RawMessage, previous *int64) (*int64, error) {
	if len(raw) == 0 {
		return previous, nil
	}
	var quota *int64
	if err := json.Unmarshal(raw, &quota); err != nil || (quota != nil && (*quota < 0 || *quota > 100*1024*1024*1024*1024)) {
		return nil, errors.New("个人容量须为 0 到 100 TB，null 表示跟随用户组")
	}
	return quota, nil
}

func cleanUsername(username string) (string, bool) {
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 80 {
		return "", false
	}
	for _, ch := range username {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.' {
			continue
		}
		return "", false
	}
	return username, true
}

func cleanEmail(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) < 6 || len(email) > 190 || strings.ContainsAny(email, " \t\r\n") {
		return "", false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", false
	}
	return email, true
}

func duplicateEmailMessage(err error) string {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		return "email already exists"
	}
	return "failed to save user"
}

func cleanRole(role string) string {
	if strings.ToLower(strings.TrimSpace(role)) == "admin" {
		return "admin"
	}
	return "user"
}

func (a *App) preventLastAdminLoss(userID uint, nextRole string, nextActive bool) error {
	user, err := a.findUserByID(userID)
	if err != nil {
		return err
	}
	if user.Role != "admin" || (nextRole == "admin" && nextActive) {
		return nil
	}
	var admins int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND active = 1 AND id <> ?`, userID).Scan(&admins); err != nil {
		return err
	}
	if admins == 0 {
		return errors.New("at least one active admin is required")
	}
	return nil
}
