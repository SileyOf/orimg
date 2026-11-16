package mailer

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"strings"
	"testing"
)

func TestMailPreviewArtifact(t *testing.T) {
	path := os.Getenv("ORIMG_MAIL_PREVIEW")
	if path == "" {
		t.Skip("preview artifact not requested")
	}
	m := Render(Config{Name: "Origin"}, "reset", DefaultTemplate("reset"), Message{Body: "我们收到了您的密码重设请求。请点击下方按钮，安全地设置新密码。", ActionURL: "https://example.test/account-action#reset:preview", ValidFor: "20 分钟"}, "red")
	html := strings.ReplaceAll(m.HTML, "cid:origin-logo", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(m.Image))
	if e := os.WriteFile(path, []byte(html), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestRenderedTemplateAndMIME(t *testing.T) {
	c := Config{Name: "Origin <test>", From: "sender@example.test"}
	tpl := DefaultTemplate("register")
	tpl.Title = "<script>alert(1)</script>"
	tpl.Body = "Hello {{site_name}}\n\n{{message}}"
	m := Render(c, "register", tpl, Message{To: "recipient@example.test", Body: "<img src=x onerror=alert(1)>", ActionURL: "https://example.test/account-action#register:token", ValidFor: "24 小时"}, "red")
	if strings.Contains(m.HTML, "<script>") || strings.Contains(m.HTML, "<img src=x") || !strings.Contains(m.HTML, "&lt;script&gt;") {
		t.Fatal("unescaped custom text")
	}
	if !strings.Contains(m.Body, "#register:token") || !strings.Contains(m.Body, "24 小时") {
		t.Fatal("missing plain text fallback")
	}
	if _, e := png.Decode(bytes.NewReader(m.Image)); e != nil {
		t.Fatal(e)
	}
	msg, e := mail.ReadMessage(bytes.NewReader(encodeMessage(c, "fixture", m)))
	if e != nil {
		t.Fatal(e)
	}
	mt, params, e := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if e != nil || mt != "multipart/related" {
		t.Fatal(mt, e)
	}
	related := multipart.NewReader(msg.Body, params["boundary"])
	alt, e := related.NextPart()
	if e != nil {
		t.Fatal(e)
	}
	mt, params, e = mime.ParseMediaType(alt.Header.Get("Content-Type"))
	if e != nil || mt != "multipart/alternative" {
		t.Fatal(mt, e)
	}
	alternatives := multipart.NewReader(alt, params["boundary"])
	for _, expected := range []string{m.Body, m.HTML} {
		part, e := alternatives.NextPart()
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
		if e != nil || string(raw) != expected {
			t.Fatal("MIME content differs", e)
		}
	}
	img, e := related.NextPart()
	if e != nil || img.Header.Get("Content-ID") != "<origin-logo>" {
		t.Fatal("missing inline image", e)
	}
	decoded, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, img))
	if e != nil || !bytes.Equal(decoded, m.Image) {
		t.Fatal("invalid image body")
	}
}

func TestTemplateValidationAndUnsafeLink(t *testing.T) {
	tpl := DefaultTemplate("reset")
	if err := tpl.Validate(); err != nil {
		t.Fatal(err)
	}
	tpl.Subject = "Hello\r\nBcc: attacker@example.test"
	if tpl.Validate() == nil {
		t.Fatal("header newline accepted")
	}
	tpl = DefaultTemplate("reset")
	tpl.Body = strings.Repeat("文", 4001)
	if tpl.Validate() == nil {
		t.Fatal("oversized body accepted")
	}
	m := Render(Config{Name: "Origin"}, "reset", DefaultTemplate("reset"), Message{ActionURL: "javascript:alert(1)"}, "unknown")
	if strings.Contains(m.HTML, "javascript:") || strings.Contains(m.HTML, "ZgotmplZ") {
		t.Fatal("unsafe URL leaked")
	}
	if bytes.Equal(logoPNG("lime"), logoPNG("red")) {
		t.Fatal("logo does not follow accent")
	}
}
