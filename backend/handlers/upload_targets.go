package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"zhuapic/models"
	"zhuapic/storage"
	"zhuapic/utils"
)

// uploadChoices returns the storages a caller may upload to and the one used
// when they do not choose. Admins may use every storage that accepts uploads;
// everyone else is limited to their group's allow-list.
func (a *App) uploadChoices(r *http.Request, policy models.UploadPolicy, admin bool) ([]storage.Storage, *storage.Storage, error) {
	candidates, err := a.Storage.UploadCandidates(r.Context())
	if err != nil {
		return nil, nil, err
	}
	byID := map[uint]storage.Storage{}
	for _, st := range candidates {
		byID[st.ID] = st
	}
	var def *storage.Storage
	if st, ok := byID[policy.DefaultStorageID]; ok && policy.DefaultStorageID != 0 {
		def = &st
	} else if st, ok := byID[a.Storage.DefaultID()]; ok {
		def = &st
	}
	allowed := []storage.Storage{}
	if admin {
		allowed = candidates
	} else {
		for _, id := range policy.StorageIDs {
			if st, ok := byID[id]; ok {
				allowed = append(allowed, st)
			}
		}
		if def != nil {
			found := false
			for _, st := range allowed {
				found = found || st.ID == def.ID
			}
			if !found {
				allowed = append([]storage.Storage{*def}, allowed...)
			}
		}
	}
	return allowed, def, nil
}

// resolveUploadStorage picks the destination for one upload. The form value
// "storage" (or the older "storage_mode") may be "default", an ID or "target:ID".
func (a *App) resolveUploadStorage(r *http.Request, policy models.UploadPolicy, loggedIn, admin bool) (*storage.Storage, error) {
	if !policy.Active {
		return nil, errors.New("没有有效的用户组或上传策略已停用")
	}
	allowed, def, err := a.uploadChoices(r, policy, admin)
	if err != nil {
		return nil, err
	}
	requested := strings.TrimSpace(r.FormValue("storage"))
	if requested == "" {
		requested = strings.TrimSpace(r.FormValue("storage_mode"))
	}
	requested = strings.TrimPrefix(requested, "target:")
	switch requested {
	case "", "default", "local", "s3", "mirror": // legacy values mean "default"
		if def == nil {
			return nil, errors.New("默认存储不可用，请联系管理员")
		}
		return a.Storage.Get(r.Context(), def.ID)
	}
	id, err := strconv.ParseUint(requested, 10, 64)
	if err != nil {
		return nil, errors.New("无效的存储选择")
	}
	for _, st := range allowed {
		if uint64(st.ID) == id {
			return a.Storage.Get(r.Context(), st.ID)
		}
	}
	return nil, errors.New("存储不可用或当前用户组无权使用，请重新选择")
}

// UploadTargets lists the storages offered on the upload page.
func (a *App) UploadTargets(w http.ResponseWriter, r *http.Request) {
	user, loggedIn := a.currentUser(r)
	if !loggedIn {
		user, loggedIn = a.apiTokenUser(r)
	}
	admin := loggedIn && user.Role == "admin"
	policy := a.uploadPolicyFor(user, loggedIn)
	allowed, def, err := a.uploadChoices(r, policy, admin)
	if err != nil {
		utils.WriteError(w, 500, "无法读取存储")
		return
	}
	out := []map[string]any{}
	defaultName := ""
	if def != nil {
		defaultName = def.Name
	}
	if policy.Active {
		for _, st := range allowed {
			out = append(out, map[string]any{"id": st.ID, "name": st.Name, "provider": st.Provider, "is_default": def != nil && st.ID == def.ID})
		}
	}
	response := map[string]any{"targets": out, "default_name": defaultName, "can_upload": policy.Active && def != nil, "allow_selection": len(out) > 1}
	if loggedIn {
		// Names for image filters; storages hold no sensitive information here.
		all := []map[string]any{}
		for id, item := range a.storageNames(r) {
			_ = id
			all = append(all, item)
		}
		response["storages"] = all
	}
	utils.WriteJSON(w, 200, response)
}
