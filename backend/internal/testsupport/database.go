// Package testsupport contains guards for explicitly requested integration tests.
package testsupport

import (
	"net"
	"os"
	"strconv"

	"github.com/go-sql-driver/mysql"
)

func IsIsolatedDatabase(cfg *mysql.Config) bool {
	if cfg == nil || os.Getenv("ORIMG_ALLOW_DB_TESTS") != "1" || cfg.Net != "tcp" || cfg.DBName != "zhuapictest" {
		return false
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	return ip != nil && ip.IsLoopback() && err == nil && n >= 1024 && n <= 65535
}
