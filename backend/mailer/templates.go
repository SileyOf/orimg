package mailer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/url"
	"strings"
	"unicode/utf8"
)

type Template struct {
	Subject string `json:"subject"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Button  string `json:"button"`
	Footer  string `json:"footer"`
}

var Titles = map[string]string{"test": "测试邮件", "verify": "验证邮箱", "reset": "重设密码", "change": "确认新邮箱", "security": "账号安全通知", "register": "欢迎加入，验证您的邮箱"}

func DefaultTemplate(kind string) Template {
	return Template{Subject: "{{site_name}} · " + Titles[kind], Title: Titles[kind], Body: "{{message}}", Button: map[string]string{"verify": "验证邮箱", "reset": "重设密码", "change": "确认新邮箱", "register": "创建账号"}[kind], Footer: "若非本人操作，请忽略此邮件。请勿将验证链接转发给他人。"}
}

func (t Template) Validate() error {
	for key, value := range map[string]string{"subject": t.Subject, "title": t.Title, "body": t.Body, "button": t.Button, "footer": t.Footer} {
		limit := 200
		if key == "body" {
			limit = 4000
		}
		if key == "footer" {
			limit = 500
		}
		if utf8.RuneCountInString(value) > limit || strings.ContainsRune(value, 0) {
			return errors.New("邮件文案超过长度限制")
		}
		if (key == "subject" || key == "title" || key == "button") && strings.ContainsAny(value, "\r\n") {
			return errors.New("标题与按钮不能换行")
		}
	}
	if strings.TrimSpace(t.Subject) == "" || strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Body) == "" {
		return errors.New("邮件主题、标题和正文不能为空")
	}
	return nil
}

type templateReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func LoadTemplate(ctx context.Context, db templateReader, kind string) (Template, error) {
	t := DefaultTemplate(kind)
	var raw string
	err := db.QueryRowContext(ctx, `SELECT content_json FROM mail_templates WHERE kind=?`, kind).Scan(&raw)
	if err == sql.ErrNoRows {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	err = json.Unmarshal([]byte(raw), &t)
	return t, err
}

var accents = map[string]color.RGBA{"lime": {202, 234, 114, 255}, "blue": {100, 170, 245, 255}, "purple": {182, 150, 232, 255}, "pink": {236, 146, 190, 255}, "red": {237, 99, 111, 255}, "orange": {245, 171, 97, 255}, "yellow": {244, 213, 100, 255}, "green": {137, 199, 143, 255}, "graphite": {176, 187, 178, 255}}

// A raster version of the Origin mark works in email clients that reject SVG.
func logoPNG(accent string) []byte {
	bg, ok := accents[accent]
	if !ok {
		bg = accents["lime"]
	}
	im := image.NewNRGBA(image.Rect(0, 0, 160, 160))
	ink := color.RGBA{32, 37, 31, 255}
	for y := 0; y < 160; y++ {
		for x := 0; x < 160; x++ {
			dx := math.Max(math.Abs(float64(x)-79.5)-32, 0)
			dy := math.Max(math.Abs(float64(y)-79.5)-32, 0)
			if dx*dx+dy*dy > 48*48 {
				continue
			}
			im.Set(x, y, bg)
			rx, ry := float64(x)-80, float64(y)-80
			radius := math.Hypot(rx, ry)
			if radius >= 34 && radius <= 54 && !(rx > 8 && ry < -8) {
				im.Set(x, y, ink)
			}
			if x >= 108 && x < 132 && y >= 28 && y < 52 {
				im.Set(x, y, ink)
			}
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, im)
	return out.Bytes()
}

var emailLayout = template.Must(template.New("email").Parse(`<!doctype html><html lang="zh-CN"><head><meta name="viewport" content="width=device-width,initial-scale=1"><meta charset="UTF-8"></head><body style="margin:0;background:#f5f7f3;color:#202820;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif"><table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:40px 16px"><table role="presentation" width="560" style="width:100%;max-width:560px" cellpadding="0" cellspacing="0"><tr><td style="padding:0 8px 28px"><img src="cid:origin-logo" width="48" height="48" alt="Origin" style="vertical-align:middle;border:0"><span style="font-size:22px;font-weight:650;vertical-align:middle;padding-left:12px">{{.Name}}</span></td></tr><tr><td style="padding:36px 28px;background:#ffffff;border:1px solid #e1e6df;border-radius:8px"><h1 style="font-size:26px;line-height:1.35;margin:0 0 24px;font-weight:650">{{.Title}}</h1>{{range .Paragraphs}}<p style="font-size:15px;line-height:1.9;margin:0 0 14px;white-space:pre-wrap;overflow-wrap:anywhere">{{.}}</p>{{end}}{{if .URL}}<table role="presentation" cellpadding="0" cellspacing="0" style="margin:28px 0"><tr><td bgcolor="#25382b" style="border-radius:6px"><a href="{{.URL}}" style="display:inline-block;padding:15px 26px;color:#ffffff;font-weight:600;font-size:15px;text-decoration:none">{{.Button}}</a></td></tr></table><p style="font-size:13px;color:#697469">链接有效期：{{.ValidFor}}。仅可使用一次。</p><p style="font-size:12px;line-height:1.7;word-break:break-all"><a href="{{.URL}}" style="color:#506851">{{.URL}}</a></p>{{end}}<p style="border-top:1px solid #e1e6df;padding-top:22px;margin-top:28px;font-size:12px;color:#748073;line-height:1.8">{{.Footer}}</p></td></tr><tr><td style="padding:24px 8px;color:#839080;font-size:12px">{{.Name}} · 账号服务</td></tr></table></td></tr></table></body></html>`))

func Render(c Config, kind string, t Template, m Message, accent string) Message {
	r := strings.NewReplacer("{{site_name}}", c.Name, "{{message}}", m.Body)
	title, body, footer := r.Replace(t.Title), r.Replace(t.Body), r.Replace(t.Footer)
	link := m.ActionURL
	if u, e := url.Parse(link); e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		link = ""
	}
	button := t.Button
	if strings.TrimSpace(button) == "" {
		button = "确认操作"
	}
	var html bytes.Buffer
	_ = emailLayout.Execute(&html, map[string]any{"Name": c.Name, "Title": title, "Paragraphs": strings.Split(body, "\n\n"), "Footer": footer, "URL": link, "Button": button, "ValidFor": m.ValidFor})
	m.Subject = r.Replace(t.Subject)
	m.HTML = html.String()
	m.Image = logoPNG(accent)
	m.Body = title + "\n\n" + body
	if link != "" {
		m.Body += "\n\n" + button + "：\n" + link + "\n\n有效期：" + m.ValidFor + "。仅可使用一次。"
	}
	m.Body += "\n\n" + footer
	return m
}
