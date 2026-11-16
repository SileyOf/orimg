package handlers

import (
	"net/http"
	"time"

	"zhuapic/config"
	"zhuapic/utils"
)

func (a *App) AdminDashboard(w http.ResponseWriter, r *http.Request) {
	type metrics struct {
		Images           int64 `json:"images"`
		ImageBytes       int64 `json:"image_bytes"`
		UploadsToday     int64 `json:"uploads_today"`
		UploadBytesToday int64 `json:"upload_bytes_today"`
		Users            int64 `json:"users"`
		ActiveUsers      int64 `json:"active_users"`
		PendingReplicas  int64 `json:"pending_replicas"`
		FailedReplicas   int64 `json:"failed_replicas"`
		QueuedJobs       int64 `json:"queued_jobs"`
		RunningJobs      int64 `json:"running_jobs"`
		DBOpen           int   `json:"db_open"`
		DBInUse          int   `json:"db_in_use"`
		DBIdle           int   `json:"db_idle"`
		DBWaitCount      int64 `json:"db_wait_count"`
	}

	var result metrics
	queries := []struct {
		query string
		dest  []any
	}{
		{`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM images WHERE deleted_at IS NULL`, []any{&result.Images, &result.ImageBytes}},
		{`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM images WHERE deleted_at IS NULL AND created_at >= CURDATE()`, []any{&result.UploadsToday, &result.UploadBytesToday}},
		{`SELECT COUNT(*), COALESCE(SUM(active = 1), 0) FROM users`, []any{&result.Users, &result.ActiveUsers}},
		{`SELECT COALESCE(SUM(status IN ('pending', 'syncing', 'verifying')), 0), COALESCE(SUM(status IN ('failed','verify_failed')), 0) FROM (SELECT status FROM image_replicas UNION ALL SELECT status FROM storage_target_replicas) replicas`, []any{&result.PendingReplicas, &result.FailedReplicas}},
		{`SELECT COALESCE(SUM(status IN ('queued', 'paused')), 0), COALESCE(SUM(status = 'running'), 0) FROM storage_jobs`, []any{&result.QueuedJobs, &result.RunningJobs}},
	}
	dbStats := a.DB.Stats()
	result.DBOpen = dbStats.OpenConnections
	result.DBInUse = dbStats.InUse
	result.DBIdle = dbStats.Idle
	result.DBWaitCount = dbStats.WaitCount
	for _, item := range queries {
		if err := a.DB.QueryRowContext(r.Context(), item.query).Scan(item.dest...); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to load dashboard metrics")
			return
		}
	}

	rows, err := a.DB.QueryContext(r.Context(), `SELECT DATE(created_at), COUNT(*), COALESCE(SUM(size), 0)
		FROM images WHERE deleted_at IS NULL AND created_at >= DATE_SUB(CURDATE(), INTERVAL 6 DAY)
		GROUP BY DATE(created_at) ORDER BY DATE(created_at) ASC`)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load upload trend")
		return
	}
	defer rows.Close()
	activity := make([]map[string]any, 0, 7)
	for rows.Next() {
		var day time.Time
		var count, bytes int64
		if err := rows.Scan(&day, &count, &bytes); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to read upload trend")
			return
		}
		activity = append(activity, map[string]any{"date": day.Format("01-02"), "count": count, "bytes": bytes})
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"metrics": result, "activity": activity, "app_version": config.AppVersion})
}
