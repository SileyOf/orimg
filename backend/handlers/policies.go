package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"zhuapic/models"
	"zhuapic/storage"
	"zhuapic/utils"
)

type updatePolicyRequest struct {
	MaxFileMB             int64  `json:"max_file_mb"`
	MaxBatchCount         int    `json:"max_batch_count"`
	MaxDailyUploads       int64  `json:"max_daily_uploads"`
	MaxDailyBytes         int64  `json:"max_daily_bytes"`
	TotalQuotaBytes       int64  `json:"total_quota_bytes"`
	StorageMode           string `json:"storage_mode"`
	AllowStorageSelection bool   `json:"allow_storage_selection"`
	RetentionDays         int    `json:"retention_days"`
	AllowedTypes          string `json:"allowed_types"`
	AllowSVG              bool   `json:"allow_svg"`
	AllowAPI              bool   `json:"allow_api"`
	Active                bool   `json:"active"`
}

type createGroupRequest struct {
	Name            string `json:"name"`
	CopyFromGroupID uint   `json:"copy_from_group_id"`
}

type updateGroupRequest struct {
	Name string `json:"name"`
}

func (a *App) AdminGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listGroups(w)
	case http.MethodPost:
		a.createGroup(w, r)
	default:
		utils.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) AdminGroup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		a.updateGroup(w, r)
	case http.MethodDelete:
		a.deleteGroup(w, r)
	default:
		utils.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) listGroups(w http.ResponseWriter) {
	rows, err := a.DB.Query(`SELECT user_group.id, user_group.name, user_group.slug, user_group.policy_id, user_group.is_system, COUNT(users.id),
		policies.id, policies.name, policies.slug, policies.max_file_mb, policies.max_batch_count,
		policies.max_daily_uploads, policies.max_daily_bytes, policies.total_quota_bytes, policies.storage_mode,
		policies.retention_days, policies.allowed_types, policies.allow_svg, policies.allow_api, policies.active,
		policies.created_at, policies.updated_at, policies.allow_storage_selection
		FROM user_groups user_group
		JOIN upload_policies policies ON policies.id = user_group.policy_id
		LEFT JOIN users ON users.group_id = user_group.id
		GROUP BY user_group.id, user_group.name, user_group.slug, user_group.policy_id, user_group.is_system,
		policies.id, policies.name, policies.slug, policies.max_file_mb, policies.max_batch_count,
		policies.max_daily_uploads, policies.max_daily_bytes, policies.total_quota_bytes, policies.storage_mode,
		policies.retention_days, policies.allowed_types, policies.allow_svg, policies.allow_api, policies.active,
		policies.created_at, policies.updated_at, policies.allow_storage_selection
		ORDER BY user_group.id ASC`)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load user groups")
		return
	}
	defer rows.Close()
	groups := []models.UserGroup{}
	for rows.Next() {
		var group models.UserGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.Slug, &group.PolicyID, &group.IsSystem, &group.MemberCount,
			&group.Policy.ID, &group.Policy.Name, &group.Policy.Slug, &group.Policy.MaxFileMB, &group.Policy.MaxBatchCount,
			&group.Policy.MaxDailyUploads, &group.Policy.MaxDailyBytes, &group.Policy.TotalQuotaBytes, &group.Policy.StorageMode,
			&group.Policy.RetentionDays, &group.Policy.AllowedTypes, &group.Policy.AllowSVG, &group.Policy.AllowAPI,
			&group.Policy.Active, &group.Policy.CreatedAt, &group.Policy.UpdatedAt, &group.Policy.AllowStorageSelection); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to read user groups")
			return
		}
		groups = append(groups, group)
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"groups": groups, "guest_group_id": utils.SettingInt64(a.DB, "guest_group_id", 0, 0, 1<<32-1)})
}

func (a *App) createGroup(w http.ResponseWriter, r *http.Request) {
	unlock, lockErr := a.Coordinator.LockTargetConfig(r.Context())
	if lockErr != nil {
		utils.WriteError(w, 409, lockErr.Error())
		return
	}
	defer unlock()
	var req createGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user group payload")
		return
	}
	name, ok := cleanGroupName(req.Name)
	if !ok {
		utils.WriteError(w, http.StatusBadRequest, "group name must be 2 to 80 characters")
		return
	}
	if duplicate, err := a.groupNameExists(name, 0); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to validate user group")
		return
	} else if duplicate {
		utils.WriteError(w, http.StatusConflict, "group name already exists")
		return
	}
	baseGroupID := req.CopyFromGroupID
	random, err := utils.RandomHex(6)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create user group")
		return
	}
	slug := "group_" + random
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create user group")
		return
	}
	defer tx.Rollback()
	var res sql.Result
	if baseGroupID == 0 {
		res, err = tx.Exec(`INSERT INTO upload_policies (name,slug,allowed_types,total_quota_bytes) VALUES (?,?,?,?)`, name, slug, "jpg,jpeg,png,gif,webp,avif", int64(5*1024*1024*1024))
	} else {
		res, err = tx.Exec(`INSERT INTO upload_policies
		(name, slug, max_file_mb, max_batch_count, max_daily_uploads, max_daily_bytes, total_quota_bytes,
		storage_mode, retention_days, allowed_types, allow_svg, allow_api, active, allow_storage_selection)
		SELECT ?, ?, policy.max_file_mb, policy.max_batch_count, policy.max_daily_uploads, policy.max_daily_bytes,
		policy.total_quota_bytes, policy.storage_mode, policy.retention_days, policy.allowed_types,
		policy.allow_svg, policy.allow_api, policy.active, policy.allow_storage_selection
		FROM user_groups user_group JOIN upload_policies policy ON policy.id = user_group.policy_id
		WHERE user_group.id = ?`, name, slug, baseGroupID)
	}
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to copy upload policy")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		utils.WriteError(w, http.StatusBadRequest, "source user group not found")
		return
	}
	policyID, err := res.LastInsertId()
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create user group")
		return
	}
	groupResult, err := tx.Exec(`INSERT INTO user_groups (name, slug, policy_id, is_system) VALUES (?, ?, ?, 0)`, name, slug, policyID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create user group")
		return
	}
	groupID, _ := groupResult.LastInsertId()
	if err := tx.Commit(); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create user group")
		return
	}
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "user_group.created", "user_group", strconv.FormatInt(groupID, 10), map[string]any{"name": name})
	utils.WriteJSON(w, http.StatusCreated, map[string]any{"created": true, "id": groupID})
}

func (a *App) updateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := utils.UserIDFromString(r.PathValue("id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user group id")
		return
	}
	var req updateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user group payload")
		return
	}
	name, ok := cleanGroupName(req.Name)
	if !ok {
		utils.WriteError(w, http.StatusBadRequest, "group name must be 2 to 80 characters")
		return
	}
	if duplicate, err := a.groupNameExists(name, id); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to validate user group")
		return
	} else if duplicate {
		utils.WriteError(w, http.StatusConflict, "group name already exists")
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to update user group")
		return
	}
	defer tx.Rollback()
	var policyID uint
	if err := tx.QueryRow(`SELECT policy_id FROM user_groups WHERE id = ? FOR UPDATE`, id).Scan(&policyID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			utils.WriteError(w, http.StatusNotFound, "user group not found")
		} else {
			utils.WriteError(w, http.StatusInternalServerError, "failed to update user group")
		}
		return
	}
	if _, err := tx.Exec(`UPDATE user_groups SET name = ? WHERE id = ?`, name, id); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to update user group")
		return
	}
	if _, err := tx.Exec(`UPDATE upload_policies SET name = ? WHERE id = ?`, name, policyID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to update upload policy name")
		return
	}
	if err := tx.Commit(); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to update user group")
		return
	}
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "user_group.updated", "user_group", strconv.FormatUint(uint64(id), 10), map[string]any{"name": name})
	utils.WriteJSON(w, http.StatusOK, map[string]any{"updated": true})
}

func (a *App) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := utils.UserIDFromString(r.PathValue("id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid user group id")
		return
	}
	unlock, err := a.Coordinator.LockTargetConfig(r.Context())
	if err != nil {
		utils.WriteError(w, 409, err.Error())
		return
	}
	defer unlock()
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to delete user group")
		return
	}
	defer tx.Rollback()
	var policyID uint
	var name string
	var system bool
	if err := tx.QueryRow(`SELECT policy_id, name, is_system FROM user_groups WHERE id = ? FOR UPDATE`, id).Scan(&policyID, &name, &system); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			utils.WriteError(w, http.StatusNotFound, "user group not found")
		} else {
			utils.WriteError(w, http.StatusInternalServerError, "failed to delete user group")
		}
		return
	}
	var members int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE group_id = ?`, id).Scan(&members); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to delete user group")
		return
	}
	if _, err := tx.Exec(`UPDATE users SET group_id=NULL WHERE group_id=?`, id); err != nil {
		utils.WriteError(w, 500, "failed to detach group members")
		return
	}
	if _, err := tx.Exec("UPDATE site_settings SET value='0' WHERE `key`='guest_group_id' AND value=?", strconv.FormatUint(uint64(id), 10)); err != nil {
		utils.WriteError(w, 500, "failed to detach guest group")
		return
	}
	if _, err := tx.Exec(`DELETE FROM user_groups WHERE id = ?`, id); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to delete user group")
		return
	}
	if _, err := tx.Exec(`DELETE FROM upload_policies WHERE id = ?`, policyID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to delete upload policy")
		return
	}
	if err := tx.Commit(); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to delete user group")
		return
	}
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "user_group.deleted", "user_group", strconv.FormatUint(uint64(id), 10), map[string]any{"name": name, "unassigned_users": members})
	utils.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (a *App) groupNameExists(name string, exceptID uint) (bool, error) {
	var count int
	err := a.DB.QueryRow(`SELECT COUNT(*) FROM user_groups WHERE LOWER(name) = LOWER(?) AND id <> ?`, name, exceptID).Scan(&count)
	return count > 0, err
}

func cleanGroupName(value string) (string, bool) {
	value = strings.TrimSpace(value)
	length := len([]rune(value))
	return value, length >= 2 && length <= 80
}

func (a *App) UpdateUploadPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := utils.UserIDFromString(r.PathValue("id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid policy id")
		return
	}
	var req updatePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid policy payload")
		return
	}
	req.MaxFileMB = clampInt64(req.MaxFileMB, 1, 200)
	req.MaxBatchCount = int(clampInt64(int64(req.MaxBatchCount), 1, 100))
	req.MaxDailyUploads = clampInt64(req.MaxDailyUploads, 0, 1000000)
	req.MaxDailyBytes = clampInt64(req.MaxDailyBytes, 0, 10*1024*1024*1024*1024)
	req.TotalQuotaBytes = clampInt64(req.TotalQuotaBytes, 0, 100*1024*1024*1024*1024)
	req.RetentionDays = int(clampInt64(int64(req.RetentionDays), 0, 3650))
	unlock, lockErr := a.Coordinator.LockTargetConfig(r.Context())
	if lockErr != nil {
		utils.WriteError(w, 409, lockErr.Error())
		return
	}
	defer unlock()
	if strings.HasPrefix(req.StorageMode, "target:") {
		targetID, parseErr := storage.ParseTargetSelection(req.StorageMode)
		if parseErr != nil {
			utils.WriteError(w, 400, parseErr.Error())
			return
		}
		target, loadErr := a.Coordinator.Target(r.Context(), targetID)
		if loadErr != nil || !target.Enabled || !target.Configured {
			utils.WriteError(w, 400, "所选存储未启用或配置不完整")
			return
		}
	} else if req.StorageMode != "local" && req.StorageMode != "s3" && req.StorageMode != "mirror" {
		req.StorageMode = "default"
	}
	req.AllowedTypes = sanitizeSetting("allowed_types", req.AllowedTypes, a.Cfg)
	res, err := a.DB.Exec(`UPDATE upload_policies SET max_file_mb = ?, max_batch_count = ?, max_daily_uploads = ?,
		max_daily_bytes = ?, total_quota_bytes = ?, storage_mode = ?, retention_days = ?, allowed_types = ?,
		allow_svg = ?, allow_api = ?, active = ?, allow_storage_selection = ? WHERE id = ?`, req.MaxFileMB, req.MaxBatchCount,
		req.MaxDailyUploads, req.MaxDailyBytes, req.TotalQuotaBytes, req.StorageMode, req.RetentionDays,
		req.AllowedTypes, req.AllowSVG, req.AllowAPI, req.Active, req.AllowStorageSelection, id)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to save upload policy")
		return
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		var exists int
		if err := a.DB.QueryRow(`SELECT COUNT(*) FROM upload_policies WHERE id=?`, id).Scan(&exists); err != nil || exists == 0 {
			utils.WriteError(w, http.StatusNotFound, "upload policy not found")
			return
		}
	}
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "upload_policy.updated", "upload_policy", strconv.FormatUint(uint64(id), 10), nil)
	utils.WriteJSON(w, http.StatusOK, map[string]any{"updated": true})
}

func (a *App) uploadPolicyFor(user *models.User, loggedIn bool) models.UploadPolicy {
	groupID := uint(0)
	if loggedIn && user != nil {
		groupID = user.GroupID
	} else if !loggedIn {
		groupID = uint(utils.SettingInt64(a.DB, "guest_group_id", 0, 0, 1<<32-1))
	}
	if groupID == 0 {
		return models.UploadPolicy{}
	}
	query := `SELECT policies.id, policies.name, policies.slug, policies.max_file_mb, policies.max_batch_count,
		policies.max_daily_uploads, policies.max_daily_bytes, policies.total_quota_bytes, policies.storage_mode,
		policies.retention_days, policies.allowed_types, policies.allow_svg, policies.allow_api, policies.active,
		policies.created_at, policies.updated_at, policies.allow_storage_selection FROM upload_policies policies
		JOIN user_groups user_group ON user_group.policy_id = policies.id WHERE user_group.id = ?`
	var policy models.UploadPolicy
	err := a.DB.QueryRow(query, groupID).Scan(&policy.ID, &policy.Name, &policy.Slug, &policy.MaxFileMB,
		&policy.MaxBatchCount, &policy.MaxDailyUploads, &policy.MaxDailyBytes, &policy.TotalQuotaBytes,
		&policy.StorageMode, &policy.RetentionDays, &policy.AllowedTypes, &policy.AllowSVG, &policy.AllowAPI,
		&policy.Active, &policy.CreatedAt, &policy.UpdatedAt, &policy.AllowStorageSelection)
	if err == nil {
		if loggedIn && user != nil && user.QuotaBytes != nil {
			policy.TotalQuotaBytes = *user.QuotaBytes
		}
		return policy
	}
	return models.UploadPolicy{}
}

func (a *App) consumePolicyUsage(policy models.UploadPolicy, user *models.User, ip string, size int64) error {
	if !policy.Active {
		return errors.New("upload policy is disabled")
	}
	subjectType, subjectKey := "ip", strings.TrimSpace(ip)
	if user != nil {
		subjectType, subjectKey = "user", strconv.FormatUint(uint64(user.ID), 10)
	}
	tx, err := a.DB.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT IGNORE INTO upload_usage_daily (usage_date, subject_type, subject_key, upload_count, upload_bytes)
		VALUES (CURDATE(), ?, ?, 0, 0)`, subjectType, subjectKey); err != nil {
		return err
	}
	var count, bytes int64
	if err := tx.QueryRow(`SELECT upload_count, upload_bytes FROM upload_usage_daily
		WHERE usage_date = CURDATE() AND subject_type = ? AND subject_key = ? FOR UPDATE`, subjectType, subjectKey).Scan(&count, &bytes); err != nil {
		return err
	}
	if policy.MaxDailyUploads > 0 && count >= policy.MaxDailyUploads {
		return errors.New("daily upload count limit reached")
	}
	if policy.MaxDailyBytes > 0 && bytes+size > policy.MaxDailyBytes {
		return errors.New("daily upload traffic limit reached")
	}
	if user != nil && policy.TotalQuotaBytes > 0 {
		var used int64
		if err := tx.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM images WHERE user_id = ? AND deleted_at IS NULL`, user.ID).Scan(&used); err != nil {
			return err
		}
		if used+size > policy.TotalQuotaBytes {
			return errors.New("storage quota exceeded")
		}
	}
	if _, err := tx.Exec(`UPDATE upload_usage_daily SET upload_count = upload_count + 1,
		upload_bytes = upload_bytes + ? WHERE usage_date = CURDATE() AND subject_type = ? AND subject_key = ?`, size, subjectType, subjectKey); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) validGroupID(requested uint, role string) (uint, error) {
	if requested == 0 {
		return 0, nil
	}
	var count int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM user_groups WHERE id = ?`, requested).Scan(&count); err != nil || count == 0 {
		return 0, errors.New("group not found")
	}
	return requested, nil
}

func clampInt64(value, minValue, maxValue int64) int64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func publicPolicy(policy models.UploadPolicy) map[string]any {
	return map[string]any{
		"name": policy.Name, "slug": policy.Slug, "max_file_mb": policy.MaxFileMB,
		"max_batch_count": policy.MaxBatchCount, "max_daily_uploads": policy.MaxDailyUploads,
		"max_daily_bytes": policy.MaxDailyBytes, "total_quota_bytes": policy.TotalQuotaBytes,
		"storage_mode": policy.StorageMode, "retention_days": policy.RetentionDays,
		"active": policy.Active, "allow_storage_selection": policy.AllowStorageSelection,
		"allowed_types": policy.AllowedTypes, "allow_svg": policy.AllowSVG, "allow_api": policy.AllowAPI,
	}
}

func (a *App) uploadUsageFor(user *models.User) map[string]any {
	var dailyCount, dailyBytes, totalBytes int64
	if user == nil {
		return map[string]any{"daily_count": 0, "daily_bytes": 0, "total_bytes": 0}
	}
	_ = a.DB.QueryRow(`SELECT upload_count, upload_bytes FROM upload_usage_daily
		WHERE usage_date = CURDATE() AND subject_type = 'user' AND subject_key = ?`, strconv.FormatUint(uint64(user.ID), 10)).Scan(&dailyCount, &dailyBytes)
	_ = a.DB.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM images WHERE user_id = ? AND deleted_at IS NULL`, user.ID).Scan(&totalBytes)
	return map[string]any{"daily_count": dailyCount, "daily_bytes": dailyBytes, "total_bytes": totalBytes}
}
