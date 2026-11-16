package database

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"os"
	"testing"
	"zhuapic/internal/testsupport"
)

func TestStorageTargetMigrationOnApprovedTestDB(t *testing.T) {
	dsn := os.Getenv("ORIMG_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit test database required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if !testsupport.IsIsolatedDatabase(cfg) {
		t.Fatal("requires ORIMG_ALLOW_DB_TESTS=1 and a loopback zhuapictest database on an unprivileged TCP port")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fingerprint := func() string {
		h := sha256.New()
		for _, query := range []string{"SELECT CONCAT_WS('|',`key`,value) FROM site_settings WHERE `key`<>'guest_group_id' ORDER BY `key`", "SELECT CONCAT_WS('|',id,username,email,password,role,active) FROM users ORDER BY id", "SELECT CONCAT_WS('|',id,public_name,storage_mode,storage_key,checksum_sha256) FROM images ORDER BY id", "SELECT CONCAT_WS('|',id,image_id,storage_mode,storage_key,status) FROM image_replicas ORDER BY id"} {
			rows, err := db.Query(query)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var s string
				if err := rows.Scan(&s); err != nil {
					t.Fatal(err)
				}
				fmt.Fprintln(h, s)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
		return fmt.Sprintf("%x", h.Sum(nil))
	}
	before := fingerprint()
	for i := 0; i < 2; i++ {
		if err := runVersionedMigrations(db); err != nil {
			t.Fatal(err)
		}
	}
	if after := fingerprint(); before != after {
		t.Fatal("migration changed existing settings/users/images/replicas")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM storage_targets WHERE legacy_mode IN ('local','s3')").Scan(&count); err != nil || count != 2 {
		t.Fatal("legacy storage mapping missing")
	}
	t.Log("storage migrations applied twice; existing settings, users, images and legacy replicas unchanged")
}
