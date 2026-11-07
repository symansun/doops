// dosql-driver is the template for an external DoSql engine. Build it with the
// driver you need linked in and install it as <driver dir>/dosql-driver-<scheme>:
//
//	go build -o <driver dir>/dosql-driver-extsql ./cmd/dosql-driver
package main

import (
	"fmt"
	"os"

	_ "github.com/go-sql-driver/mysql"

	"github.com/user/doops/agent/internal/server"
)

func main() {
	scheme := os.Getenv("DOSQL_DRIVER_SCHEME")
	if scheme == "" {
		scheme = "extsql"
	}
	if err := server.RunDoSQLDriver(scheme, "mysql", server.DoSQLMySQLDSN, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
