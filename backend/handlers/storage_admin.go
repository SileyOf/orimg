package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"zhuapic/models"
	"zhuapic/utils"
)

type storageJobRequest struct {
	Type       string `json:"type"`
	SourceMode string `json:"source_mode"`
	TargetMode string `json:"target_mode"`
	Action     string `json:"action"`
}

func (a *App) StorageCache(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	stats := a.Store.CacheInventory(r.Context(), a.storageOptions(), utils.SettingCacheTTL(a.DB, a.Cfg.CacheTTL))
	utils.WriteJSON(w, 200, map[string]any{"cache": stats, "cleanup_enabled": !a.Cfg.DisableBackgroundWork, "cleanup_interval_seconds": int64(a.Cfg.CleanupInterval / time.Second)})
}

func (a *App) StorageOverview(w http.ResponseWriter, r *http.Request) {
	if a.Coordinator == nil {
		utils.WriteError(w, http.StatusServiceUnavailable, "storage coordinator unavailable")
		return
	}
	stats, err := a.Coordinator.Stats(r.Context())
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load storage stats")
		return
	}
	jobs, err := a.Coordinator.ListJobs(r.Context(), 30)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load storage jobs")
		return
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"stats": stats, "jobs": jobs})
}

func (a *App) CreateStorageJob(w http.ResponseWriter, r *http.Request) {
	if a.Coordinator == nil {
		utils.WriteError(w, http.StatusServiceUnavailable, "storage coordinator unavailable")
		return
	}
	var req storageJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid storage job")
		return
	}
	user, _ := a.currentUser(r)
	var id uint
	var err error
	switch req.Type {
	case "migrate":
		id, err = a.Coordinator.CreateMigrationJob(r.Context(), req.SourceMode, req.TargetMode, user.ID)
	case "verify":
		mode := req.SourceMode
		if mode == "" {
			mode = "all"
		}
		id, err = a.Coordinator.CreateVerifyJob(r.Context(), mode, user.ID)
	default:
		err = errors.New("unsupported storage job")
	}
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.writeAudit(r, user, "storage_job.created", "storage_job", strconv.FormatUint(uint64(id), 10), map[string]any{"type": req.Type, "source": req.SourceMode, "target": req.TargetMode})
	utils.WriteJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *App) ControlStorageJob(w http.ResponseWriter, r *http.Request) {
	if a.Coordinator == nil {
		utils.WriteError(w, http.StatusServiceUnavailable, "storage coordinator unavailable")
		return
	}
	id64, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id64 == 0 {
		utils.WriteError(w, http.StatusBadRequest, "invalid storage job id")
		return
	}
	var req storageJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid storage job action")
		return
	}
	if err := a.Coordinator.ControlJob(r.Context(), uint(id64), req.Action); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "storage_job.controlled", "storage_job", strconv.FormatUint(id64, 10), map[string]any{"action": req.Action})
	utils.WriteJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (a *App) CreateDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	if a.Cfg.DisableBackgroundWork {
		utils.WriteError(w, http.StatusConflict, "后台工作器已停用，不能创建备份任务")
		return
	}
	if a.Coordinator == nil {
		utils.WriteError(w, http.StatusServiceUnavailable, "storage coordinator unavailable")
		return
	}
	user, _ := a.currentUser(r)
	id, err := a.Coordinator.CreateDatabaseBackupJob(r.Context(), user.ID)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.writeAudit(r, user, "database_backup.created", "storage_job", strconv.FormatUint(uint64(id), 10), nil)
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
	out := make([]map[string]any, 0, len(images))
	for i := range images {
		item := a.imageResponse(&images[i], baseURL)
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
	if a.Coordinator == nil {
		utils.WriteError(w, http.StatusServiceUnavailable, "storage coordinator unavailable")
		return
	}
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
	if err := a.Coordinator.PurgeImage(r.Context(), id); err != nil {
		utils.WriteJSON(w, http.StatusAccepted, map[string]any{"purging": true, "message": "storage deletion will retry automatically"})
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "image.purged", "image", strconv.FormatUint(uint64(id), 10), nil)
	utils.WriteJSON(w, http.StatusOK, map[string]bool{"purged": true})
}
