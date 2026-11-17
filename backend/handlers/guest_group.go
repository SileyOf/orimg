package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"zhuapic/utils"
)

func (a *App) UpdateGuestGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID uint `json:"group_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, 400, "invalid group")
		return
	}
	unlock, err := a.Storage.LockConfig(r.Context())
	if err != nil {
		utils.WriteError(w, 409, err.Error())
		return
	}
	defer unlock()
	if _, err := a.validGroupID(req.GroupID, ""); err != nil {
		utils.WriteError(w, 400, "用户组不存在")
		return
	}
	value := strconv.FormatUint(uint64(req.GroupID), 10)
	if err := utils.UpsertSetting(a.DB, "guest_group_id", value); err != nil {
		utils.WriteError(w, 500, "保存失败")
		return
	}
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "guest_group.updated", "user_group", value, nil)
	utils.WriteJSON(w, 200, map[string]bool{"updated": true})
}
