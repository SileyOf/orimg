package testsupport

import (
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestDatabaseGuard(t *testing.T) {
	cfg := mysql.NewConfig()
	cfg.Net, cfg.Addr, cfg.DBName = "tcp", "127.0.0.1:13306", "zhuapictest"
	t.Setenv("ORIMG_ALLOW_DB_TESTS", "")
	if IsIsolatedDatabase(cfg) {
		t.Fatal("missing opt-in accepted")
	}
	t.Setenv("ORIMG_ALLOW_DB_TESTS", "1")
	if !IsIsolatedDatabase(cfg) {
		t.Fatal("local fixture rejected")
	}
	for _, addr := range []string{"192.0.2.10:13306", "localhost:13306", "127.0.0.1:330", "127.0.0.1:99999", "invalid"} {
		cfg.Addr = addr
		if IsIsolatedDatabase(cfg) {
			t.Fatalf("unsafe address accepted: %s", addr)
		}
	}
	cfg.Addr = "[::1]:13306"
	if !IsIsolatedDatabase(cfg) {
		t.Fatal("IPv6 loopback rejected")
	}
	cfg.DBName = "production"
	if IsIsolatedDatabase(cfg) {
		t.Fatal("non-test database accepted")
	}
	if IsIsolatedDatabase(nil) {
		t.Fatal("nil config accepted")
	}
}
