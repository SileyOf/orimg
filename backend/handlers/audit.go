package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"zhuapic/models"
	"zhuapic/utils"
)

func (a *App) writeAudit(r *http.Request, actor *models.User, action, targetType, targetID string, details map[string]any) {
	actorID := uint(0)
	actorLabel := "system"
	if actor != nil {
		actorID = actor.ID
		actorLabel = actor.Email
		if actorLabel == "" {
			actorLabel = actor.Username
		}
	}
	body, _ := json.Marshal(details)
	if len(body) > 4096 {
		body = []byte(`{"truncated":true}`)
	}
	userAgent := strings.TrimSpace(r.UserAgent())
	if len(userAgent) > 255 {
		userAgent = userAgent[:255]
	}
	_, _ = a.DB.Exec(`INSERT INTO audit_logs
		(actor_user_id, actor_label, action, target_type, target_id, ip, user_agent, details)
		VALUES (NULLIF(?, 0), ?, ?, ?, ?, ?, ?, ?)`, actorID, actorLabel, action, targetType,
		targetID, a.clientIP(r), userAgent, string(body))
}

func (a *App) AdminAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 && parsed <= 100 {
		limit = parsed
	}
	before, _ := strconv.ParseUint(r.URL.Query().Get("before"), 10, 64)
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	query := `SELECT id, COALESCE(actor_user_id, 0), actor_label, action, target_type, target_id,
		ip, user_agent, details, created_at FROM audit_logs WHERE 1 = 1`
	args := []any{}
	if before > 0 {
		query += ` AND id < ?`
		args = append(args, before)
	}
	if action != "" {
		query += ` AND action = ?`
		args = append(args, action)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := a.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load audit logs")
		return
	}
	defer rows.Close()
	logs := make([]models.AuditLog, 0, limit)
	for rows.Next() {
		var item models.AuditLog
		if err := rows.Scan(&item.ID, &item.ActorUserID, &item.ActorLabel, &item.Action, &item.TargetType,
			&item.TargetID, &item.IP, &item.UserAgent, &item.Details, &item.CreatedAt); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to read audit logs")
			return
		}
		logs = append(logs, item)
	}
	hasMore := len(logs) > limit
	if hasMore {
		logs = logs[:limit]
	}
	nextBefore := uint(0)
	if hasMore && len(logs) > 0 {
		nextBefore = logs[len(logs)-1].ID
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"logs": logs, "has_more": hasMore, "next_before": nextBefore,
	})
}
