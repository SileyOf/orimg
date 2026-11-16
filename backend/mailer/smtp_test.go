package mailer

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{Host: "smtp.example.test", Port: 465, TLS: "tls", From: "sender@example.test", Name: "起源", BaseURL: "https://example.test"}
}
func TestConfigAndMessageSafety(t *testing.T) {
	c := testConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){func(c *Config) { c.TLS = "none" }, func(c *Config) { c.From = "a@example.test\r\nBcc: b@example.test" }, func(c *Config) { c.BaseURL = "http://example.test" }, func(c *Config) { c.BaseURL = "https://user@example.test" }, func(c *Config) { c.Host = "host/path" }} {
		bad := c
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	enabled := c
	enabled.Enabled = true
	if enabled.Fingerprint() != c.Fingerprint() {
		t.Fatal("enable flag changes tested fingerprint")
	}
	enabled.Password = "changed"
	if enabled.Fingerprint() == c.Fingerprint() {
		t.Fatal("credential changes retain tested fingerprint")
	}
	for _, ip := range []string{"127.0.0.1", "10.1.1.1", "169.254.169.254", "::1", "fd00::1", "0.0.0.0", "100.100.100.200", "::ffff:127.0.0.1", "64:ff9b::a00:1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Fatal("private SMTP accepted", ip)
		}
	}
	m := Message{To: "recipient@example.test", Subject: "验证邮箱", Body: strings.Repeat("一次性链接\n", 40)}
	parsed, err := mail.ReadMessage(bytes.NewReader(encodeMessage(c, "0123456789", m)))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != m.Subject {
		t.Fatal("subject encoding", err)
	}
	decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, parsed.Body))
	if err != nil || string(decoded) != m.Body {
		t.Fatal("body encoding", err)
	}
}

func TestSTARTTLSNeverDowngrades(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	server.SetDeadline(time.Now().Add(3 * time.Second))
	commands := make(chan string, 1)
	go func() {
		defer server.Close()
		io.WriteString(server, "220 fake ESMTP\r\n")
		reader := bufio.NewReader(server)
		line, _ := reader.ReadString('\n')
		commands <- line
		io.WriteString(server, "250-fake\r\n250 AUTH PLAIN\r\n")
		rest, _ := io.ReadAll(reader)
		if strings.Contains(string(rest), "AUTH") {
			commands <- "AUTH"
		}
	}()
	c := testConfig()
	c.TLS = "starttls"
	c.Username = "secret-user"
	c.Password = "secret-password"
	err := sendConnection(client, c, "test", Message{To: "recipient@example.test"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatal("plaintext downgrade not rejected", err)
	}
	if line := <-commands; !strings.HasPrefix(line, "EHLO") {
		t.Fatal(line)
	}
}
