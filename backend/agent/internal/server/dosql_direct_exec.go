package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/sijms/go-ora/v2"
)

const (
	doSQLDirectRowLimit   = 200
	doSQLDirectLockWait   = 5 * time.Second
	doSQLDirectStatementT = 60 * time.Second
)

type doSQLDirectRequest struct {
	Target       doSQLTarget
	Statements   []string
	Verification string
	ReadOnly     bool
	DryRun       bool
}

type doSQLStatementResult struct {
	Statement    string     `json:"statement"`
	AffectedRows int64      `json:"affected_rows,omitempty"`
	Columns      []string   `json:"columns,omitempty"`
	Rows         [][]string `json:"rows,omitempty"`
	Truncated    bool       `json:"truncated,omitempty"`
}

type doSQLDirectResult struct {
	Status       string                 `json:"status"`
	Committed    bool                   `json:"committed"`
	Statements   []doSQLStatementResult `json:"statements"`
	Verification *doSQLStatementResult  `json:"verification,omitempty"`
	DurationMS   int64                  `json:"duration_ms"`
}

// executeDoSQLDirect runs the whole batch plus its verification in one
// transaction on one connection, so a failed verification rolls the batch back.
// MySQL and Oracle commit DDL implicitly; for them only DML is atomic.
func executeDoSQLDirect(ctx context.Context, request doSQLDirectRequest) (doSQLDirectResult, error) {
	started := time.Now()
	result := doSQLDirectResult{Status: "failed"}
	if len(request.Statements) == 0 {
		return result, errors.New("dosql_statement_required")
	}
	db, err := sql.Open(request.Target.Driver, request.Target.DSN)
	if err != nil {
		return result, doSQLFailure("dosql_connect_failed", err.Error())
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return result, doSQLFailure("dosql_connect_failed", err.Error())
	}
	defer conn.Close()
	if err := prepareDoSQLSession(ctx, conn, request.Target.Engine); err != nil {
		return result, doSQLFailure("dosql_session_setup_failed", err.Error())
	}
	tx, err := beginDoSQLTransaction(ctx, conn, request.Target.Engine, request.ReadOnly)
	if err != nil {
		return result, doSQLFailure("dosql_transaction_failed", err.Error())
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for index, statement := range request.Statements {
		executed, err := runDoSQLStatement(ctx, tx, statement)
		if err != nil {
			return result, doSQLFailure("dosql_statement_failed", fmt.Sprintf("statement %d: %v", index+1, err))
		}
		result.Statements = append(result.Statements, executed)
	}
	if strings.TrimSpace(request.Verification) != "" {
		verified, err := runDoSQLStatement(ctx, tx, request.Verification)
		if err != nil {
			return result, doSQLFailure("dosql_verification_failed", err.Error())
		}
		result.Verification = &verified
	}
	result.DurationMS = time.Since(started).Milliseconds()
	if request.ReadOnly || request.DryRun {
		result.Status = "verified"
		if request.DryRun {
			result.Status = "dry_run_rolled_back"
		}
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return result, doSQLFailure("dosql_commit_failed", err.Error())
	}
	committed = true
	result.Committed = true
	result.Status = "verified"
	return result, nil
}

func prepareDoSQLSession(ctx context.Context, conn *sql.Conn, engine string) error {
	seconds := int(doSQLDirectLockWait / time.Second)
	var statements []string
	switch engine {
	case "postgres":
		statements = []string{
			fmt.Sprintf("SET lock_timeout = '%ds'", seconds),
			fmt.Sprintf("SET statement_timeout = '%ds'", int(doSQLDirectStatementT/time.Second)),
		}
	case "mysql":
		statements = []string{fmt.Sprintf("SET SESSION innodb_lock_wait_timeout = %d", seconds)}
	case "oracle":
		statements = []string{fmt.Sprintf("ALTER SESSION SET DDL_LOCK_TIMEOUT = %d", seconds)}
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func beginDoSQLTransaction(ctx context.Context, conn *sql.Conn, engine string, readOnly bool) (*sql.Tx, error) {
	// go-ora rejects TxOptions.ReadOnly; Oracle gets the equivalent statement.
	if engine == "oracle" {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil || !readOnly {
			return tx, err
		}
		if _, err := tx.ExecContext(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		return tx, nil
	}
	return conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
}

func runDoSQLStatement(ctx context.Context, tx *sql.Tx, statement string) (doSQLStatementResult, error) {
	executed := doSQLStatementResult{Statement: statement}
	if !doSQLReadOnlyStatement(statement) {
		outcome, err := tx.ExecContext(ctx, statement)
		if err != nil {
			return executed, err
		}
		executed.AffectedRows, _ = outcome.RowsAffected()
		return executed, nil
	}
	rows, err := tx.QueryContext(ctx, statement)
	if err != nil {
		return executed, err
	}
	defer rows.Close()
	if executed.Columns, err = rows.Columns(); err != nil {
		return executed, err
	}
	for rows.Next() {
		if len(executed.Rows) == doSQLDirectRowLimit {
			executed.Truncated = true
			break
		}
		values := make([]interface{}, len(executed.Columns))
		pointers := make([]interface{}, len(values))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return executed, err
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = doSQLCellText(value)
		}
		executed.Rows = append(executed.Rows, row)
	}
	return executed, rows.Err()
}

func doSQLCellText(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return "NULL"
	case []byte:
		return sanitizeSecretText(string(typed))
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		return sanitizeSecretText(fmt.Sprint(typed))
	}
}
