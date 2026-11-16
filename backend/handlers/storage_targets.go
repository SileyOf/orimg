package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"zhuapic/storage"
	"zhuapic/utils"
)

func (a *App) StorageTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		targets, err := a.Coordinator.ListTargets(r.Context())
		if err != nil {
			utils.WriteError(w, 500, "无法读取存储目标")
			return
		}
		tasks, err := a.Coordinator.TargetFailures(r.Context())
		if err != nil {
			utils.WriteError(w, 500, "无法读取副本任务")
			return
		}
		utils.WriteJSON(w, 200, map[string]any{"targets": targets, "default_target_id": a.Coordinator.DefaultTargetID(), "tasks": tasks, "workers_disabled": a.Cfg.DisableBackgroundWork})
		return
	}
	var t storage.Target
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&t); err != nil {
		utils.WriteError(w, 400, "无效的存储配置")
		return
	}
	t.ID = 0
	if err := a.Coordinator.SaveTarget(r.Context(), &t); err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "storage_target.created", "storage_target", strconv.Itoa(int(t.ID)), map[string]any{"name": t.Name, "provider": t.Provider})
	utils.WriteJSON(w, 201, map[string]any{"id": t.ID})
}

func (a *App) StorageTarget(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		utils.WriteError(w, 400, "无效的存储 ID")
		return
	}
	if r.Method == http.MethodDelete {
		err = a.Coordinator.DeleteTarget(r.Context(), uint(id))
	} else {
		var t storage.Target
		if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&t); err == nil {
			t.ID = uint(id)
			err = a.Coordinator.SaveTarget(r.Context(), &t)
		}
	}
	if err != nil {
		utils.WriteError(w, 409, err.Error())
		return
	}
	user, _ := a.currentUser(r)
	action := "storage_target.updated"
	if r.Method == http.MethodDelete {
		action = "storage_target.deleted"
	}
	a.writeAudit(r, user, action, "storage_target", strconv.FormatUint(id, 10), nil)
	utils.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) StorageTargetAction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		utils.WriteError(w, 400, "无效的存储 ID")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		utils.WriteError(w, 400, "无效操作")
		return
	}
	var n int64
	switch req.Action {
	case "default":
		err = a.Coordinator.SetDefaultTarget(r.Context(), uint(id))
	case "probe":
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		err = a.Coordinator.ProbeTarget(ctx, uint(id))
	default:
		n, err = a.Coordinator.QueueTarget(r.Context(), uint(id), req.Action)
		if err == nil && req.Action == "verify" {
			t, loadErr := a.Coordinator.Target(r.Context(), uint(id))
			if loadErr != nil {
				err = loadErr
			} else if t.LegacyMode != "" {
				user, _ := a.currentUser(r)
				_, err = a.Coordinator.CreateVerifyJob(r.Context(), t.LegacyMode, user.ID)
			}
		}
	}
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "storage_target."+req.Action, "storage_target", strconv.FormatUint(id, 10), map[string]any{"queued": n})
	utils.WriteJSON(w, 200, map[string]any{"ok": true, "queued": n})
}
