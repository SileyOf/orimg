package handlers

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"zhuapic/mailer"
	"zhuapic/utils"
)

func (a *App) MailTemplates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	kind := r.PathValue("kind")
	if _, ok := mailer.Titles[kind]; !ok {
		utils.WriteError(w, 404, "邮件类型不存在")
		return
	}
	t, err := mailer.LoadTemplate(r.Context(), a.DB, kind)
	if err != nil {
		utils.WriteError(w, 500, "读取模板失败")
		return
	}
	if r.Method != "GET" {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&t) != nil {
			utils.WriteError(w, 400, "文案格式错误")
			return
		}
		if err = t.Validate(); err != nil {
			utils.WriteError(w, 400, err.Error())
			return
		}
	}
	if r.Method == "PUT" {
		b, _ := json.Marshal(t)
		if _, err = a.DB.ExecContext(r.Context(), `INSERT INTO mail_templates(kind,content_json) VALUES(?,?) ON DUPLICATE KEY UPDATE content_json=VALUES(content_json)`, kind, string(b)); err != nil {
			utils.WriteError(w, 500, "保存失败")
			return
		}
		actor, _ := a.currentUser(r)
		a.writeAudit(r, actor, "mail.template_updated", "mail", kind, nil)
	}
	c, _, err := mailer.New(a.DB, a.Cfg.JWTSecret).Config(r.Context())
	if err != nil {
		utils.WriteError(w, 500, "读取邮件设置失败")
		return
	}
	m := mailer.Message{To: "preview@example.test", Body: mailer.PurposeMessage(kind), ActionURL: c.BaseURL + "/account-action#preview", ValidFor: "24 小时"}
	if kind == "reset" {
		m.ValidFor = "20 分钟"
	}
	if kind == "test" || kind == "security" {
		m.ActionURL = ""
	}
	m = mailer.Render(c, kind, t, m, utils.GetSetting(a.DB, "default_accent_color", "lime"))
	html := strings.ReplaceAll(m.HTML, "cid:origin-logo", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(m.Image))
	utils.WriteJSON(w, 200, map[string]any{"template": t, "html": html, "text": m.Body, "subject": m.Subject})
}
