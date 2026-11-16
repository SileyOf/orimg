package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"zhuapic/utils"
)

type bulkImagesRequest struct {
	Action string `json:"action"`
	IDs    []uint `json:"ids"`
}

func (a *App) BulkImages(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req bulkImagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid bulk image payload")
		return
	}
	if req.Action != "delete" {
		utils.WriteError(w, http.StatusBadRequest, "unsupported bulk action")
		return
	}
	unique := make([]uint, 0, len(req.IDs))
	seen := map[uint]bool{}
	for _, id := range req.IDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 || len(unique) > 100 {
		utils.WriteError(w, http.StatusBadRequest, "select 1 to 100 images")
		return
	}
	placeholders := make([]string, len(unique))
	args := make([]any, 0, len(unique)+2)
	for index, id := range unique {
		placeholders[index] = "?"
		args = append(args, id)
	}
	days := utils.SettingInt64(a.DB, "recycle_bin_days", 7, 1, 30)
	query := `UPDATE images SET deleted_at = NOW(), delete_after = DATE_ADD(NOW(), INTERVAL ? DAY)
		WHERE deleted_at IS NULL AND id IN (` + strings.Join(placeholders, ",") + `)`
	args = append([]any{days}, args...)
	if user.Role != "admin" {
		query += ` AND user_id = ?`
		args = append(args, user.ID)
	}
	res, err := a.DB.ExecContext(r.Context(), query, args...)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to move images to recycle bin")
		return
	}
	affected, _ := res.RowsAffected()
	a.writeAudit(r, user, "images.bulk_deleted", "image", "bulk", map[string]any{
		"requested": len(unique), "affected": affected,
	})
	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"deleted": affected, "purge_after_days": days,
	})
}
