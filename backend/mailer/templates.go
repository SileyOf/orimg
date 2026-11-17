package mailer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	return Template{Subject: "{{site_name}} · " + Titles[kind], Title: Titles[kind], Body: "{{message}}", Button: map[string]string{"verify": "验证邮箱", "reset": "重设密码", "change": "确认新邮箱", "register": "创建账号"}[kind], Footer: "若非本人操作，请忽略此邮件。请勿将链接转发给他人，{{site_name}} 不会向您索要密码。"}
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

// PurposeMessage is the default explanation inserted as {{message}}.
func PurposeMessage(kind string) string {
	switch kind {
	case "verify":
		return "请确认这是您的邮箱地址。验证后，它将用于找回密码和接收账号安全通知。"
	case "reset":
		return "我们收到了重设账号密码的请求。点击下方按钮即可设置新密码。\n如果不是您本人申请，请忽略此邮件，您的密码不会改变。"
	case "change":
		return "请确认将账号邮箱更换为此地址。确认后，登录和通知都将使用这个新邮箱。"
	case "register":
		return "欢迎加入。验证您的邮箱后，即可设置用户名和密码，开启自己的图片空间。"
	case "security":
		return "您的账号安全设置刚刚发生变化。原会话和 API Token 已撤销。\n如果这不是您的操作，请立即联系站点管理员。"
	default:
		return "这是一封由管理员主动发送的测试邮件。\n收到此邮件说明发信配置可用，可以开启正式发信。"
	}
}

func accentHex(accent string) string {
	c, ok := accents[accent]
	if !ok {
		c = accents["lime"]
	}
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Inline styles and tables only: many mail clients drop <style> blocks.
var emailLayout = template.Must(template.New("email").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light"><title>{{.Subject}}</title></head>` +
	`<body style="margin:0;padding:0;background:#f4f6f2;color:#202820;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',Arial,sans-serif;-webkit-text-size-adjust:100%">` +
	`<div style="display:none;max-height:0;overflow:hidden;opacity:0;mso-hide:all">{{.Preheader}}</div>` +
	`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f6f2"><tr><td align="center" style="padding:36px 16px">` +
	`<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="width:100%;max-width:560px">` +
	`<tr><td style="padding:0 4px 22px"><img src="cid:origin-logo" width="40" height="40" alt="" style="vertical-align:middle;border:0;border-radius:8px"><span style="font-size:19px;font-weight:650;vertical-align:middle;padding-left:12px;color:#202820">{{.Name}}</span></td></tr>` +
	`<tr><td style="background:#ffffff;border:1px solid #e1e6df;border-radius:10px;overflow:hidden">` +
	`<div style="height:6px;background:{{.Accent}};font-size:0;line-height:0">&nbsp;</div>` +
	`<div style="padding:34px 30px 30px"><h1 style="font-size:24px;line-height:1.4;margin:0 0 20px;font-weight:650;color:#202820">{{.Title}}</h1>` +
	`{{range .Paragraphs}}<p style="font-size:15px;line-height:1.85;margin:0 0 14px;color:#2d362c;overflow-wrap:anywhere">{{range $i, $line := .}}{{if $i}}<br>{{end}}{{$line}}{{end}}</p>{{end}}` +
	`{{if .URL}}<table role="presentation" cellpadding="0" cellspacing="0" style="margin:26px 0 22px"><tr><td bgcolor="{{.Accent}}" style="border-radius:8px;background:{{.Accent}}"><a href="{{.URL}}" style="display:inline-block;padding:14px 28px;color:#1c241b;font-weight:650;font-size:15px;line-height:1;text-decoration:none;border-radius:8px">{{.Button}}</a></td></tr></table>` +
	`<p style="font-size:13px;line-height:1.7;margin:0 0 8px;color:#5f6b5e">链接 {{.ValidFor}}内有效，仅可使用一次。按钮无法点击时，请复制下面的地址到浏览器打开：</p>` +
	`<p style="font-size:12px;line-height:1.7;margin:0;word-break:break-all"><a href="{{.URL}}" style="color:#4c6a4c">{{.URL}}</a></p>{{end}}` +
	`<p style="border-top:1px solid #e8ece6;padding-top:20px;margin:28px 0 0;font-size:12px;color:#748073;line-height:1.8">{{range $i, $line := .Footer}}{{if $i}}<br>{{end}}{{$line}}{{end}}</p></div></td></tr>` +
	`<tr><td style="padding:20px 4px;color:#8a9688;font-size:12px;line-height:1.7">此邮件由 {{if .SiteURL}}<a href="{{.SiteURL}}" style="color:#6d7b6b">{{.Name}}</a>{{else}}{{.Name}}{{end}} 自动发送，请勿直接回复。</td></tr>` +
	`</table></td></tr></table></body></html>`))

func splitLines(text string) []string {
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func Render(c Config, kind string, t Template, m Message, accent string) Message {
	siteURL := strings.TrimRight(c.BaseURL, "/")
	if u, e := url.Parse(siteURL); e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		siteURL = ""
	}
	if strings.TrimSpace(m.Body) == "" {
		m.Body = PurposeMessage(kind)
	}
	r := strings.NewReplacer("{{site_name}}", c.Name, "{{message}}", m.Body, "{{site_url}}", siteURL, "{{valid_for}}", m.ValidFor)
	title, body, footer := r.Replace(t.Title), strings.TrimSpace(r.Replace(t.Body)), strings.TrimSpace(r.Replace(t.Footer))
	link := m.ActionURL
	if u, e := url.Parse(link); e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		link = ""
	}
	button := t.Button
	if strings.TrimSpace(button) == "" {
		button = "确认操作"
	}
	paragraphs := [][]string{}
	for _, block := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		if block = strings.TrimSpace(block); block != "" {
			paragraphs = append(paragraphs, splitLines(block))
		}
	}
	preheader := strings.Join(strings.Fields(strings.ReplaceAll(body, "\n", " ")), " ")
	if runes := []rune(preheader); len(runes) > 90 {
		preheader = string(runes[:90]) + "…"
	}
	m.Subject = r.Replace(t.Subject)
	var html bytes.Buffer
	_ = emailLayout.Execute(&html, map[string]any{"Name": c.Name, "Subject": m.Subject, "Title": title, "Paragraphs": paragraphs,
		"Footer": splitLines(footer), "URL": link, "Button": button, "ValidFor": m.ValidFor, "Accent": template.CSS(accentHex(accent)),
		"Preheader": preheader, "SiteURL": siteURL})
	m.HTML = html.String()
	m.Image = logoPNG(accent)
	text := title + "\n\n" + body
	if link != "" {
		text += "\n\n" + button + "：\n" + link + "\n\n链接 " + m.ValidFor + "内有效，仅可使用一次。"
	}
	if footer != "" {
		text += "\n\n" + footer
	}
	text += "\n\n— " + c.Name
	if siteURL != "" {
		text += "\n" + siteURL
	}
	m.Body = text
	return m
}
