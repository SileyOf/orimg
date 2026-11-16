package utils

import (
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// settingsDriver is a minimal in-memory database/sql driver that understands
// the two statements used by the settings helpers.
type settingsDriver struct {
	mu      sync.Mutex
	values  map[string]string
	queries atomic.Int32
}

func (d *settingsDriver) Open(string) (driver.Conn, error) { return &settingsConn{d}, nil }

type settingsConn struct{ d *settingsDriver }

func (c *settingsConn) Prepare(query string) (driver.Stmt, error) {
	return &settingsStmt{c.d, query}, nil
}
func (c *settingsConn) Close() error              { return nil }
func (c *settingsConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

type settingsStmt struct {
	d     *settingsDriver
	query string
}

func (s *settingsStmt) Close() error  { return nil }
func (s *settingsStmt) NumInput() int { return -1 }
func (s *settingsStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.d.values[args[0].(string)] = args[1].(string)
	return driver.RowsAffected(1), nil
}
func (s *settingsStmt) Query([]driver.Value) (driver.Rows, error) {
	if !strings.Contains(s.query, "FROM site_settings") {
		return nil, driver.ErrSkip
	}
	s.d.queries.Add(1)
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	rows := &settingsRows{}
	for k, v := range s.d.values {
		rows.data = append(rows.data, [2]string{k, v})
	}
	return rows, nil
}

type settingsRows struct {
	data [][2]string
	pos  int
}

func (r *settingsRows) Columns() []string { return []string{"key", "value"} }
func (r *settingsRows) Close() error      { return nil }
func (r *settingsRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.data) {
		return io.EOF
	}
	dest[0], dest[1] = r.data[r.pos][0], r.data[r.pos][1]
	r.pos++
	return nil
}

var registerSettingsDriver sync.Once
var testSettingsDriver = &settingsDriver{values: map[string]string{"site_name": "Origin"}}

func TestSettingsSnapshotCacheAndInvalidation(t *testing.T) {
	registerSettingsDriver.Do(func() { sql.Register("settings-test", testSettingsDriver) })
	db, err := sql.Open("settings-test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 50; i++ {
		if GetSetting(db, "site_name", "") != "Origin" || GetSetting(db, "missing", "fallback") != "fallback" {
			t.Fatal("unexpected setting value")
		}
	}
	if n := testSettingsDriver.queries.Load(); n != 1 {
		t.Fatalf("expected one snapshot query, got %d", n)
	}
	copyOfSettings := GetSettings(db)
	copyOfSettings["site_name"] = "mutated"
	if GetSetting(db, "site_name", "") != "Origin" {
		t.Fatal("GetSettings must return a private copy")
	}
	if err := UpsertSetting(db, "site_name", "Renamed"); err != nil {
		t.Fatal(err)
	}
	if GetSetting(db, "site_name", "") != "Renamed" {
		t.Fatal("write was not visible immediately")
	}
	if n := testSettingsDriver.queries.Load(); n != 2 {
		t.Fatalf("expected reload after write, got %d queries", n)
	}
}
