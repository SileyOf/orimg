package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"zhuapic/mailer"
	"zhuapic/models"
	"zhuapic/utils"
)

func (a *App) MailConfig(w http.ResponseWriter, r *http.Request) {
	s := mailer.New(a.DB, a.Cfg.JWTSecret)
	if r.Method == http.MethodGet {
		c, tested, err := s.Config(r.Context())
		if err != nil {
			utils.WriteError(w, 500, "无法读取邮件配置")
			return
		}
		valid := tested != "" && tested == c.Fingerprint()
		has := c.Password != ""
		c.Password = ""
		utils.WriteJSON(w, 200, map[string]any{"config": c, "tested": valid, "has_password": has, "worker_enabled": !a.Cfg.DisableBackgroundWork})
		return
	}
	var c mailer.Config
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if json.NewDecoder(r.Body).Decode(&c) != nil {
		utils.WriteError(w, 400, "邮件配置无效")
		return
	}
	if err := s.Save(r.Context(), c); err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	user, _ := a.currentUser(r)
	a.writeAudit(r, user, "mail.settings_updated", "mail", "smtp", nil)
	utils.WriteJSON(w, 200, map[string]bool{"saved": true})
}
func (a *App) MailTest(w http.ResponseWriter, r *http.Request) {
	if a.Cfg.DisableBackgroundWork {
		utils.WriteError(w, 409, "后台工作器未启用，无法发送测试邮件")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || !mailer.Address(req.Email) {
		utils.WriteError(w, 400, "收件邮箱无效")
		return
	}
	s := mailer.New(a.DB, a.Cfg.JWTSecret)
	user, _ := a.currentUser(r)
	if !s.Allow(r.Context(), "test:"+strconv.Itoa(int(user.ID)), 3, 60) {
		utils.WriteError(w, 429, "测试邮件发送过于频繁")
		return
	}
	id, err := s.Test(r.Context(), req.Email)
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	a.writeAudit(r, user, "mail.test_queued", "mail", strconv.Itoa(int(id)), nil)
	utils.WriteJSON(w, 202, map[string]any{"id": id})
}
func (a *App) MailJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		if err != nil || id == 0 {
			utils.WriteError(w, 400, "无效任务")
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			utils.WriteError(w, 400, "无效操作")
			return
		}
		var res sql.Result
		if req.Action == "cancel" {
			res, err = a.DB.ExecContext(r.Context(), `UPDATE mail_outbox SET status='cancelled',payload='',finished_at=NOW() WHERE id=? AND status IN ('queued','failed')`, id)
		} else if req.Action == "retry" {
			res, err = a.DB.ExecContext(r.Context(), `UPDATE mail_outbox SET status='queued',attempts=0,last_error='',next_attempt_at=NOW() WHERE id=? AND status='failed' AND expires_at>NOW() AND payload<>''`, id)
		} else {
			utils.WriteError(w, 400, "无效操作")
			return
		}
		if err != nil {
			utils.WriteError(w, 500, "任务更新失败")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			utils.WriteError(w, 409, "任务状态已变化或已过期")
			return
		}
		actor, _ := a.currentUser(r)
		a.writeAudit(r, actor, "mail.task_"+req.Action, "mail", strconv.FormatUint(id, 10), nil)
		utils.WriteJSON(w, 200, map[string]bool{"updated": true})
		return
	}
	before, _ := strconv.ParseUint(r.URL.Query().Get("before"), 10, 64)
	status := r.URL.Query().Get("status")
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,kind,recipient_mask,status,attempts,last_error,UNIX_TIMESTAMP(created_at),COALESCE(UNIX_TIMESTAMP(finished_at),0) FROM mail_outbox WHERE (?=0 OR id<?) AND (?='' OR status=?) ORDER BY id DESC LIMIT 51`, before, before, status, status)
	if err != nil {
		utils.WriteError(w, 500, "邮件记录读取失败")
		return
	}
	defer rows.Close()
	jobs := []map[string]any{}
	var next uint64
	for rows.Next() {
		var id uint64
		var kind, to, state, message string
		var attempts int
		var created, finished int64
		if rows.Scan(&id, &kind, &to, &state, &attempts, &message, &created, &finished) != nil {
			utils.WriteError(w, 500, "邮件记录读取失败")
			return
		}
		if len(jobs) == 50 {
			next = jobs[len(jobs)-1]["id"].(uint64)
			break
		}
		var finishedAt any
		if finished > 0 {
			finishedAt = time.Unix(finished, 0).UTC()
		}
		jobs = append(jobs, map[string]any{"id": id, "kind": kind, "recipient": to, "status": state, "attempts": attempts, "error": message, "created_at": time.Unix(created, 0).UTC(), "finished_at": finishedAt})
	}
	if err := rows.Err(); err != nil {
		utils.WriteError(w, 500, "邮件记录读取失败")
		return
	}
	utils.WriteJSON(w, 200, map[string]any{"jobs": jobs, "next_before": next})
}

func (a *App) RequestAccountMail(w http.ResponseWriter, r *http.Request) {
	purpose := r.PathValue("purpose")
	if purpose != "reset" && purpose != "verify" && purpose != "change" {
		utils.WriteError(w, 404, "not found")
		return
	}
	start := time.Now()
	if purpose == "reset" {
		defer func() {
			if wait := 300*time.Millisecond - time.Since(start); wait > 0 {
				time.Sleep(wait)
			}
		}()
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"current_password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		utils.WriteError(w, 400, "请求无效")
		return
	}
	s := mailer.New(a.DB, a.Cfg.JWTSecret)
	c, _, err := s.Config(r.Context())
	if err != nil || !c.Enabled || a.Cfg.DisableBackgroundWork {
		utils.WriteError(w, 503, "邮件服务尚未启用，请联系管理员")
		return
	}
	if !s.Allow(r.Context(), "request-ip:"+a.clientIP(r), 10, 60) {
		utils.WriteError(w, 429, "请求过于频繁，请稍后再试")
		return
	}
	if !s.Allow(r.Context(), "request-global", 100, 60) {
		utils.WriteError(w, 429, "邮件服务繁忙，请稍后再试")
		return
	}
	user, logged := a.currentUser(r)
	to := ""
	generic := func() {
		utils.WriteJSON(w, 202, map[string]string{"message": "如账号可用，邮件将发送至对应邮箱。请稍后查看。"})
	}
	if purpose == "reset" {
		to, logged = cleanEmail(req.Email)
		if !logged {
			utils.WriteError(w, 400, "邮箱格式无效")
			return
		}
		if !s.Allow(r.Context(), "recipient:"+to, 1, 60) || !s.Allow(r.Context(), "recipient-hour:"+to, 5, 3600) {
			generic()
			return
		}
		var id uint
		if a.DB.QueryRowContext(r.Context(), `SELECT id FROM users WHERE email=? AND active=1`, to).Scan(&id) != nil {
			generic()
			return
		}
		user, err = a.findUserByID(id)
		if err != nil {
			generic()
			return
		}
	} else {
		if !logged {
			utils.WriteError(w, 401, "请先登录")
			return
		}
		to = user.Email
		if purpose == "change" {
			if !utils.VerifyPassword(user.Password, req.Password) {
				utils.WriteError(w, 401, "当前密码不正确")
				return
			}
			var ok bool
			to, ok = cleanEmail(req.Email)
			if !ok || strings.EqualFold(to, user.Email) {
				utils.WriteError(w, 400, "请填写不同的新邮箱")
				return
			}
		}
		if !s.Allow(r.Context(), "account:"+strconv.Itoa(int(user.ID)), 1, 60) || !s.Allow(r.Context(), "recipient:"+to, 1, 60) || !s.Allow(r.Context(), "recipient-hour:"+to, 5, 3600) {
			utils.WriteError(w, 429, "请稍后再发送邮件")
			return
		}
	}
	token, err := utils.RandomHex(32)
	if err != nil {
		utils.WriteError(w, 500, "无法创建验证请求")
		return
	}
	hash := mailer.Hash(token)
	ttl := 24 * time.Hour
	if purpose == "reset" {
		ttl = 20 * time.Minute
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, 500, "请求创建失败")
		return
	}
	defer tx.Rollback()
	var version uint64
	var email string
	var active bool
	if tx.QueryRowContext(r.Context(), `SELECT auth_version,email,active FROM users WHERE id=? FOR UPDATE`, user.ID).Scan(&version, &email, &active) != nil || version != user.AuthVersion || email != user.Email || !active {
		generic()
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE account_challenges SET consumed_at=NOW() WHERE user_id=? AND purpose=? AND consumed_at IS NULL`, user.ID, purpose); err != nil {
		utils.WriteError(w, 500, "请求创建失败")
		return
	}
	expires := time.Now().Add(ttl)
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO account_challenges(token_hash,user_id,purpose,email,old_email,auth_version,expires_at) VALUES(?,?,?,?,?,?,DATE_ADD(NOW(),INTERVAL ? SECOND))`, hash, user.ID, purpose, to, user.Email, user.AuthVersion, int64(ttl.Seconds())); err != nil {
		utils.WriteError(w, 500, "请求创建失败")
		return
	}
	link := c.BaseURL + "/account-action#" + purpose + ":" + token
	validFor := "24 小时"
	if purpose == "reset" {
		validFor = "20 分钟"
	}
	_, err = s.QueueTx(r.Context(), tx, c, purpose, mailer.Message{To: to, Body: mailer.PurposeMessage(purpose), ActionURL: link, ValidFor: validFor}, hash, expires)
	if err != nil {
		utils.WriteError(w, 503, "邮件暂时无法入队，请稍后再试")
		return
	}
	if err = tx.Commit(); err != nil {
		utils.WriteError(w, 500, "请求创建失败")
		return
	}
	if purpose == "reset" {
		generic()
	} else {
		utils.WriteJSON(w, 202, map[string]string{"message": "验证邮件已加入发送队列"})
	}
}

func (a *App) ConfirmAccountMail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Purpose  string `json:"purpose"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || len(req.Token) != 64 || (req.Purpose != "verify" && req.Purpose != "reset" && req.Purpose != "change") {
		utils.WriteError(w, 400, "验证链接无效")
		return
	}
	s := mailer.New(a.DB, a.Cfg.JWTSecret)
	if !s.Allow(r.Context(), "confirm-ip:"+a.clientIP(r), 20, 60) {
		utils.WriteError(w, 429, "请求过于频繁")
		return
	}
	hash := mailer.Hash(req.Token)
	var passwordHash string
	var err error
	if req.Purpose == "reset" {
		if len(req.Password) < 10 || len(req.Password) > 128 {
			utils.WriteError(w, 400, "新密码需要 10 至 128 位")
			return
		}
		passwordHash, err = utils.HashPassword(req.Password)
		if err != nil {
			utils.WriteError(w, 500, "无法设置密码")
			return
		}
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, 500, "验证失败")
		return
	}
	defer tx.Rollback()
	var uid uint
	var to, old string
	var version uint64
	// Lock user before challenge, matching the request path, to avoid cross-request deadlocks.
	if tx.QueryRowContext(r.Context(), `SELECT user_id FROM account_challenges WHERE token_hash=?`, hash).Scan(&uid) != nil {
		utils.WriteError(w, 400, "链接已失效或已使用")
		return
	}
	var currentEmail string
	var currentVersion uint64
	var active bool
	if tx.QueryRowContext(r.Context(), `SELECT email,auth_version,active FROM users WHERE id=? FOR UPDATE`, uid).Scan(&currentEmail, &currentVersion, &active) != nil {
		utils.WriteError(w, 400, "链接已失效或已使用")
		return
	}
	err = tx.QueryRowContext(r.Context(), `SELECT email,old_email,auth_version FROM account_challenges WHERE token_hash=? AND purpose=? AND consumed_at IS NULL AND expires_at>NOW() FOR UPDATE`, hash, req.Purpose).Scan(&to, &old, &version)
	if err != nil || !active || currentVersion != version || currentEmail != old {
		utils.WriteError(w, 400, "链接已失效或已使用")
		return
	}
	if req.Purpose == "reset" {
		_, err = tx.ExecContext(r.Context(), `UPDATE users SET password=?,auth_version=auth_version+1 WHERE id=?`, passwordHash, uid)
	} else if req.Purpose == "change" {
		_, err = tx.ExecContext(r.Context(), `UPDATE users SET email=?,email_verified=1,auth_version=auth_version+1 WHERE id=?`, to, uid)
	} else {
		_, err = tx.ExecContext(r.Context(), `UPDATE users SET email_verified=1 WHERE id=?`, uid)
	}
	if err != nil {
		utils.WriteError(w, 409, "账号更新失败，新邮箱可能已被使用")
		return
	}
	if req.Purpose != "verify" {
		if _, err = tx.ExecContext(r.Context(), `UPDATE api_tokens SET revoked_at=NOW() WHERE user_id=? AND revoked_at IS NULL`, uid); err != nil {
			utils.WriteError(w, 500, "会话撤销失败")
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE account_challenges SET consumed_at=NOW() WHERE token_hash=?`, hash); err != nil {
		utils.WriteError(w, 500, "验证失败")
		return
	}
	// Security notifications are committed with the account change; no plaintext password is sent.
	c, _, loadErr := s.Config(r.Context())
	if loadErr != nil {
		utils.WriteError(w, 503, "邮件服务暂时不可用")
		return
	}
	if req.Purpose != "verify" && c.Enabled {
		subject := "密码已重设"
		if req.Purpose == "change" {
			subject = "登录邮箱已更换"
		}
		_, err = s.QueueTx(r.Context(), tx, c, "security", mailer.Message{To: old, Subject: c.Name + " · " + subject, Body: subject + "。原会话和 API Token 已撤销。\n如果这不是您的操作，请立即联系站点管理员。"}, "", time.Now().Add(24*time.Hour))
		if err != nil {
			utils.WriteError(w, 503, "安全通知无法入队，请稍后重试")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		utils.WriteError(w, 500, "验证失败")
		return
	}
	a.writeAudit(r, &models.User{ID: uid, Email: to}, "mail.account_"+req.Purpose, "user", strconv.Itoa(int(uid)), nil)
	utils.WriteJSON(w, 200, map[string]any{"confirmed": true, "requires_login": req.Purpose != "verify"})
}

func (a *App) MailStatus(w http.ResponseWriter, r *http.Request) {
	c, _, err := mailer.New(a.DB, a.Cfg.JWTSecret).Config(r.Context())
	enabled := err == nil && c.Enabled && !a.Cfg.DisableBackgroundWork
	var register bool
	_ = a.DB.QueryRowContext(r.Context(), `SELECT s.enabled AND p.active FROM registration_settings s JOIN user_groups g ON g.id=s.group_id JOIN upload_policies p ON p.id=g.policy_id WHERE s.id=1`).Scan(&register)
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, 200, map[string]bool{"enabled": enabled, "registration_enabled": enabled && register})
}
