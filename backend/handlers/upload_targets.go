package handlers

import (
	"errors"
	"net/http"
	"strings"

	"zhuapic/models"
	"zhuapic/storage"
	"zhuapic/utils"
)

func (a *App) UploadTargets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	user, loggedIn := a.currentUser(r)
	if r.Header.Get("Authorization") != "" && !loggedIn {
		utils.WriteError(w, 401, "登录已过期")
		return
	}
	policy := a.uploadPolicyFor(user, loggedIn)
	targets, err := a.Coordinator.UploadTargets(r.Context())
	if err != nil {
		utils.WriteError(w, 503, "暂时无法读取上传位置")
		return
	}
	admin := loggedIn && user.Role == "admin"
	defaultID := a.Coordinator.DefaultTargetID()
	defaultName := "默认存储"
	if policy.StorageMode == "mirror" || defaultID == 0 {
		defaultName = "Local + S3"
	}
	out := []map[string]any{}
	for _, t := range targets {
		isDefault := t.ID == defaultID
		if policy.StorageMode != "default" && policy.StorageMode != "" {
			isDefault = policy.StorageMode == storage.TargetSelection(t.ID) || policy.StorageMode == t.LegacyMode
		}
		if isDefault {
			defaultName = t.Name
		}
		if policy.Active && storage.CanSelectTarget(&t, defaultID, policy.StorageMode, loggedIn, admin, policy.AllowStorageSelection) {
			out = append(out, map[string]any{"id": t.ID, "name": t.Name, "provider": t.Provider, "is_default": isDefault})
		}
	}
	utils.WriteJSON(w, 200, map[string]any{"targets": out, "default_name": defaultName, "can_upload": policy.Active, "allow_selection": policy.AllowStorageSelection})
}

func (a *App) resolveUploadTarget(r *http.Request, policy models.UploadPolicy, loggedIn, admin bool) (*storage.Target, string, error) {
	if !policy.Active {
		return nil, "", errors.New("没有有效的用户组或上传策略已停用")
	}
	mode := strings.ToLower(utils.GetSetting(a.DB, "storage_mode", a.Cfg.StorageMode))
	if policy.StorageMode != "" && policy.StorageMode != "default" {
		mode = policy.StorageMode
	}
	requested := strings.TrimSpace(r.FormValue("storage_mode"))
	if requested == "" {
		requested = "default"
	}
	defaultID := a.Coordinator.DefaultTargetID()
	var id uint
	var err error
	if strings.HasPrefix(requested, "target:") {
		id, err = storage.ParseTargetSelection(requested)
	} else if requested == "default" {
		if strings.HasPrefix(mode, "target:") {
			id, err = storage.ParseTargetSelection(mode)
		} else if policy.StorageMode == "default" || policy.StorageMode == "" {
			id = defaultID
		}
	} else if requested == "local" || requested == "s3" || requested == "mirror" {
		if requested == "mirror" && mode != "mirror" {
			return nil, "", errors.New("用户组不允许选择此存储")
		}
		mode = requested
	} else {
		return nil, "", errors.New("无效的存储选择")
	}
	if err != nil {
		return nil, "", err
	}
	if id == 0 && (mode == "local" || mode == "s3") {
		err = a.DB.QueryRowContext(r.Context(), "SELECT id FROM storage_targets WHERE legacy_mode=?", mode).Scan(&id)
		if err != nil {
			return nil, "", errors.New("存储位置不可用")
		}
	}
	if id != 0 {
		t, err := a.Coordinator.Target(r.Context(), id)
		if err != nil || !storage.CanSelectTarget(t, defaultID, policy.StorageMode, loggedIn, admin, policy.AllowStorageSelection) {
			return nil, "", errors.New("存储不可用或当前用户组无权使用，请重新选择")
		}
		return t, t.Mode(), nil
	}
	if mode != "mirror" {
		return nil, "", errors.New("默认存储未配置")
	}
	// Mirror is retained for older fixed policies, but no longer offered as a storage location.
	for _, legacy := range []string{"local", "s3"} {
		var enabled bool
		if err := a.DB.QueryRowContext(r.Context(), "SELECT enabled FROM storage_targets WHERE legacy_mode=?", legacy).Scan(&enabled); err != nil || !enabled {
			return nil, "", errors.New("双副本存储不可用")
		}
	}
	return nil, mode, nil
}
