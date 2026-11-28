// Package sqldrivers registers the database/sql drivers used by DoSQL. Import
// it for side effects at the Agent entrypoint, never from shared server code.
package sqldrivers

import (
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/sijms/go-ora/v2"
)
