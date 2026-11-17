package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"zhuapic/models"
	"zhuapic/storage"
	"zhuapic/utils"
)

func (a *App) StorageCache(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	stats := a.Storage.Manager().CacheInventory(r.Context(), a.storageOptions(), utils.SettingCacheTTL(a.DB, a.Cfg.CacheTTL))
	utils.WriteJSON(w, 200, map[string]any{"cache": stats, "cleanup_enabled": !a.Cfg.DisableBackgroundWork, "cleanup_interval_seconds": int64(a.Cfg.CleanupInterval / time.Second)})
}

func pathID(r *http.Request) (uint, bool) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	return uint(id), err == nil && id > 0
}

// Storages lists storages (GET) or creates one (POST).
func (a *App) Storages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == http.MethodGet {
		storages, err := a.Storage.List(r.Context())
		if err != nil {
			utils.WriteError(w, 500, "无法读取存储")
			return
		}
		failures, err := a.Storage.Failures(r.Context(), 50)
		if err != nil {
			utils.WriteError(w, 500, "无法读取副本状态")
			return
		}
		stats, _ := a.Storage.Stats(r.Context())
		utils.WriteJSON(w, 200, map[string]any{"storages": storages, "default_storage_id": a.Storage.DefaultID(), "failures": failures,
			"stats": stats, "workers_disabled": a.Cfg.DisableBackgroundWork,
			"db_backup_storage_id": utils.SettingInt64(a.DB, "db_backup_storage_id", 0, 0, 1<<31)})
		return
	}
	var st storage.Storage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&st); err != nil {
		utils.WriteError(w, 400, "无效的存储配置")
		return
	}
	st.ID = 0
	if err := a.Storage.Save(r.Context(), &st); err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "storage.created", "storage", strconv.Itoa(int(st.ID)), map[string]any{"name": st.Name, "provider": st.Provider})
	utils.WriteJSON(w, 201, map[string]any{"id": st.ID})
}

// UpdateStorage saves an existing storage.
func (a *App) UpdateStorage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		utils.WriteError(w, 400, "无效的存储 ID")
		return
	}
	var st storage.Storage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&st); err != nil {
		utils.WriteError(w, 400, "无效的存储配置")
		return
	}
	st.ID = id
	if err := a.Storage.Save(r.Context(), &st); err != nil {
		utils.WriteError(w, 409, err.Error())
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "storage.updated", "storage", strconv.FormatUint(uint64(id), 10), nil)
	utils.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// StorageAction runs probe, default, pause, resume, retry and verify.
func (a *App) StorageAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		utils.WriteError(w, 400, "无效的存储 ID")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		utils.WriteError(w, 400, "无效操作")
		return
	}
	user, _ := a.currentUser(r)
	result := map[string]any{"ok": true}
	var err error
	switch req.Action {
	case "probe":
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		err = a.Storage.Probe(ctx, id)
		cancel()
	case "default":
		err = a.Storage.SetDefault(r.Context(), id)
	case "pause":
		err = a.Storage.SetStatus(r.Context(), id, storage.StatusPaused)
	case "resume":
		err = a.Storage.SetStatus(r.Context(), id, storage.StatusActive)
	case "retry":
		var n int64
		n, err = a.Storage.RetryFailed(r.Context(), id)
		result["queued"] = n
	case "verify":
		var op uint
		op, err = a.Storage.StartVerify(r.Context(), id, user.ID)
		result["operation_id"] = op
	default:
		utils.WriteError(w, 400, "无效操作")
		return
	}
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	a.writeAudit(r, user, "storage."+req.Action, "storage", strconv.FormatUint(uint64(id), 10), nil)
	utils.WriteJSON(w, 200, result)
}

// StorageRemoval shows the impact of removing a storage (GET) or removes it
// (POST) by migrating images out ("drain") or immediately ("force").
func (a *App) StorageRemoval(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		utils.WriteError(w, 400, "无效的存储 ID")
		return
	}
	if r.Method == http.MethodGet {
		plan, err := a.Storage.RemovalPlan(r.Context(), id)
		if err != nil {
			utils.WriteError(w, 404, err.Error())
			return
		}
		utils.WriteJSON(w, 200, plan)
		return
	}
	var req struct {
		Mode          string `json:"mode"`
		DestinationID uint   `json:"destination_id"`
		DeleteObjects bool   `json:"delete_objects"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		utils.WriteError(w, 400, "无效请求")
		return
	}
	user, _ := a.currentUser(r)
	switch req.Mode {
	case "drain":
		op, err := a.Storage.StartDrain(r.Context(), id, req.DestinationID, req.DeleteObjects, user.ID)
		if err != nil {
			utils.WriteError(w, 409, err.Error())
			return
		}
		a.writeAudit(r, user, "storage.drain_started", "storage", strconv.FormatUint(uint64(id), 10), map[string]any{"destination": req.DestinationID, "delete_objects": req.DeleteObjects})
		utils.WriteJSON(w, 202, map[string]any{"operation_id": op})
	case "force":
		lost, err := a.Storage.ForceRemove(r.Context(), id, user.ID)
		if err != nil {
			utils.WriteError(w, 409, err.Error())
			return
		}
		a.writeAudit(r, user, "storage.force_removed", "storage", strconv.FormatUint(uint64(id), 10), map[string]any{"recycled_images": lost})
		utils.WriteJSON(w, 200, map[string]any{"removed": true, "recycled_images": lost})
	default:
		utils.WriteError(w, 400, "无效的移除方式")
	}
}

// Operations lists recent long-running operations.
func (a *App) Operations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	ops, err := a.Storage.ListOperations(r.Context(), 30)
	if err != nil {
		utils.WriteError(w, 500, "无法读取任务")
		return
	}
	utils.WriteJSON(w, 200, map[string]any{"operations": ops, "workers_disabled": a.Cfg.DisableBackgroundWork})
}

// ControlOperation cancels an operation.
func (a *App) ControlOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		utils.WriteError(w, 400, "无效的任务 ID")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || req.Action != "cancel" {
		utils.WriteError(w, 400, "无效操作")
		return
	}
	if err := a.Storage.CancelOperation(r.Context(), id); err != nil {
		utils.WriteError(w, 409, err.Error())
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "operation.cancelled", "operation", strconv.FormatUint(uint64(id), 10), nil)
	utils.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) CreateDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	if a.Cfg.DisableBackgroundWork {
		utils.WriteError(w, http.StatusConflict, "后台工作器已停用，不能创建备份任务")
		return
	}
	user, _ := a.currentUser(r)
	id, err := a.Storage.CreateDatabaseBackup(r.Context(), user.ID)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.writeAudit(r, user, "database_backup.created", "operation", strconv.FormatUint(uint64(id), 10), nil)
	utils.WriteJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *App) ListTrash(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(imageSelectSQL() + ` WHERE images.deleted_at IS NOT NULL ORDER BY images.deleted_at DESC LIMIT 200`)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load recycle bin")
		return
	}
	defer rows.Close()
	images := []models.Image{}
	for rows.Next() {
		image, err := scanImage(rows)
		if err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to read recycle bin")
			return
		}
		images = append(images, *image)
	}
	baseURL := a.requestBaseURL(r)
	names := a.storageNames(r)
	out := make([]map[string]any, 0, len(images))
	for i := range images {
		item := a.imageResponse(&images[i], baseURL, names)
		var deletedAt, deleteAfter sql.NullTime
		_ = a.DB.QueryRow(`SELECT deleted_at, delete_after FROM images WHERE id = ?`, images[i].ID).Scan(&deletedAt, &deleteAfter)
		if deletedAt.Valid {
			item["deleted_at"] = deletedAt.Time.Format(time.RFC3339)
		}
		if deleteAfter.Valid {
			item["delete_after"] = deleteAfter.Time.Format(time.RFC3339)
		}
		out = append(out, item)
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"images": out})
}

func (a *App) RestoreImage(w http.ResponseWriter, r *http.Request) {
	id, err := utils.UserIDFromString(r.PathValue("id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid image id")
		return
	}
	res, err := a.DB.Exec(`UPDATE images SET deleted_at = NULL, delete_after = NULL, expires_at = NULL WHERE id = ? AND deleted_at IS NOT NULL`, id)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to restore image")
		return
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		utils.WriteError(w, http.StatusNotFound, "image not found in recycle bin")
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "image.restored", "image", strconv.FormatUint(uint64(id), 10), nil)
	utils.WriteJSON(w, http.StatusOK, map[string]bool{"restored": true})
}

func (a *App) PurgeImage(w http.ResponseWriter, r *http.Request) {
	id, err := utils.UserIDFromString(r.PathValue("id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid image id")
		return
	}
	res, err := a.DB.Exec(`UPDATE images SET delete_after = NOW() WHERE id = ? AND deleted_at IS NOT NULL`, id)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to schedule purge")
		return
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		utils.WriteError(w, http.StatusNotFound, "image not found in recycle bin")
		return
	}
	// Records are removed now; files on storages are deleted in the background.
	if err := a.Storage.PurgeImage(r.Context(), id); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to purge image")
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "image.purged", "image", strconv.FormatUint(uint64(id), 10), nil)
	utils.WriteJSON(w, http.StatusOK, map[string]bool{"purged": true})
}
