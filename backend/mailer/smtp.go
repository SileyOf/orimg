package mailer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      string `json:"tls"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
}
type Message struct {
	To        string `json:"to"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	HTML      string `json:"html,omitempty"`
	Image     []byte `json:"image,omitempty"`
	ActionURL string `json:"action_url,omitempty"`
	ValidFor  string `json:"valid_for,omitempty"`
}

func Address(value string) bool {
	a, e := mail.ParseAddress(value)
	return e == nil && a.Address == value && len(value) <= 190 && !strings.ContainsAny(value, "\r\n")
}
func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (c Config) Fingerprint() string {
	c.Enabled = false
	b, _ := json.Marshal(c)
	return Hash(string(b))
}
func (c Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 || c.Host == "" || len(c.Host) > 253 || strings.ContainsAny(c.Host, " /\\\r\n:@") {
		return errors.New("SMTP 主机或端口无效")
	}
	if c.TLS != "tls" && c.TLS != "starttls" {
		return errors.New("必须使用 TLS 或 STARTTLS")
	}
	if !Address(c.From) || strings.ContainsAny(c.Name+c.Username, "\r\n") || len(c.Name) > 80 || len(c.Username) > 190 || len(c.Password) > 1024 {
		return errors.New("发件人或认证信息无效")
	}
	u, e := url.Parse(c.BaseURL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return errors.New("安全链接域名必须是 HTTPS 站点根地址")
	}
	return nil
}

func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func Send(ctx context.Context, c Config, key string, m Message) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !Address(m.To) {
		return errors.New("收件地址无效")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", c.Host)
	if err != nil {
		return errors.New("SMTP 域名解析失败")
	}
	if len(ips) == 0 {
		return errors.New("SMTP 域名没有地址")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return errors.New("SMTP 地址必须是公网地址")
		}
	}
	var conn net.Conn
	dialer := net.Dialer{Timeout: 10 * time.Second}
	for _, ip := range ips {
		conn, err = dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(c.Port)))
		if err == nil {
			break
		}
	}
	if err != nil {
		return errors.New("SMTP 连接失败或超时")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	return sendConnection(conn, c, key, m)
}

func sendConnection(conn net.Conn, c Config, key string, m Message) error {
	tc := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	if c.TLS == "tls" {
		secured := tls.Client(conn, tc)
		if err := secured.Handshake(); err != nil {
			return errors.New("SMTP TLS 证书或握手失败")
		}
		conn = secured
	}
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return errors.New("SMTP 握手失败")
	}
	defer client.Close()
	if c.TLS == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP 不支持 STARTTLS，未发送凭据")
		}
		if err = client.StartTLS(tc); err != nil {
			return errors.New("SMTP TLS 证书或握手失败")
		}
	}
	if c.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return errors.New("SMTP 认证失败，请检查账号、授权码及 PLAIN 支持")
		}
	}
	if err = client.Mail(c.From); err != nil {
		return errors.New("SMTP 拒绝发件人")
	}
	if err = client.Rcpt(m.To); err != nil {
		return errors.New("SMTP 拒绝收件人")
	}
	w, err := client.Data()
	if err != nil {
		return errors.New("SMTP 无法接收邮件内容")
	}
	_, err = w.Write(encodeMessage(c, key, m))
	if err != nil {
		return errors.New("SMTP 写入超时或连接中断")
	}
	if err = w.Close(); err != nil {
		return errors.New("SMTP 未确认接收，可能需要重试")
	}
	// Acceptance is the DATA acknowledgement, not successful delivery to the inbox.
	_ = client.Quit()
	return nil
}

func encodeMessage(c Config, key string, m Message) []byte {
	if m.HTML != "" {
		return encodeHTMLMessage(c, key, m)
	}
	host := strings.SplitN(c.From, "@", 2)[1]
	from := (&mail.Address{Name: c.Name, Address: c.From}).String()
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n", from, m.To, mime.BEncoding.Encode("UTF-8", m.Subject), time.Now().Format(time.RFC1123Z), key, host)
	encoded := base64.StdEncoding.EncodeToString([]byte(m.Body))
	var body strings.Builder
	body.WriteString(headers)
	for len(encoded) > 76 {
		body.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	body.WriteString(encoded + "\r\n")
	return []byte(body.String())
}

func encodeHTMLMessage(c Config, key string, m Message) []byte {
	var body bytes.Buffer
	related := multipart.NewWriter(&body)
	var alternative bytes.Buffer
	alt := multipart.NewWriter(&alternative)
	writePart := func(w *multipart.Writer, contentType, cid string, data []byte) {
		h := textproto.MIMEHeader{"Content-Type": {contentType}, "Content-Transfer-Encoding": {"base64"}}
		if cid != "" {
			h.Set("Content-ID", "<"+cid+">")
			h.Set("Content-Disposition", "inline; filename=origin.png")
		}
		part, _ := w.CreatePart(h)
		encoded := base64.StdEncoding.EncodeToString(data)
		for len(encoded) > 76 {
			fmt.Fprint(part, encoded[:76]+"\r\n")
			encoded = encoded[76:]
		}
		fmt.Fprint(part, encoded+"\r\n")
	}
	writePart(alt, "text/plain; charset=UTF-8", "", []byte(m.Body))
	writePart(alt, "text/html; charset=UTF-8", "", []byte(m.HTML))
	alt.Close()
	part, _ := related.CreatePart(textproto.MIMEHeader{"Content-Type": {"multipart/alternative; boundary=" + alt.Boundary()}})
	part.Write(alternative.Bytes())
	if len(m.Image) > 0 {
		writePart(related, "image/png", "origin-logo", m.Image)
	}
	related.Close()
	host := strings.SplitN(c.From, "@", 2)[1]
	head := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=%s\r\n\r\n", (&mail.Address{Name: c.Name, Address: c.From}).String(), m.To, mime.BEncoding.Encode("UTF-8", m.Subject), time.Now().Format(time.RFC1123Z), key, host, related.Boundary())
	return append([]byte(head), body.Bytes()...)
}
