package handlers

import (
	"net/http"
	"time"

	"zhuapic/utils"
)

func (a *App) UserDashboard(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	metrics := struct {
		Images       int64 `json:"images"`
		ImageBytes   int64 `json:"image_bytes"`
		UploadsToday int64 `json:"uploads_today"`
		BytesToday   int64 `json:"bytes_today"`
		LocalImages  int64 `json:"local_images"`
		S3Images     int64 `json:"s3_images"`
		MirrorImages int64 `json:"mirror_images"`
	}{}
	if err := a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(size), 0),
		COALESCE(SUM(created_at >= CURDATE()), 0), COALESCE(SUM(IF(created_at >= CURDATE(), size, 0)), 0),
		COALESCE(SUM(locals > 0 AND copies = 1), 0), COALESCE(SUM(locals = 0 AND copies > 0), 0),
		COALESCE(SUM(copies > 1), 0)
		FROM (SELECT size, created_at,
			(SELECT COUNT(*) FROM image_copies c WHERE c.image_id = images.id AND c.state = 'ready') AS copies,
			(SELECT COUNT(*) FROM image_copies c JOIN storages s ON s.id = c.storage_id WHERE c.image_id = images.id AND c.state = 'ready' AND s.provider = 'local') AS locals
			FROM images WHERE user_id = ? AND deleted_at IS NULL) inventory`, user.ID).Scan(&metrics.Images, &metrics.ImageBytes,
		&metrics.UploadsToday, &metrics.BytesToday, &metrics.LocalImages, &metrics.S3Images, &metrics.MirrorImages); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load user dashboard")
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT DATE(created_at), COUNT(*), COALESCE(SUM(size), 0)
		FROM images WHERE user_id = ? AND deleted_at IS NULL AND created_at >= DATE_SUB(CURDATE(), INTERVAL 13 DAY)
		GROUP BY DATE(created_at) ORDER BY DATE(created_at) ASC`, user.ID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load user activity")
		return
	}
	defer rows.Close()
	activity := make([]map[string]any, 0, 14)
	for rows.Next() {
		var day time.Time
		var count, bytes int64
		if err := rows.Scan(&day, &count, &bytes); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to read user activity")
			return
		}
		activity = append(activity, map[string]any{"date": day.Format("01-02"), "count": count, "bytes": bytes})
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"metrics": metrics, "activity": activity})
}
