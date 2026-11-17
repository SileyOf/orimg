package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"zhuapic/models"
	"zhuapic/utils"
)

type updatePolicyRequest struct {
	MaxFileMB        int64  `json:"max_file_mb"`
	MaxBatchCount    int    `json:"max_batch_count"`
	MaxDailyUploads  int64  `json:"max_daily_uploads"`
	MaxDailyBytes    int64  `json:"max_daily_bytes"`
	TotalQuotaBytes  int64  `json:"total_quota_bytes"`
	DefaultStorageID uint   `json:"default_storage_id"`
	StorageIDs       []uint `json:"storage_ids"`
	RetentionDays    int    `json:"retention_days"`
	AllowedTypes     string `json:"allowed_types"`
	AllowSVG         bool   `json:"allow_svg"`
	AllowAPI         bool   `json:"allow_api"`
	Active           bool   `json:"active"`
}

type createGroupRequest struct {
	Name            string `json:"name"`
	CopyFromGroupID uint   `json:"copy_from_group_id"`
}

type updateGroupRequest struct {
	Name string `json:"name"`
}

const groupColumns = `g.id, g.name, g.slug, g.is_system, g.max_file_mb, g.max_batch_count, g.max_daily_uploads, g.max_daily_bytes,
	g.total_quota_bytes, COALESCE(g.default_storage_id, 0), g.retention_days, COALESCE(g.allowed_types, ''), g.allow_svg, g.allow_api,
	g.active, g.created_at, g.updated_at`

func scanGroup(row interface{ Scan(...any) error }, group *models.UserGroup) error {
	p := &group.Policy
	err := row.Scan(&group.ID, &group.Name, &group.Slug, &group.IsSystem, &p.MaxFileMB, &p.MaxBatchCount, &p.MaxDailyUploads, &p.MaxDailyBytes,
		&p.TotalQuotaBytes, &p.DefaultStorageID, &p.RetentionDays, &p.AllowedTypes, &p.AllowSVG, &p.AllowAPI, &p.Active, &p.CreatedAt, &p.UpdatedAt)
	p.ID, p.Name, p.Slug, group.PolicyID = group.ID, group.Name, group.Slug, group.ID
	p.StorageIDs = []uint{}
	return err
}

func (a *App) groupStorages(ctx context.Context) (map[uint][]uint, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT gs.group_id, gs.storage_id FROM group_storages gs JOIN storages s ON s.id = gs.storage_id ORDER BY gs.storage_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint][]uint{}
	for rows.Next() {
		var group, storageID uint
		if err := rows.Scan(&group, &storageID); err != nil {
			return nil, err
		}
		out[group] = append(out[group], storageID)
	}
	return out, rows.Err()
}

func (a *App) AdminGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listGroups(w, r)
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

func (a *App) listGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT `+groupColumns+`, (SELECT COUNT(*) FROM users u WHERE u.group_id = g.id) FROM user_groups g ORDER BY g.id`)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load user groups")
		return
	}
	groups := []models.UserGroup{}
	for rows.Next() {
		var group models.UserGroup
		var members int64
		if err := scanGroup(scanTail{rows, &members}, &group); err != nil {
			rows.Close()
			utils.WriteError(w, http.StatusInternalServerError, "failed to read user groups")
			return
		}
		group.MemberCount = members
		groups = append(groups, group)
	}
	rows.Close()
	allowed, err := a.groupStorages(r.Context())
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to read group storages")
		return
	}
	for i := range groups {
		if ids, ok := allowed[groups[i].ID]; ok {
			groups[i].Policy.StorageIDs = ids
		}
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"groups": groups, "guest_group_id": utils.SettingInt64(a.DB, "guest_group_id", 0, 0, 1<<32-1)})
}

// scanTail appends extra destinations after the group columns.
type scanTail struct {
	row   interface{ Scan(...any) error }
	extra *int64
}

func (s scanTail) Scan(dest ...any) error { return s.row.Scan(append(dest, s.extra)...) }

func (a *App) createGroup(w http.ResponseWriter, r *http.Request) {
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
	if req.CopyFromGroupID == 0 {
		res, err = tx.Exec(`INSERT INTO user_groups (name, slug, is_system, allowed_types, total_quota_bytes) VALUES (?, ?, 0, ?, ?)`,
			name, slug, "jpg,jpeg,png,gif,webp,avif", int64(5*1024*1024*1024))
	} else {
		res, err = tx.Exec(`INSERT INTO user_groups (name, slug, is_system, max_file_mb, max_batch_count, max_daily_uploads, max_daily_bytes,
			total_quota_bytes, default_storage_id, retention_days, allowed_types, allow_svg, allow_api, active)
			SELECT ?, ?, 0, max_file_mb, max_batch_count, max_daily_uploads, max_daily_bytes, total_quota_bytes, default_storage_id,
			retention_days, allowed_types, allow_svg, allow_api, active FROM user_groups WHERE id = ?`, name, slug, req.CopyFromGroupID)
	}
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create user group")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		utils.WriteError(w, http.StatusBadRequest, "source user group not found")
		return
	}
	groupID, _ := res.LastInsertId()
	if req.CopyFromGroupID != 0 {
		if _, err = tx.Exec(`INSERT INTO group_storages (group_id, storage_id) SELECT ?, storage_id FROM group_storages WHERE group_id = ?`, groupID, req.CopyFromGroupID); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to copy group storages")
			return
		}
	}
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
	res, err := a.DB.Exec(`UPDATE user_groups SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to update user group")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if a.DB.QueryRow(`SELECT COUNT(*) FROM user_groups WHERE id = ?`, id).Scan(&exists); exists == 0 {
			utils.WriteError(w, http.StatusNotFound, "user group not found")
			return
		}
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
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to delete user group")
		return
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRow(`SELECT name FROM user_groups WHERE id = ? FOR UPDATE`, id).Scan(&name); err != nil {
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
	if _, err := tx.Exec("UPDATE site_settings SET value='0' WHERE `key`='guest_group_id' AND value=?", strconv.FormatUint(uint64(id), 10)); err != nil {
		log.Printf("delete user group %d: %v", id, err)
		utils.WriteError(w, http.StatusInternalServerError, "failed to delete user group")
		return
	}
	for _, q := range []string{
		`UPDATE users SET group_id=NULL WHERE group_id=?`,
		`DELETE FROM group_storages WHERE group_id = ?`,
		`DELETE FROM user_groups WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			log.Printf("delete user group %d: %v", id, err)
			utils.WriteError(w, http.StatusInternalServerError, "failed to delete user group")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to delete user group")
		return
	}
	utils.InvalidateSettings(a.DB)
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "user_group.deleted", "user_group", strconv.FormatUint(uint64(id), 10), map[string]any{"name": name, "unassigned_users": members})
	utils.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// UpdateUploadPolicy saves a group's upload limits and storage access. The
// path ID is the group ID (policies are part of groups since schema 10).
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
	req.AllowedTypes = sanitizeSetting("allowed_types", req.AllowedTypes, a.Cfg)
	unlock, err := a.Storage.LockConfig(r.Context())
	if err != nil {
		utils.WriteError(w, 409, err.Error())
		return
	}
	defer unlock()
	candidates, err := a.Storage.UploadCandidates(r.Context())
	if err != nil {
		utils.WriteError(w, 500, "无法读取存储")
		return
	}
	usable := map[uint]bool{}
	for _, st := range candidates {
		usable[st.ID] = true
	}
	if req.DefaultStorageID != 0 && !usable[req.DefaultStorageID] {
		utils.WriteError(w, 400, "默认上传位置必须是已启用且允许上传的存储")
		return
	}
	allowed := []uint{}
	seen := map[uint]bool{}
	for _, sid := range req.StorageIDs {
		if !seen[sid] {
			if !usable[sid] {
				utils.WriteError(w, 400, "所选存储未启用或不允许上传")
				return
			}
			seen[sid] = true
			allowed = append(allowed, sid)
		}
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, 500, "failed to save upload policy")
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE user_groups SET max_file_mb = ?, max_batch_count = ?, max_daily_uploads = ?, max_daily_bytes = ?,
		total_quota_bytes = ?, default_storage_id = NULLIF(?, 0), retention_days = ?, allowed_types = ?, allow_svg = ?, allow_api = ?, active = ?
		WHERE id = ?`, req.MaxFileMB, req.MaxBatchCount, req.MaxDailyUploads, req.MaxDailyBytes, req.TotalQuotaBytes, req.DefaultStorageID,
		req.RetentionDays, req.AllowedTypes, req.AllowSVG, req.AllowAPI, req.Active, id)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to save upload policy")
		return
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM user_groups WHERE id=?`, id).Scan(&exists); err != nil || exists == 0 {
			utils.WriteError(w, http.StatusNotFound, "user group not found")
			return
		}
	}
	if _, err := tx.Exec(`DELETE FROM group_storages WHERE group_id = ?`, id); err != nil {
		utils.WriteError(w, 500, "failed to save group storages")
		return
	}
	for _, sid := range allowed {
		if _, err := tx.Exec(`INSERT INTO group_storages (group_id, storage_id) VALUES (?, ?)`, id, sid); err != nil {
			utils.WriteError(w, 500, "failed to save group storages")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		utils.WriteError(w, 500, "failed to save upload policy")
		return
	}
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "upload_policy.updated", "user_group", strconv.FormatUint(uint64(id), 10), map[string]any{"storages": allowed})
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
	var group models.UserGroup
	if err := scanGroup(a.DB.QueryRow(`SELECT `+groupColumns+` FROM user_groups g WHERE g.id = ?`, groupID), &group); err != nil {
		return models.UploadPolicy{}
	}
	policy := group.Policy
	rows, err := a.DB.Query(`SELECT gs.storage_id FROM group_storages gs JOIN storages s ON s.id = gs.storage_id WHERE gs.group_id = ? ORDER BY gs.storage_id`, groupID)
	if err == nil {
		for rows.Next() {
			var sid uint
			if rows.Scan(&sid) == nil {
				policy.StorageIDs = append(policy.StorageIDs, sid)
			}
		}
		rows.Close()
	}
	if loggedIn && user != nil && user.QuotaBytes != nil {
		policy.TotalQuotaBytes = *user.QuotaBytes
	}
	return policy
}

func publicPolicy(policy models.UploadPolicy) map[string]any {
	return map[string]any{
		"name": policy.Name, "slug": policy.Slug, "max_file_mb": policy.MaxFileMB,
		"max_batch_count": policy.MaxBatchCount, "max_daily_uploads": policy.MaxDailyUploads,
		"max_daily_bytes": policy.MaxDailyBytes, "total_quota_bytes": policy.TotalQuotaBytes,
		"default_storage_id": policy.DefaultStorageID, "storage_ids": policy.StorageIDs,
		"retention_days": policy.RetentionDays, "active": policy.Active,
		"allowed_types": policy.AllowedTypes, "allow_svg": policy.AllowSVG, "allow_api": policy.AllowAPI,
	}
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
