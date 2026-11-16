package installer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"zhuapic/config"
	"zhuapic/database"
	"zhuapic/routes"
	"zhuapic/storage"
	"zhuapic/utils"
)

// This suite only accepts a loopback port for a fresh, disposable MySQL container.
// It cannot use a production DSN or the historical shared test database.
func TestIsolatedDatabaseInstallation(t *testing.T) {
	port := os.Getenv("ORIMG_INSTALL_TEST_PORT")
	if port == "" {
		t.Skip("set ORIMG_INSTALL_TEST_PORT for disposable local MySQL")
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1024 || p > 65535 {
		t.Fatal("invalid isolated database port")
	}
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "static", "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	cfg := fixtureConfig(t)
	cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, cfg.DBPassword = "127.0.0.1", port, "orimg_install_test", "root", "fixture-only-orimg-047"
	cfg.JWTSecret = "0123456789abcdef0123456789abcdef"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	info, err := database.Inspect(ctx, db)
	if err != nil || !info.Empty {
		t.Fatal("requires a NEW empty disposable database", err)
	}
	var activated *config.Config
	s := &Server{Base: cfg, Path: filepath.Join(work, "installation.json"), Token: "fixture-install-code", Activate: func(c *config.Config, d *sql.DB) { activated = c; d.Close() }}
	req := Request{Host: cfg.DBHost, Port: port, User: cfg.DBUser, Password: cfg.DBPassword, Name: cfg.DBName, TLS: "disabled", BaseURL: "https://fixture.example.test",
		AdminUsername: "owner", AdminEmail: "owner@example.test", AdminPassword: "fixture-owner-password", Confirmed: true}
	assertStatus := func(server *Server, path string, request Request, want int) {
		t.Helper()
		w := installRequest(server, "POST", path, request)
		if w.Code != want {
			t.Fatalf("%s: %d want %d: %s", path, w.Code, want, w.Body.String())
		}
	}
	assertStatus(s, "/api/install/check", req, 200)
	info, _ = database.Inspect(ctx, db)
	if !info.Empty {
		t.Fatal("probe changed database")
	}
	assertStatus(s, "/api/install/complete", req, 200)
	if activated == nil {
		t.Fatal("not activated")
	}
	app := routes.Setup(db, activated, storage.NewManager(activated))
	for _, endpoint := range []struct {
		path   string
		status int
	}{{"/api/ready", 200}, {"/install", 404}, {"/api/install/status", 404}} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost"+endpoint.path, nil))
		if w.Code != endpoint.status {
			t.Fatalf("installed %s: %d", endpoint.path, w.Code)
		}
	}
	state, err := config.ReadInstallation(s.Path)
	if err != nil || !state.Installed {
		t.Fatal("state not committed", err)
	}
	var members int
	if err = db.QueryRow(`SELECT COUNT(*) FROM users u JOIN user_groups g ON g.id=u.group_id WHERE u.role='admin'`).Scan(&members); err != nil || members != 1 {
		t.Fatal("fresh admin lacks a group", err)
	}
	assertStatus(s, "/api/install/complete", req, 410)
	if _, err = db.Exec("UPDATE site_settings SET value='My retained page' WHERE `key`='hero_title'"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE site_settings SET value='custom/uploads' WHERE `key`='local_upload_dir'"); err != nil {
		t.Fatal(err)
	}
	var guestUploads string
	if err = db.QueryRow("SELECT value FROM site_settings WHERE `key`='public_upload_enabled'").Scan(&guestUploads); err != nil || guestUploads != "false" {
		t.Fatal("fresh install must not open guest uploads", err)
	}
	if _, err = db.Exec("UPDATE site_settings SET value='true' WHERE `key`='public_upload_enabled'"); err != nil {
		t.Fatal(err)
	}
	before := snapshotDatabase(t, db)
	attach := &Server{Base: cfg, Path: filepath.Join(work, "new-host", "installation.json"), Token: s.Token, Activate: func(c *config.Config, d *sql.DB) {
		if !c.DisableBackgroundWork {
			t.Error("old tasks started without consent")
		}
		d.Close()
	}}
	req.Existing = true
	req.Secret = state.Config.JWTSecret
	req.AdminPassword = "incorrect-password"
	assertStatus(attach, "/api/install/complete", req, 403)
	req.AdminPassword = "fixture-owner-password"
	req.Secret = "abcdef0123456789abcdef0123456789"
	assertStatus(attach, "/api/install/complete", req, 400)
	if !reflect.DeepEqual(before, snapshotDatabase(t, db)) {
		t.Fatal("failed verification changed database")
	}
	if _, err = os.Stat(attach.Path); !os.IsNotExist(err) {
		t.Fatal("failed verification wrote installation config")
	}
	req.Secret = state.Config.JWTSecret
	assertStatus(attach, "/api/install/complete", req, 200)
	if !reflect.DeepEqual(before, snapshotDatabase(t, db)) {
		t.Fatal("old database settings or accounts changed")
	}

	// The smallest supported pool must not deadlock during concurrent startup.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- database.Initialize(ctx, db, &state.Config, false) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, snapshotDatabase(t, db)) {
		t.Fatal("repeat startup changed database")
	}
	if _, err = db.Exec(`UPDATE users SET role='user'`); err != nil {
		t.Fatal(err)
	}
	if database.Initialize(ctx, db, &state.Config, false) == nil {
		t.Fatal("missing administrator silently accepted")
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&members); err != nil || members != 0 {
		t.Fatal("user auto-promoted", err)
	}
	if _, err = db.Exec(`UPDATE users SET role='admin'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO schema_migrations (version,name) VALUES (999,'future fixture')`); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Inspect(ctx, db); err == nil {
		t.Fatal("newer schema accepted")
	}
	if _, err = db.Exec(`DELETE FROM schema_migrations WHERE version=999`); err != nil {
		t.Fatal(err)
	}

	// A legacy installation without a fingerprint still verifies encrypted secrets.
	enc, err := utils.EncryptSecret("fixture-secret", state.Config.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DELETE FROM site_settings WHERE `key`='installation_key_digest'"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO site_settings (`key`,value) VALUES ('fixture_encrypted',?)", enc); err != nil {
		t.Fatal(err)
	}
	if database.VerifySecrets(ctx, db, "abcdef0123456789abcdef0123456789") == nil {
		t.Fatal("wrong legacy key accepted")
	}
	if err = database.VerifySecrets(ctx, db, state.Config.JWTSecret); err != nil {
		t.Fatal(err)
	}

	// Reproduce the production upgrade with a historical short encryption key.
	legacy := state.Config
	legacy.JWTSecret = "change-me"
	enc, err = utils.EncryptSecret("retained-smtp-and-storage-secret", legacy.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE site_settings SET value=? WHERE `key`='fixture_encrypted'", enc); err != nil {
		t.Fatal(err)
	}
	before = snapshotDatabase(t, db)
	legacyPath := filepath.Join(work, "legacy", "installation.json")
	bad := legacy
	bad.JWTSecret = "wrong-key"
	if database.PrepareRuntimeSecurity(ctx, db, &bad, false, legacyPath) == nil {
		t.Fatal("wrong short key accepted")
	}
	if _, err = os.Stat(config.SessionKeyPath(legacyPath)); !os.IsNotExist(err) {
		t.Fatal("wrong key created session key file")
	}
	if err = database.PrepareRuntimeSecurity(ctx, db, &legacy, false, legacyPath); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshotDatabase(t, db)) {
		t.Fatal("security preparation changed database")
	}
	if legacy.JWTSecret != "change-me" || config.ValidateSecret(legacy.SigningKey()) != nil {
		t.Fatal("legacy keys not separated")
	}
	if err = database.Initialize(ctx, db, &legacy, false); err != nil {
		t.Fatal(err)
	}
	// Initialization adds only the existing v0.4.7 key fingerprint, not a schema change.
	if _, err = db.Exec("DELETE FROM site_settings WHERE `key`='installation_key_digest'"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshotDatabase(t, db)) {
		t.Fatal("weak-key upgrade rewrote existing data")
	}
	weakAttach := &Server{Base: cfg, Path: filepath.Join(work, "weak-attach", "installation.json"), Token: s.Token,
		Activate: func(c *config.Config, d *sql.DB) {
			if c.JWTSecret != legacy.JWTSecret || config.ValidateSecret(c.SigningKey()) != nil {
				t.Error("installer changed encryption key")
			}
			d.Close()
		}}
	req.Secret = legacy.JWTSecret
	assertStatus(weakAttach, "/api/install/complete", req, 200)
	weakState, err := config.ReadInstallation(weakAttach.Path)
	if err != nil || weakState.Config.JWTSecret != legacy.JWTSecret {
		t.Fatal("weak legacy checkpoint cannot restart", err)
	}
	if err = database.PrepareRuntimeSecurity(ctx, db, &weakState.Config, false, weakAttach.Path); err != nil {
		t.Fatal(err)
	}
	before = snapshotDatabase(t, db)
	if err = database.Initialize(ctx, db, &weakState.Config, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshotDatabase(t, db)) {
		t.Fatal("repeat legacy upgrade changed database")
	}
	t.Log("short-key upgrade, wrong-key rejection, unchanged encrypted data and old-database installer passed")

	// Simulate interruption after the first DDL but before user creation.
	if _, err = db.Exec(`CREATE DATABASE orimg_install_resume`); err != nil {
		t.Fatal(err)
	}
	resumeCfg := *cfg
	resumeCfg.DBName = "orimg_install_resume"
	resumeDB, err := database.Open(ctx, &resumeCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer resumeDB.Close()
	if _, err = resumeDB.Exec("CREATE TABLE site_settings (`key` VARCHAR(80) PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Inspect(ctx, resumeDB); err == nil {
		t.Fatal("unrecognized partial database accepted without checkpoint")
	}
	checkpoint := &config.Installation{Config: resumeCfg, Fresh: true}
	resume := &Server{Base: &resumeCfg, Pending: checkpoint, Path: filepath.Join(work, "resume.json"), Token: s.Token, Activate: func(_ *config.Config, d *sql.DB) { d.Close() }}
	if err = config.WriteInstallation(resume.Path, checkpoint); err != nil {
		t.Fatal(err)
	}
	req.Name = resumeCfg.DBName
	req.Existing = false
	assertStatus(resume, "/api/install/check", req, 200)
	assertStatus(resume, "/api/install/complete", req, 200)
	resumed, err := config.ReadInstallation(resume.Path)
	if err != nil || !resumed.Installed || resumed.Config.JWTSecret != resumeCfg.JWTSecret {
		t.Fatal("resume lost original key", err)
	}
	t.Log("fresh install, read-only probe, old DB preservation, wrong credentials/key, concurrent startup, no auto-promotion, downgrade rejection and interrupted setup passed")
	db.Close()
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/ready", nil))
	if w.Code != 503 {
		t.Fatal("closed database reported ready")
	}
}

func snapshotDatabase(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	result := map[string][]string{}
	for _, table := range tables {
		rows, err = db.Query(fmt.Sprintf("SELECT * FROM `%s`", table))
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		result[table] = []string{}
		for rows.Next() {
			if err = rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(values)
			result[table] = append(result[table], string(b))
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(result[table])
	}
	return result
}
