package database

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"zhuapic/config"
	"zhuapic/utils"
)

const SchemaVersion = 10

type Inspection struct {
	Empty        bool  `json:"empty"`
	Version      int   `json:"version"`
	Users        int64 `json:"users"`
	Images       int64 `json:"images"`
	ActiveAdmins int64 `json:"active_admins"`
}

func Open(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	c := mysql.NewConfig()
	c.User, c.Passwd, c.DBName = cfg.DBUser, cfg.DBPassword, cfg.DBName
	c.Net, c.Addr = "tcp", net.JoinHostPort(cfg.DBHost, cfg.DBPort)
	c.ParseTime, c.Loc = true, time.Local
	// DATETIME values carry no zone. Run the session in the application's
	// zone so NOW()/CURRENT_TIMESTAMP and values written from Go agree even
	// when the MySQL server uses another zone (for example a UTC container).
	c.Params = map[string]string{"charset": "utf8mb4", "time_zone": "'" + time.Now().Format("-07:00") + "'"}
	c.Timeout, c.ReadTimeout, c.WriteTimeout = 8*time.Second, 2*time.Minute, 30*time.Second
	if cfg.DBTLS == "verify" {
		c.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.DBHost}
		if cfg.DBTLSCA != "" {
			roots, err := x509.SystemCertPool()
			if err != nil {
				roots = x509.NewCertPool()
			}
			if !roots.AppendCertsFromPEM([]byte(cfg.DBTLSCA)) {
				return nil, errors.New("invalid database CA certificate")
			}
			c.TLS.RootCAs = roots
		}
	}
	connector, err := mysql.NewConnector(c)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(max(2, cfg.DBMaxOpen))
	db.SetMaxIdleConns(min(cfg.DBMaxIdle, max(2, cfg.DBMaxOpen)))
	db.SetConnMaxLifetime(cfg.DBConnLifetime)
	db.SetConnMaxIdleTime(10 * time.Minute)
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = db.PingContext(probe); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Inspect(ctx context.Context, db *sql.DB, resumeFresh ...bool) (Inspection, error) {
	var result Inspection
	rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA=DATABASE()`)
	if err != nil {
		return result, err
	}
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return result, err
		}
		tables[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	result.Empty = len(tables) == 0
	if result.Empty {
		return result, nil
	}
	resuming := len(resumeFresh) > 0 && resumeFresh[0]
	for _, name := range []string{"users", "images", "site_settings"} {
		if !tables[name] && !resuming {
			return result, errors.New("database is not an empty database or a recognized Origin database")
		}
	}
	if tables["schema_migrations"] {
		if err = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&result.Version); err != nil {
			return result, err
		}
		if result.Version > SchemaVersion {
			return result, errors.New("database is newer than this application; downgrade is not supported")
		}
	}
	if tables["users"] {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&result.Users); err != nil {
			return result, err
		}
	}
	if tables["images"] {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM images`).Scan(&result.Images); err != nil {
			return result, err
		}
	}
	if tables["users"] {
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND active=1`).Scan(&result.ActiveAdmins); err != nil {
			return result, err
		}
	}
	return result, nil
}

// The lock lives on its own connection, across DDL's implicit commits.
func Initialize(ctx context.Context, db *sql.DB, cfg *config.Config, bootstrap bool) error {
	if bootstrap {
		if err := config.ValidateSecret(cfg.JWTSecret); err != nil {
			return err
		}
	}
	// Keep the advisory lock outside the migration pool: some legacy migrations
	// stream rows while issuing updates, and must work with a two-connection pool.
	lockDB, err := Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer lockDB.Close()
	conn, err := lockDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	hash := sha256.Sum256([]byte(cfg.DBName))
	lock := "orimg:init:" + hex.EncodeToString(hash[:16])
	var acquired int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK(?,30)`, lock).Scan(&acquired); err != nil {
		return err
	}
	if acquired != 1 {
		return errors.New("another installation or upgrade is in progress")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, `SELECT RELEASE_LOCK(?)`, lock)
	}()
	info, err := Inspect(ctx, db, bootstrap)
	if err != nil {
		return err
	}
	if bootstrap && info.Users > 0 {
		if err = VerifyAdministrator(ctx, db, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			return err
		}
	}
	if info.Users == 0 && bootstrap {
		if info.Images != 0 {
			return errors.New("refusing bootstrap in a database with images but no users")
		}
		if err = config.ValidateAdmin(cfg.AdminUsername, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			return err
		}
	} else if info.ActiveAdmins == 0 {
		return errors.New("database has no active administrator; explicit administrator recovery is required")
	}
	if !info.Empty && info.Users > 0 {
		if err = VerifySecrets(ctx, db, cfg.JWTSecret); err != nil {
			return err
		}
	}
	// Migrations write site_settings directly; drop any snapshot read earlier.
	defer utils.InvalidateSettings(db)
	if err = migrate(db, cfg); err != nil {
		return err
	}
	if err = seedAdmin(db, cfg, bootstrap); err != nil {
		return err
	}
	if err = seedSettings(db, cfg); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte("orimg-install-key:" + cfg.JWTSecret))
	_, err = db.ExecContext(ctx, "INSERT IGNORE INTO site_settings (`key`,value) VALUES ('installation_key_digest',?)", hex.EncodeToString(digest[:]))
	return err
}

func VerifyAdministrator(ctx context.Context, db *sql.DB, email, password string) error {
	var hash string
	err := db.QueryRowContext(ctx, `SELECT password FROM users WHERE email=? AND role='admin' AND active=1`, strings.ToLower(strings.TrimSpace(email))).Scan(&hash)
	if err != nil || !utils.VerifyPassword(hash, password) {
		return errors.New("existing administrator verification failed")
	}
	return nil
}

func VerifySecrets(ctx context.Context, db *sql.DB, secret string) error {
	var expected string
	err := db.QueryRowContext(ctx, "SELECT value FROM site_settings WHERE `key`='installation_key_digest'").Scan(&expected)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if expected != "" {
		digest := sha256.Sum256([]byte("orimg-install-key:" + secret))
		if expected != hex.EncodeToString(digest[:]) {
			return errors.New("JWT_SECRET does not match this database; restore the original key")
		}
	}
	for _, item := range []struct{ table, column string }{
		{"site_settings", "value"}, {"storage_targets", "config_json"}, {"storages", "config_json"}, {"mail_settings", "config_json"}, {"mail_outbox", "payload"},
	} {
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, item.table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue
		}
		rows, err := db.QueryContext(ctx, "SELECT "+quoteIdentifier(item.column)+" FROM "+quoteIdentifier(item.table)+" WHERE "+quoteIdentifier(item.column)+" LIKE '%enc:v1:%'")
		if err != nil {
			return err
		}
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				rows.Close()
				return err
			}
			if err = verifyEncryptedValue(value, secret); err != nil {
				rows.Close()
				return fmt.Errorf("original JWT_SECRET cannot decrypt %s; no database changes have been made", item.table)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func verifyEncryptedValue(value, secret string) error {
	if strings.HasPrefix(value, "enc:v1:") {
		_, err := utils.DecryptSecret(value, secret)
		return err
	}
	var doc any
	if json.Unmarshal([]byte(value), &doc) != nil {
		return nil
	}
	var walk func(any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case string:
			if strings.HasPrefix(x, "enc:v1:") {
				_, err := utils.DecryptSecret(x, secret)
				return err
			}
		case map[string]any:
			for _, child := range x {
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range x {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(doc)
}
