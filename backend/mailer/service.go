package mailer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"zhuapic/utils"
)

type Service struct {
	DB      *sql.DB
	Secret  string
	Deliver func(context.Context, Config, string, Message) error
}

func New(db *sql.DB, secret string) *Service { return &Service{DB: db, Secret: secret} }
func (s *Service) Config(ctx context.Context) (Config, string, error) {
	c := Config{Port: 465, TLS: "tls", Name: "Origin"}
	var raw, tested string
	err := s.DB.QueryRowContext(ctx, `SELECT config_json,tested_hash FROM mail_settings WHERE id=1`).Scan(&raw, &tested)
	if errors.Is(err, sql.ErrNoRows) {
		return c, "", nil
	}
	if err != nil {
		return c, "", err
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, "", err
	}
	c.Password, err = utils.DecryptSecret(c.Password, s.Secret)
	return c, tested, err
}
func (s *Service) Save(ctx context.Context, c Config) error {
	old, tested, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if c.Password == "" {
		c.Password = old.Password
	}
	c.Host = strings.TrimSpace(c.Host)
	c.From = strings.TrimSpace(c.From)
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if err = c.Validate(); err != nil {
		return err
	}
	if c.Enabled && tested != c.Fingerprint() {
		return errors.New("请先保存关闭状态的配置，并成功发送测试邮件，再开启正式发信")
	}
	c.Password, err = utils.EncryptSecret(c.Password, s.Secret)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(c)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO mail_settings(id,config_json) VALUES(1,?) ON DUPLICATE KEY UPDATE config_json=VALUES(config_json)`, string(raw))
	return err
}
func (s *Service) Allow(ctx context.Context, key string, limit, seconds int) bool {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	bucket := Hash(key)
	window := time.Now().Unix() / int64(seconds) * int64(seconds)
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO mail_limits(bucket,window_start,hits) VALUES(?,?,0)`, bucket, window); err != nil {
		return false
	}
	var start int64
	var hits int
	if err = tx.QueryRowContext(ctx, `SELECT window_start,hits FROM mail_limits WHERE bucket=? FOR UPDATE`, bucket).Scan(&start, &hits); err != nil {
		return false
	}
	if start != window {
		hits = 0
	}
	if hits >= limit {
		return false
	}
	if _, err = tx.ExecContext(ctx, `UPDATE mail_limits SET window_start=?,hits=? WHERE bucket=?`, window, hits+1, bucket); err != nil {
		return false
	}
	return tx.Commit() == nil
}
func Mask(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	r := []rune(parts[0])
	if len(r) == 0 {
		return "***"
	}
	return string(r[0]) + "***@" + parts[1]
}
func (s *Service) QueueTx(ctx context.Context, tx *sql.Tx, c Config, kind string, m Message, challenge string, expires time.Time) (uint, error) {
	if !Address(m.To) {
		return 0, errors.New("无效收件地址")
	}
	tpl, err := LoadTemplate(ctx, tx, kind)
	if err != nil {
		return 0, err
	}
	accent := "lime"
	_ = tx.QueryRowContext(ctx, "SELECT value FROM site_settings WHERE `key`='default_accent_color'").Scan(&accent)
	m = Render(c, kind, tpl, m, accent)
	key, err := utils.RandomHex(16)
	if err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(m)
	payload, err := utils.EncryptSecret(string(raw), s.Secret)
	if err != nil {
		return 0, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_outbox WHERE status IN ('queued','sending')`).Scan(&count); err != nil {
		return 0, err
	}
	if count >= 1000 {
		return 0, errors.New("邮件队列已满")
	}
	seconds := int64(time.Until(expires).Seconds())
	if seconds < 1 {
		return 0, errors.New("邮件请求已过期")
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO mail_outbox(message_key,kind,recipient_mask,payload,challenge_hash,config_hash,expires_at) VALUES(?,?,?,?,?,?,DATE_ADD(NOW(),INTERVAL ? SECOND))`, key, kind, Mask(m.To), payload, challenge, c.Fingerprint(), seconds)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}
func (s *Service) Test(ctx context.Context, to string) (uint, error) {
	c, _, err := s.Config(ctx)
	if err != nil {
		return 0, err
	}
	if err = c.Validate(); err != nil {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := s.QueueTx(ctx, tx, c, "test", Message{To: to, Subject: c.Name + " · 测试邮件", Body: "这是一封由管理员主动发送的 Origin 测试邮件。\n收到此邮件后可以开启正式发信。"}, "", time.Now().Add(time.Hour))
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			work, cancel := context.WithTimeout(ctx, 45*time.Second)
			_ = s.ProcessOne(work)
			cancel()
		}
	}
}
func (s *Service) ProcessOne(ctx context.Context) error {
	c, _, err := s.Config(ctx)
	if err != nil {
		return err
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE mail_outbox SET status='expired',payload='',lease='',lease_until=NULL,finished_at=NOW() WHERE expires_at<=NOW() AND status IN ('queued','sending','failed')`)
	_, _ = s.DB.ExecContext(ctx, `UPDATE mail_outbox SET status='queued',lease='',lease_until=NULL WHERE status='sending' AND lease_until<NOW()`)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM account_challenges WHERE expires_at<DATE_SUB(NOW(),INTERVAL 7 DAY)`)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM registration_requests WHERE expires_at<DATE_SUB(NOW(),INTERVAL 7 DAY)`)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM mail_outbox WHERE finished_at<DATE_SUB(NOW(),INTERVAL 30 DAY)`)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM mail_limits WHERE window_start<UNIX_TIMESTAMP()-86400`)
	var id uint
	var key, kind, payload, challenge, fingerprint string
	var attempts int
	err = s.DB.QueryRowContext(ctx, `SELECT id,message_key,kind,payload,challenge_hash,config_hash,attempts FROM mail_outbox WHERE status='queued' AND next_attempt_at<=NOW() AND expires_at>NOW() AND (?=1 OR kind='test') ORDER BY id LIMIT 1`, c.Enabled).Scan(&id, &key, &kind, &payload, &challenge, &fingerprint, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !s.Allow(ctx, "worker-send", 20, 60) {
		return nil
	}
	lease, err := utils.RandomHex(16)
	if err != nil {
		return err
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE mail_outbox SET status='sending',attempts=attempts+1,lease=?,lease_until=DATE_ADD(NOW(),INTERVAL 2 MINUTE) WHERE id=? AND status='queued'`, lease, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil
	}
	status, message := "accepted", ""
	if fingerprint != c.Fingerprint() {
		status, message = "cancelled", "发信配置已变化，请重新发起"
	}
	if challenge != "" {
		var count int
		if kind == "register" {
			err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_requests r JOIN registration_settings s ON s.id=1 AND s.enabled=1 AND s.group_id=r.group_id JOIN user_groups g ON g.id=r.group_id JOIN upload_policies p ON p.id=g.policy_id AND p.active=1 WHERE r.token_hash=? AND r.consumed_at IS NULL AND r.expires_at>NOW()`, challenge).Scan(&count)
		} else {
			err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_challenges c JOIN users u ON u.id=c.user_id WHERE c.token_hash=? AND c.consumed_at IS NULL AND c.expires_at>NOW() AND u.active=1 AND u.auth_version=c.auth_version AND u.email=c.old_email`, challenge).Scan(&count)
		}
		if err != nil || count == 0 {
			status, message = "cancelled", "验证请求已失效"
		}
	}
	if status == "accepted" {
		raw, decryptErr := utils.DecryptSecret(payload, s.Secret)
		var m Message
		if decryptErr != nil || json.Unmarshal([]byte(raw), &m) != nil {
			err = errors.New("邮件内容无法解密")
		} else {
			send := s.Deliver
			if send == nil {
				send = Send
			}
			err = send(ctx, c, key, m)
		}
		if err != nil {
			message = err.Error()
			status = "queued"
			if attempts >= 4 {
				status = "failed"
			}
		}
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if status == "queued" || status == "failed" {
		_, err = s.DB.ExecContext(cleanup, `UPDATE mail_outbox SET status=?,last_error=?,lease='',lease_until=NULL,next_attempt_at=DATE_ADD(NOW(),INTERVAL ? SECOND) WHERE id=? AND lease=?`, status, message, 60*(1<<min(attempts, 4)), id, lease)
		return err
	}
	_, err = s.DB.ExecContext(cleanup, `UPDATE mail_outbox SET status=?,last_error=?,payload='',finished_at=NOW(),lease='',lease_until=NULL WHERE id=? AND lease=?`, status, message, id, lease)
	if err == nil && kind == "test" && status == "accepted" {
		_, err = s.DB.ExecContext(cleanup, `UPDATE mail_settings SET tested_hash=? WHERE id=1`, fingerprint)
	}
	return err
}
