package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/user/doops/agent/api"
)

// Copying moves whole tables between two databases bound to the same target, so
// the caller never has to page rows through the result limit and re-submit them
// as INSERT text. Both sides must use the same engine; the destination tables
// must already exist. All inserts share one destination transaction: a failure,
// or --dry-run, leaves the destination untouched.
//
// Only the engines' own protocol limits apply: one INSERT carries at most
// doSQLCopyMaxParams placeholders (both drivers cap at 65535).
const (
	doSQLCopyBatchRows = 500
	doSQLCopyMaxParams = 60000
)

func doSQLCopyRequested(args api.AgentPromptParams) bool {
	return strings.TrimSpace(args.Skill) == "dosql" && strings.TrimSpace(args.SQLCopyFrom) != ""
}

func validateDoSQLCopyTables(tables []string) ([]string, error) {
	if len(tables) == 0 {
		return nil, errors.New("dosql_copy_tables_required: pass --tables a,b")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(tables))
	for _, table := range tables {
		table = strings.TrimSpace(table)
		// Identifiers are quoted with escaping, so any real table name is safe;
		// only an empty name or control characters cannot be one.
		if table == "" || strings.ContainsFunc(table, unicode.IsControl) {
			return nil, fmt.Errorf("dosql_copy_table_invalid: %q is not a table name", table)
		}
		if seen[table] {
			continue
		}
		seen[table] = true
		out = append(out, table)
	}
	return out, nil
}

func doSQLQuoteIdentifier(engine, name string) string {
	if engine == "mysql" {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func doSQLCopyInsert(engine, table string, columns []string, rows int) string {
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = doSQLQuoteIdentifier(engine, column)
	}
	var builder strings.Builder
	builder.WriteString("INSERT INTO " + doSQLQuoteIdentifier(engine, table) + " (" + strings.Join(quoted, ", ") + ") VALUES ")
	n := 0
	for r := 0; r < rows; r++ {
		if r > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString("(")
		for c := range columns {
			if c > 0 {
				builder.WriteString(", ")
			}
			n++
			if engine == "postgres" {
				builder.WriteString(fmt.Sprintf("$%d", n))
			} else {
				builder.WriteString("?")
			}
		}
		builder.WriteString(")")
	}
	return builder.String()
}

func handleDoSQLCopy(ctx context.Context, sessionID string, args api.AgentPromptParams) (string, error) {
	if strings.ToLower(strings.TrimSpace(args.Operation)) != "apply" {
		return "", errors.New("dosql_copy_requires_apply: copying writes the destination, use --mode apply")
	}
	tables, err := validateDoSQLCopyTables(args.SQLCopyTables)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.SQLDatabase) == "" {
		return "", errors.New("dosql_copy_destination_required: pass --database <destination>")
	}
	if strings.TrimSpace(args.SQLDatabase) == strings.TrimSpace(args.SQLCopyFrom) {
		return "", errors.New("dosql_copy_same_database: source and destination are the same")
	}
	source, err := resolveDirectDoSQLBinding(ctx, args.CredentialBindings, args.SQLCopyFrom, readDoSQLSecretData)
	if err != nil {
		return "", fmt.Errorf("source: %w", err)
	}
	dest, err := resolveDirectDoSQLBinding(ctx, args.CredentialBindings, args.SQLDatabase, readDoSQLSecretData)
	if err != nil {
		return "", fmt.Errorf("destination: %w", err)
	}
	if source.Engine != dest.Engine || (dest.Engine != "mysql" && dest.Engine != "postgres") {
		return "", fmt.Errorf("dosql_copy_engine_unsupported: copy needs mysql→mysql or postgres→postgres, got %s→%s", source.Engine, dest.Engine)
	}
	sourceTarget, err := doSQLConnectionTarget(source.Engine, source.Database, source.Data)
	if err != nil {
		return "", err
	}
	destTarget, err := doSQLConnectionTarget(dest.Engine, dest.Database, dest.Data)
	if err != nil {
		return "", err
	}
	statements := make([]string, len(tables))
	for i, table := range tables {
		statements[i] = fmt.Sprintf("COPY %s.%s -> %s.%s", source.Database, table, dest.Database, table)
	}
	report := doSQLDirectReport{
		OperationID: "dbop_" + randomHex(8), Mode: "copy", Engine: dest.Engine,
		Database: dest.Database, Credential: dest.Binding.Name + "<-" + source.Binding.Name,
		StatementFingerprint: "sha256:" + doSQLStatementFingerprint(statements),
	}
	if args.SQLDryRun {
		report.Mode = "copy_dry_run"
	}
	journalPath := doSQLVersionJournalPath(dest.Engine, dest.Database)
	if !args.SQLDryRun {
		version, err := beginDoSQLVersion(journalPath, report.OperationID, report.Credential, report.StatementFingerprint, statements)
		if err != nil {
			return "", fmt.Errorf("%w (operation_id=%s): nothing was executed", err, report.OperationID)
		}
		report.Version = version
	}
	result, execErr := executeDoSQLCopy(ctx, sourceTarget, destTarget, tables, args.SQLDryRun)
	report.Result = result
	if report.Version != 0 {
		if err := endDoSQLVersion(journalPath, report.Version, report.OperationID, report.Credential, report.StatementFingerprint, result, execErr); err != nil {
			report.VersionRecordError = err.Error()
		}
	}
	report.EvidencePath = writeDirectDoSQLEvidence(sessionID, report, execErr)
	if execErr != nil {
		return "", fmt.Errorf("%w (operation_id=%s%s)", execErr, report.OperationID, doSQLVersionSuffix(report))
	}
	return marshalDoSQLReport(report)
}

func marshalDoSQLReport(report doSQLDirectReport) (string, error) {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", errors.New("dosql_result_encode_failed")
	}
	return string(encoded), nil
}

func executeDoSQLCopy(ctx context.Context, source, dest doSQLTarget, tables []string, dryRun bool) (doSQLDirectResult, error) {
	started := time.Now()
	result := doSQLDirectResult{Status: "failed"}
	srcDB, err := sql.Open(source.Driver, source.DSN)
	if err != nil {
		return result, doSQLFailure("dosql_connect_failed", "source: "+err.Error())
	}
	defer srcDB.Close()
	dstDB, err := sql.Open(dest.Driver, dest.DSN)
	if err != nil {
		return result, doSQLFailure("dosql_connect_failed", "destination: "+err.Error())
	}
	defer dstDB.Close()
	dstDB.SetMaxOpenConns(1)
	dstConn, err := dstDB.Conn(ctx)
	if err != nil {
		return result, doSQLFailure("dosql_connect_failed", "destination: "+err.Error())
	}
	defer dstConn.Close()
	if err := prepareDoSQLSession(ctx, dstConn, dest.Engine); err != nil {
		return result, doSQLFailure("dosql_session_setup_failed", err.Error())
	}
	srcTx, err := srcDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, doSQLFailure("dosql_transaction_failed", "source: "+err.Error())
	}
	defer srcTx.Rollback()
	dstTx, err := beginDoSQLTransaction(ctx, dstConn, dest.Engine, false)
	if err != nil {
		return result, doSQLFailure("dosql_transaction_failed", "destination: "+err.Error())
	}
	committed := false
	defer func() {
		if !committed {
			_ = dstTx.Rollback()
		}
	}()
	for _, table := range tables {
		copied, err := copyDoSQLTable(ctx, srcTx, dstTx, dest.Engine, table)
		if err != nil {
			return result, doSQLFailure("dosql_copy_failed", fmt.Sprintf("table %s: %v", table, err))
		}
		result.Statements = append(result.Statements, doSQLStatementResult{
			Statement: fmt.Sprintf("COPY %s", table), AffectedRows: copied,
		})
	}
	result.DurationMS = time.Since(started).Milliseconds()
	if dryRun {
		result.Status = "dry_run_rolled_back"
		return result, nil
	}
	if err := dstTx.Commit(); err != nil {
		return result, doSQLFailure("dosql_commit_failed", err.Error())
	}
	committed = true
	result.Committed = true
	result.Status = "verified"
	return result, nil
}

func copyDoSQLTable(ctx context.Context, src, dst *sql.Tx, engine, table string) (int64, error) {
	rows, err := src.QueryContext(ctx, "SELECT * FROM "+doSQLQuoteIdentifier(engine, table))
	if err != nil {
		return 0, fmt.Errorf("read source: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	if len(columns) == 0 {
		return 0, errors.New("table has no columns")
	}
	batchRows := doSQLCopyMaxParams / len(columns)
	if batchRows > doSQLCopyBatchRows {
		batchRows = doSQLCopyBatchRows
	}
	if batchRows < 1 {
		return 0, errors.New("table has too many columns")
	}
	var copied int64
	args := make([]interface{}, 0, batchRows*len(columns))
	pending := 0
	flush := func() error {
		if pending == 0 {
			return nil
		}
		if _, err := dst.ExecContext(ctx, doSQLCopyInsert(engine, table, columns, pending), args...); err != nil {
			return fmt.Errorf("write destination: %w", err)
		}
		copied += int64(pending)
		args, pending = args[:0], 0
		return nil
	}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		pointers := make([]interface{}, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return 0, err
		}
		args = append(args, values...)
		pending++
		if pending == batchRows {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := flush(); err != nil {
		return 0, err
	}
	return copied, nil
}
