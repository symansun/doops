package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/user/doops/agent/internal/dosql/lexer"
	"github.com/user/doops/agent/internal/dosql/policy"
	"github.com/user/doops/agent/internal/dosql/recorder"
)

const (
	doSQLReadCanceledCategory = "dosql_read_canceled"
	doSQLDirectRowLimit       = 200
	doSQLDirectLockWait       = 5 * time.Second
	doSQLDirectStatementT     = 60 * time.Second
	doSQLMaxLockWait          = 5 * time.Minute
	doSQLMaxStatementT        = 30 * time.Minute
)

var errDoSQLReadCanceled = errors.New(doSQLReadCanceledCategory + ": PostgreSQL confirmed query cancellation (SQLSTATE 57014)")

func openDoSQLDirectDB(request doSQLDirectRequest) (*sql.DB, error) {
	if !request.ReadOnly || request.Target.Driver != "pgx" {
		return sql.Open(request.Target.Driver, request.Target.DSN)
	}
	config, err := pgx.ParseConfig(request.Target.DSN)
	if err != nil {
		return nil, err
	}
	config.BuildContextWatcherHandler = func(conn *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: conn, DeadlineDelay: 5 * time.Second}
	}
	return stdlib.OpenDB(*config), nil
}

type doSQLDirectRequest struct {
	Target           doSQLTarget
	Statements       []string
	ParsedStatements []lexer.Statement
	Verification     string
	ReadOnly         bool
	DryRun           bool
	LockTimeout      time.Duration
	StatementTimeout time.Duration
	PolicyReport     *policy.Report
	OnStatement      func(recorder.StatementRecord)
}

type doSQLStatementResult struct {
	Statement    string     `json:"statement"`
	Kind         string     `json:"kind,omitempty"`
	SegmentIndex int        `json:"segment_index,omitempty"`
	AffectedRows int64      `json:"affected_rows,omitempty"`
	Columns      []string   `json:"columns,omitempty"`
	Rows         [][]string `json:"rows,omitempty"`
	Truncated    bool       `json:"truncated,omitempty"`
	DurationMS   int64      `json:"duration_ms,omitempty"`
}

type doSQLSegmentResult struct {
	Index      int    `json:"index"`
	Kind       string `json:"kind"`
	Atomic     bool   `json:"atomic"`
	Committed  bool   `json:"committed"`
	Statements int    `json:"statements"`
}

type doSQLDirectResult struct {
	Status          string                 `json:"status"`
	Committed       bool                   `json:"committed"`
	ImplicitCommits int                    `json:"implicit_commits,omitempty"`
	Applied         int                    `json:"applied,omitempty"`
	Total           int                    `json:"total,omitempty"`
	AuthSource      string                 `json:"auth_source,omitempty"`
	Segments        []doSQLSegmentResult   `json:"segments,omitempty"`
	Statements      []doSQLStatementResult `json:"statements"`
	Verification    *doSQLStatementResult  `json:"verification,omitempty"`
	Policy          *policy.Report         `json:"policy,omitempty"`
	DurationMS      int64                  `json:"duration_ms"`
}

type sqlQueryExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// executeDoSQLDirect executes statements using automatic segmentation:
//   - Session SETs run on the connection so they apply to subsequent segments.
//   - Implicit-commit DDL (MySQL/Oracle) and non-transactional PG DDL (CONCURRENTLY/VACUUM)
//     run directly on the connection with truthful Applied/Total & Committed reporting.
//   - Consecutive transactional statements (DML on MySQL, DDL+DML on PG/MSSQL) run
//     atomically inside one transaction.
func executeDoSQLDirect(ctx context.Context, request doSQLDirectRequest) (doSQLDirectResult, error) {
	started := time.Now()
	result := doSQLDirectResult{
		Status: "failed",
		Total:  len(request.Statements),
		Policy: request.PolicyReport,
	}
	if len(request.Statements) == 0 {
		return result, errors.New("dosql_statement_required")
	}
	if request.Target.Engine == "mongodb" {
		return executeDoSQLMongo(ctx, request)
	}
	if request.Target.Driver == doSQLExternalDriver {
		return executeDoSQLExternal(ctx, request)
	}

	parsed := request.ParsedStatements
	if len(parsed) != len(request.Statements) {
		parsed = make([]lexer.Statement, len(request.Statements))
		for i, stmt := range request.Statements {
			split, err := lexer.Split(request.Target.Engine, stmt)
			if err != nil || len(split) == 0 {
				parsed[i] = lexer.Statement{
					Index:          i + 1,
					Raw:            stmt,
					Executable:     stmt,
					Kind:           lexer.KindOther,
					ImplicitCommit: doSQLCommitsImplicitly(request.Target.Engine, stmt),
					Hash:           lexer.StatementHash(stmt),
				}
			} else {
				split[0].Index = i + 1
				parsed[i] = split[0]
			}
		}
	}

	if request.Target.Engine == "mysql" {
		checkStatements := append([]string(nil), request.Statements...)
		if strings.TrimSpace(request.Verification) != "" {
			checkStatements = append(checkStatements, request.Verification)
		}
		for index, statement := range checkStatements {
			words, err := doSQLMySQLStatementWords(statement)
			if err != nil {
				result.Status = "rejected"
				return result, fmt.Errorf("%w: statement %d", err, index+1)
			}
			if request.DryRun && !doSQLMySQLDryRunSafe(words) && !doSQLCommitsImplicitly("mysql", statement) {
				result.Status = "rejected"
				return result, fmt.Errorf("dosql_dry_run_statement_unsupported: statement %d cannot be guaranteed to roll back", index+1)
			}
			if request.DryRun && index == len(request.Statements) && doSQLCommitsImplicitly("mysql", statement) {
				result.Status = "rejected"
				return result, errors.New("dosql_verification_invalid: verification cannot commit the transaction")
			}
		}
	}

	segments := lexer.PlanSegments(parsed)
	if request.DryRun {
		for _, stmt := range parsed {
			if stmt.ImplicitCommit || doSQLCommitsImplicitly(request.Target.Engine, stmt.Executable) {
				result.Status = "rejected"
				return result, fmt.Errorf("dosql_dry_run_ddl_unsupported: statement %d is DDL and %s commits DDL implicitly, so a dry run cannot roll it back; keep the authorized target/database and execute DDL separately without --dry-run only within the authorized change scope", stmt.Index, request.Target.Engine)
			}
			if stmt.NonTransaction {
				result.Status = "rejected"
				return result, fmt.Errorf("dosql_dry_run_statement_unsupported: statement %d cannot run inside a rollbackable transaction", stmt.Index)
			}
		}
	}

	db, err := openDoSQLDirectDB(request)
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

	lockWait := request.LockTimeout
	if lockWait <= 0 {
		lockWait = doSQLDirectLockWait
	}
	stmtTimeout := request.StatementTimeout
	if stmtTimeout <= 0 {
		stmtTimeout = doSQLDirectStatementT
	}
	if err := prepareDoSQLSessionWithTimeouts(ctx, conn, request.Target.Engine, lockWait, stmtTimeout); err != nil {
		return result, doSQLFailure("dosql_session_setup_failed", err.Error())
	}

	recordStmt := func(seg lexer.Segment, stmt lexer.Statement, res doSQLStatementResult, execErr error) {
		if request.OnStatement == nil {
			return
		}
		status := "succeeded"
		errText := ""
		if execErr != nil {
			status = "failed"
			errText = sanitizeSecretText(execErr.Error())
		}
		request.OnStatement(recorder.StatementRecord{
			Engine:       request.Target.Engine,
			Database:     request.Target.Database,
			Kind:         string(stmt.Kind),
			SegmentIndex: seg.Index,
			Atomic:       seg.Atomic,
			Statement:    sanitizeSecretText(stmt.Executable),
			StmtHash:     stmt.Hash,
			AffectedRows: res.AffectedRows,
			DurationMS:   res.DurationMS,
			Status:       status,
			Error:        errText,
		})
	}

	// Fast-path: pure read-only or single transactional segment (preserves exact
	// verification-in-same-tx rollback semantics!).
	if request.ReadOnly || request.DryRun || (len(segments) == 1 && segments[0].Kind == lexer.SegmentTransaction) {
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
		seg := lexer.Segment{Index: 1, Kind: lexer.SegmentTransaction, Atomic: true, Statements: parsed}
		for _, stmt := range parsed {
			stmtStart := time.Now()
			executed, err := runDoSQLStatementOn(ctx, tx, stmt.Executable)
			executed.Kind = string(stmt.Kind)
			executed.SegmentIndex = seg.Index
			executed.DurationMS = time.Since(stmtStart).Milliseconds()
			recordStmt(seg, stmt, executed, err)
			if err != nil {
				if isPGReadCanceled(request, ctx, err) {
					result.Status = "canceled"
					return result, errDoSQLReadCanceled
				}
				return result, doSQLFailure("dosql_statement_failed", fmt.Sprintf("statement %d: %v%s", stmt.Index, err, partialCommitNote(&result)))
			}
			result.Statements = append(result.Statements, executed)
			result.Applied++
		}
		if strings.TrimSpace(request.Verification) != "" {
			verified, err := runDoSQLStatementOn(ctx, tx, request.Verification)
			if err != nil {
				return result, doSQLFailure("dosql_verification_failed", err.Error()+partialCommitNote(&result))
			}
			result.Verification = &verified
		}
		result.DurationMS = time.Since(started).Milliseconds()
		if request.ReadOnly || request.DryRun {
			result.Status = "verified"
			if request.DryRun {
				result.Status = "dry_run_rolled_back"
			}
			result.Segments = []doSQLSegmentResult{{Index: 1, Kind: string(seg.Kind), Atomic: true, Committed: false, Statements: len(parsed)}}
			return result, nil
		}
		if err := tx.Commit(); err != nil {
			return result, doSQLFailure("dosql_commit_failed", err.Error()+partialCommitNote(&result))
		}
		committed = true
		result.Committed = true
		result.Status = "verified"
		result.Segments = []doSQLSegmentResult{{Index: 1, Kind: string(seg.Kind), Atomic: true, Committed: true, Statements: len(parsed)}}
		return result, nil
	}

	// Multi-segment execution (e.g. session SET + MySQL DDL + DML backfill, or PG CONCURRENTLY + DML).
	for _, seg := range segments {
		segRes := doSQLSegmentResult{
			Index:      seg.Index,
			Kind:       string(seg.Kind),
			Atomic:     seg.Atomic,
			Statements: len(seg.Statements),
		}
		if seg.Kind == lexer.SegmentTransaction {
			tx, err := beginDoSQLTransaction(ctx, conn, request.Target.Engine, false)
			if err != nil {
				return result, doSQLFailure("dosql_transaction_failed", err.Error()+partialCommitNote(&result))
			}
			txCommitted := false
			for _, stmt := range seg.Statements {
				stmtStart := time.Now()
				executed, err := runDoSQLStatementOn(ctx, tx, stmt.Executable)
				executed.Kind = string(stmt.Kind)
				executed.SegmentIndex = seg.Index
				executed.DurationMS = time.Since(stmtStart).Milliseconds()
				recordStmt(seg, stmt, executed, err)
				if err != nil {
					_ = tx.Rollback()
					return result, doSQLFailure("dosql_statement_failed", fmt.Sprintf("statement %d (segment %d): %v%s", stmt.Index, seg.Index, err, partialCommitNote(&result)))
				}
				result.Statements = append(result.Statements, executed)
				result.Applied++
			}
			if err := tx.Commit(); err != nil {
				_ = tx.Rollback()
				return result, doSQLFailure("dosql_commit_failed", err.Error()+partialCommitNote(&result))
			}
			txCommitted = true
			segRes.Committed = txCommitted
			result.Committed = true
			result.Segments = append(result.Segments, segRes)
			continue
		}

		// Direct on connection: SegmentSession, SegmentImplicitCommit, SegmentNonTransaction.
		for _, stmt := range seg.Statements {
			stmtStart := time.Now()
			executed, err := runDoSQLStatementOn(ctx, conn, stmt.Executable)
			executed.Kind = string(stmt.Kind)
			executed.SegmentIndex = seg.Index
			executed.DurationMS = time.Since(stmtStart).Milliseconds()
			recordStmt(seg, stmt, executed, err)
			if err != nil {
				return result, doSQLFailure("dosql_statement_failed", fmt.Sprintf("statement %d (segment %d): %v%s", stmt.Index, seg.Index, err, partialCommitNote(&result)))
			}
			result.Statements = append(result.Statements, executed)
			result.Applied++
			if stmt.ImplicitCommit || seg.Kind == lexer.SegmentNonTransaction {
				result.ImplicitCommits++
				result.Committed = true
				result.Status = "partially_committed"
			}
		}
		segRes.Committed = seg.Kind != lexer.SegmentSession
		result.Segments = append(result.Segments, segRes)
	}

	if strings.TrimSpace(request.Verification) != "" {
		verified, err := runDoSQLStatementOn(ctx, conn, request.Verification)
		if err != nil {
			return result, doSQLFailure("dosql_verification_failed", err.Error()+partialCommitNote(&result))
		}
		result.Verification = &verified
	}
	result.DurationMS = time.Since(started).Milliseconds()
	result.Committed = true
	result.Status = "verified"
	return result, nil
}

func isPGReadCanceled(request doSQLDirectRequest, ctx context.Context, err error) bool {
	var pgErr *pgconn.PgError
	return request.ReadOnly && request.Target.Driver == "pgx" && ctx.Err() == context.Canceled &&
		errors.As(err, &pgErr) && pgErr.Code == "57014" && pgErr.Routine == "ProcessInterrupts" &&
		pgErr.Message == "canceling statement due to user request"
}

func partialCommitNote(result *doSQLDirectResult) string {
	if result.ImplicitCommits == 0 && !result.Committed {
		return ""
	}
	if result.ImplicitCommits > 0 {
		return fmt.Sprintf(" (dosql_ddl_already_committed: %d statement(s) committed implicitly and are not rolled back; applied=%d/%d)", result.ImplicitCommits, result.Applied, result.Total)
	}
	return fmt.Sprintf(" (dosql_partial_segments_committed: applied=%d/%d)", result.Applied, result.Total)
}

func prepareDoSQLSession(ctx context.Context, conn *sql.Conn, engine string) error {
	return prepareDoSQLSessionWithTimeouts(ctx, conn, engine, doSQLDirectLockWait, doSQLDirectStatementT)
}

func prepareDoSQLSessionWithTimeouts(ctx context.Context, conn *sql.Conn, engine string, lockWait, stmtTimeout time.Duration) error {
	lockSec := int(lockWait / time.Second)
	if lockSec < 1 {
		lockSec = 1
	}
	stmtSec := int(stmtTimeout / time.Second)
	if stmtSec < 1 {
		stmtSec = 1
	}
	var statements []string
	switch engine {
	case "postgres":
		statements = []string{
			fmt.Sprintf("SET lock_timeout = '%ds'", lockSec),
			fmt.Sprintf("SET statement_timeout = '%ds'", stmtSec),
		}
	case "mysql":
		// Protect against both InnoDB row locks AND metadata locks (MDL defaults to 1 year!).
		statements = []string{
			fmt.Sprintf("SET SESSION innodb_lock_wait_timeout = %d", lockSec),
			fmt.Sprintf("SET SESSION lock_wait_timeout = %d", lockSec),
		}
	case "oracle":
		statements = []string{fmt.Sprintf("ALTER SESSION SET DDL_LOCK_TIMEOUT = %d", lockSec)}
	case "mssql":
		statements = []string{fmt.Sprintf("SET LOCK_TIMEOUT %d", lockSec*1000)}
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func beginDoSQLTransaction(ctx context.Context, conn *sql.Conn, engine string, readOnly bool) (*sql.Tx, error) {
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
	return conn.BeginTx(ctx, doSQLTxOptions(engine, readOnly))
}

func runDoSQLStatement(ctx context.Context, tx *sql.Tx, statement string) (doSQLStatementResult, error) {
	return runDoSQLStatementOn(ctx, tx, statement)
}

func runDoSQLStatementOn(ctx context.Context, exec sqlQueryExecutor, statement string) (doSQLStatementResult, error) {
	executed := doSQLStatementResult{Statement: statement}
	if !doSQLReadOnlyStatement(statement) {
		outcome, err := exec.ExecContext(ctx, statement)
		if err != nil {
			return executed, err
		}
		executed.AffectedRows, _ = outcome.RowsAffected()
		return executed, nil
	}
	rows, err := exec.QueryContext(ctx, statement)
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
		return redactSecretText(string(typed))
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		return redactSecretText(fmt.Sprint(typed))
	}
}
