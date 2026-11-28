package server

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// AuditScrubExit* are the exit codes of "doops-gateway store audit-scrub".
// They intentionally match store mysql-copy: 0 success, 1 runtime failure,
// 2 usage or precondition failure.
const (
	AuditScrubExitSuccess = 0
	AuditScrubExitFailure = 1
	AuditScrubExitUsage   = 2
)

const auditScrubBatchSize = 200

// AuditScrubOptions is the command-level input. The DSN is deliberately absent:
// it is read from DOOPS_GATEWAY_MYSQL_DSN only so it can never be captured in
// shell history or a process listing.
type AuditScrubOptions struct {
	ConfirmDatabase string
	BackupPath      string
	Actions         string
	DryRun          bool
	Rollback        bool
}

var (
	auditScrubCommandSummaryPattern = regexp.MustCompile("^[A-Z]{3,7} /")
	auditScrubErrorURLPattern       = regexp.MustCompile("(?i)[a-z][a-z0-9+.-]*://[^\\s\"'<>]+")
	auditScrubAllowedActions        = map[string]bool{"proxy": true, "pull": true, "push": true}
)

// auditScrubErrorSQLPattern is the MySQL REGEXP used to pre-filter error rows.
// The Go pattern above decides the exact replacement after the row is read.
// Every scheme:// token is selected, including a clean URL and non-http(s)
// historical targets (postgres://, redis://, mysql://): the runtime scrubber
// also replaces a token it cannot parse, so selecting only userinfo/query/
// fragment shapes would leave those rows behind. Using explicit [A-Za-z]
// instead of (?i) keeps the pattern valid on both MySQL 5.7 (POSIX ERE, where
// (?i) raises ERROR 1139) and MySQL 8.0/8.4 (ICU), including case-sensitive
// utf8mb4_bin collations.
const auditScrubErrorSQLPattern = "[A-Za-z][A-Za-z0-9+.-]*://[^[:space:]\"<>]+"

func auditScrubCommandSummaryNeedsReview(summary string) bool {
	return auditScrubCommandSummaryPattern.MatchString(summary) &&
		strings.Contains(summary, "?") &&
		!strings.HasSuffix(summary, "?<redacted>")
}

type auditScrubRow struct {
	ID             int64
	Action         string
	CommandSummary string
	Error          string
}

type auditScrubChange struct {
	ID     int64
	Column string
	Old    string
	New    string
}

type auditScrubBackupEntry struct {
	ID     int64
	Column string
	Old    string
}

type auditScrubStats struct {
	CommandCandidates int64
	CommandRewrites   int64
	ErrorCandidates   int64
	ErrorRewrites     int64
	Rows              int64
	Batches           int64
}

func (s *auditScrubStats) add(other auditScrubStats) {
	s.CommandCandidates += other.CommandCandidates
	s.CommandRewrites += other.CommandRewrites
	s.ErrorCandidates += other.ErrorCandidates
	s.ErrorRewrites += other.ErrorRewrites
	s.Rows += other.Rows
}

type auditScrubQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type auditScrubRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// auditScrubColumnAllowed reports whether an entry may rewrite the named column.
// The apply path builds the name itself, while the rollback path reads it out of a
// backup file, so both sides check it before interpolating it into SQL.
func auditScrubColumnAllowed(column string) bool {
	return column == "command_summary" || column == "error"
}

// auditScrubRowHolds reports whether row id still exists and already holds want in
// column. MySQL reports zero affected rows for an UPDATE that writes the value the
// row already stores, so affected==0 alone cannot tell "this scrub already ran"
// apart from "the row is gone".
func auditScrubRowHolds(ctx context.Context, q auditScrubRowQueryer, id int64, column, want string) (bool, error) {
	if !auditScrubColumnAllowed(column) {
		return false, fmt.Errorf("refuse to read unknown audit column %q", column)
	}
	var got sql.NullString
	err := q.QueryRowContext(ctx, "SELECT "+column+" FROM audit_events WHERE id = ?", id).Scan(&got)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return got.Valid && got.String == want, nil
}

// RunAuditScrub is the testable implementation behind
// "doops-gateway store audit-scrub". It never accepts a DSN from a flag and
// never prints the DSN or a pre-image value.
func RunAuditScrub(ctx context.Context, opts AuditScrubOptions, out io.Writer) (int, error) {
	var actions []string
	if !opts.Rollback {
		var err error
		actions, err = auditScrubParseActions(opts.Actions)
		if err != nil {
			return AuditScrubExitUsage, err
		}
	}
	if strings.TrimSpace(opts.BackupPath) == "" {
		return AuditScrubExitUsage, errors.New("-backup is required: the JSONL pre-image is written before any row is changed")
	}
	if opts.DryRun && opts.Rollback {
		return AuditScrubExitUsage, errors.New("-dry-run and -rollback are mutually exclusive")
	}

	rawDSN := strings.TrimSpace(os.Getenv("DOOPS_GATEWAY_MYSQL_DSN"))
	if rawDSN == "" {
		return AuditScrubExitUsage, errors.New("DOOPS_GATEWAY_MYSQL_DSN is not set; the DSN is never a command-line argument")
	}
	cfg, err := mysql.ParseDSN(rawDSN)
	if err != nil || cfg.DBName == "" {
		// Never include the parse error: some driver errors echo the input.
		return AuditScrubExitUsage, errors.New("DOOPS_GATEWAY_MYSQL_DSN is not a valid MySQL DSN with a database name")
	}
	if opts.ConfirmDatabase != cfg.DBName {
		return AuditScrubExitUsage, fmt.Errorf("confirm-database %q does not match the destination database %q", opts.ConfirmDatabase, cfg.DBName)
	}

	db, err := sql.Open("mysql", rawDSN)
	if err != nil {
		return AuditScrubExitFailure, errors.New("could not open the destination database")
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, mysqlPingTimeout())
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return AuditScrubExitFailure, fmt.Errorf("destination database is unreachable: %v", err)
	}
	if err := auditScrubCheckSchema(ctx, db); err != nil {
		return AuditScrubExitUsage, err
	}

	if opts.Rollback {
		return auditScrubRollback(ctx, db, opts.BackupPath, out)
	}
	if opts.DryRun {
		return auditScrubDryRun(ctx, db, actions, out)
	}
	return auditScrubApply(ctx, db, actions, opts.BackupPath, out)
}

func auditScrubParseActions(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("actions must name at least one of proxy, pull, push")
	}
	seen := map[string]bool{}
	var actions []string
	for _, part := range strings.Split(raw, ",") {
		action := strings.TrimSpace(part)
		if action == "" {
			return nil, errors.New("actions contains an empty name")
		}
		if !auditScrubAllowedActions[action] {
			// This is what rejects "*" and any whole-table shortcut.
			return nil, fmt.Errorf("unsupported action %q; allowed actions are proxy, pull, push", action)
		}
		if !seen[action] {
			seen[action] = true
			actions = append(actions, action)
		}
	}
	return actions, nil
}

func auditScrubCheckSchema(ctx context.Context, db *sql.DB) error {
	var one int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM audit_events LIMIT 1").Scan(&one)
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return fmt.Errorf("destination audit_events is not ready (%v); start the Gateway once with DOOPS_GATEWAY_STORE=mysql to install the native schema", err)
}

func auditScrubCandidateQuery(actions []string) string {
	marks := make([]string, len(actions))
	for i := range marks {
		marks[i] = "?"
	}
	return "SELECT id, action, command_summary, error FROM audit_events " +
		"WHERE id > ? AND ((action IN (" + strings.Join(marks, ",") + ") AND command_summary REGEXP ? AND command_summary LIKE ? AND command_summary NOT REGEXP ?) OR error REGEXP ?) " +
		"ORDER BY id LIMIT ?"
}

func auditScrubReadCandidates(ctx context.Context, q auditScrubQueryer, actions []string, afterID int64, limit int) ([]auditScrubRow, error) {
	args := make([]any, 0, len(actions)+6)
	args = append(args, afterID)
	for _, action := range actions {
		args = append(args, action)
	}
	args = append(args, "^[A-Z]{3,7} /", "%?%", "\\?<redacted>$", auditScrubErrorSQLPattern, limit)
	rows, err := q.QueryContext(ctx, auditScrubCandidateQuery(actions), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditScrubRow
	for rows.Next() {
		var row auditScrubRow
		if err := rows.Scan(&row.ID, &row.Action, &row.CommandSummary, &row.Error); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func auditScrubBuildChanges(rows []auditScrubRow, actions []string) ([]auditScrubChange, auditScrubStats, error) {
	allowed := make(map[string]bool, len(actions))
	for _, action := range actions {
		allowed[action] = true
	}
	var changes []auditScrubChange
	var stats auditScrubStats
	for _, row := range rows {
		changedRow := false
		if allowed[row.Action] && auditScrubCommandSummaryNeedsReview(row.CommandSummary) {
			stats.CommandCandidates++
			value, changed, err := auditScrubCommandSummary(row.CommandSummary)
			if err != nil {
				return nil, stats, fmt.Errorf("audit row %d column command_summary: %w", row.ID, err)
			}
			if changed {
				changes = append(changes, auditScrubChange{ID: row.ID, Column: "command_summary", Old: row.CommandSummary, New: value})
				stats.CommandRewrites++
				changedRow = true
			}
		}
		if auditScrubErrorURLPattern.MatchString(row.Error) {
			value, changed, err := auditScrubErrorText(row.Error)
			if err != nil {
				return nil, stats, fmt.Errorf("audit row %d column error: %w", row.ID, err)
			}
			if changed {
				changes = append(changes, auditScrubChange{ID: row.ID, Column: "error", Old: row.Error, New: value})
				// A clean URL is selected by the broad pre-filter but changes
				// nothing; only a row this run would rewrite is a candidate.
				stats.ErrorCandidates++
				stats.ErrorRewrites++
				changedRow = true
			}
		}
		if changedRow {
			stats.Rows++
		}
	}
	return changes, stats, nil
}

// auditScrubCommandSummary keeps the method and path, and replaces the query
// with the literal marker. It fails closed instead of guessing when the request
// URI is malformed.
func auditScrubCommandSummary(summary string) (string, bool, error) {
	space := strings.IndexByte(summary, ' ')
	if space <= 0 || space == len(summary)-1 {
		return "", false, errors.New("missing request method or path")
	}
	method := summary[:space]
	rest := summary[space+1:]
	parsed, err := url.Parse(rest)
	if err != nil {
		return "", false, errors.New("cannot parse the request URI")
	}
	if parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil {
		return "", false, errors.New("request URI is not an absolute path")
	}
	if parsed.Path == "" || !strings.HasPrefix(parsed.Path, "/") {
		return "", false, errors.New("request URI has no absolute path")
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		path += "?<redacted>"
	}
	value := method + " " + path
	return value, value != summary, nil
}

// auditScrubErrorText removes userinfo, query and fragment from every scheme://
// URL embedded in an error string, keeping the surrounding text intact. It
// delegates the tokenization and parsing to the runtime audit scrubber so a
// future rule change cannot leave the historical command on a second copy.
//
// scrubURLCredentials maps a URL it cannot parse to "<unparsable-url>". The
// historical command is stricter: it fails that batch and names the row and
// column, because a migration rewrite must be reproducible and reviewable
// rather than silently replacing text an operator has not seen.
func auditScrubErrorText(text string) (string, bool, error) {
	value := scrubURLCredentials(text)
	if strings.Count(value, unparsableURLPlaceholder) > strings.Count(text, unparsableURLPlaceholder) {
		return "", false, errors.New("cannot parse an embedded URL")
	}
	return value, value != text, nil
}

func auditScrubDryRun(ctx context.Context, db *sql.DB, actions []string, out io.Writer) (int, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AuditScrubExitFailure, fmt.Errorf("start read-only transaction: %w", err)
	}
	defer tx.Rollback()
	var stats auditScrubStats
	afterID := int64(0)
	for {
		rows, err := auditScrubReadCandidates(ctx, tx, actions, afterID, auditScrubBatchSize)
		if err != nil {
			return AuditScrubExitFailure, fmt.Errorf("count audit scrub candidates: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		_, batch, err := auditScrubBuildChanges(rows, actions)
		if err != nil {
			return AuditScrubExitFailure, err
		}
		stats.add(batch)
		afterID = rows[len(rows)-1].ID
	}
	batches := int64(0)
	if stats.Rows > 0 {
		batches = (stats.Rows + auditScrubBatchSize - 1) / auditScrubBatchSize
	}
	fmt.Fprintf(out, "audit scrub plan (dry-run): actions=%s\n", strings.Join(actions, ","))
	fmt.Fprintf(out, "  command_summary candidates=%d rewrite=%d\n", stats.CommandCandidates, stats.CommandRewrites)
	fmt.Fprintf(out, "  error            candidates=%d rewrite=%d\n", stats.ErrorCandidates, stats.ErrorRewrites)
	fmt.Fprintf(out, "  estimated rows=%d batches=%d\n", stats.Rows, batches)
	fmt.Fprintln(out, "  dry-run wrote nothing")
	return AuditScrubExitSuccess, nil
}

func auditScrubApply(ctx context.Context, db *sql.DB, actions []string, backupPath string, out io.Writer) (int, error) {
	backup, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return AuditScrubExitFailure, fmt.Errorf("create backup %s: %w", backupPath, err)
	}
	defer backup.Close()

	receiptID, err := auditScrubOpenReceipt(ctx, db)
	if err != nil {
		return AuditScrubExitFailure, err
	}
	var stats auditScrubStats
	afterID := int64(0)
	for {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			auditScrubFailReceipt(ctx, db, receiptID, stats)
			return AuditScrubExitFailure, fmt.Errorf("start batch transaction: %w", err)
		}
		rows, err := auditScrubReadCandidates(ctx, tx, actions, afterID, auditScrubBatchSize)
		if err != nil {
			tx.Rollback()
			auditScrubFailReceipt(ctx, db, receiptID, stats)
			return AuditScrubExitFailure, fmt.Errorf("read audit scrub batch: %w", err)
		}
		if len(rows) == 0 {
			tx.Rollback()
			break
		}
		changes, batch, err := auditScrubBuildChanges(rows, actions)
		if err != nil {
			tx.Rollback()
			auditScrubFailReceipt(ctx, db, receiptID, stats)
			return AuditScrubExitFailure, err
		}
		if err := auditScrubAppendBackup(backup, changes); err != nil {
			tx.Rollback()
			auditScrubFailReceipt(ctx, db, receiptID, stats)
			return AuditScrubExitFailure, fmt.Errorf("write backup %s: %w", backupPath, err)
		}
		for _, change := range changes {
			if !auditScrubColumnAllowed(change.Column) {
				tx.Rollback()
				auditScrubFailReceipt(ctx, db, receiptID, stats)
				return AuditScrubExitFailure, fmt.Errorf("refuse to rewrite unknown audit column %q", change.Column)
			}
			res, err := tx.ExecContext(ctx, "UPDATE audit_events SET "+change.Column+" = ? WHERE id = ?", change.New, change.ID)
			if err != nil {
				tx.Rollback()
				auditScrubFailReceipt(ctx, db, receiptID, stats)
				return AuditScrubExitFailure, fmt.Errorf("update audit row %d column %s: %w", change.ID, change.Column, err)
			}
			affected, err := res.RowsAffected()
			if err != nil {
				tx.Rollback()
				auditScrubFailReceipt(ctx, db, receiptID, stats)
				return AuditScrubExitFailure, fmt.Errorf("confirm audit row %d column %s: %w", change.ID, change.Column, err)
			}
			if affected != 1 {
				// MySQL reports zero affected rows when the column already holds
				// change.New, so a second run of the same scrub must not be read as a
				// vanished row: only a missing row, or one holding another value, fails.
				already, matchErr := auditScrubRowHolds(ctx, tx, change.ID, change.Column, change.New)
				if matchErr != nil {
					tx.Rollback()
					auditScrubFailReceipt(ctx, db, receiptID, stats)
					return AuditScrubExitFailure, fmt.Errorf("confirm audit row %d column %s: %w", change.ID, change.Column, matchErr)
				}
				if !already {
					tx.Rollback()
					auditScrubFailReceipt(ctx, db, receiptID, stats)
					return AuditScrubExitFailure, fmt.Errorf("audit row %d column %s is no longer present", change.ID, change.Column)
				}
			}
		}
		stats.add(batch)
		stats.Batches++
		if _, err := tx.ExecContext(ctx, "UPDATE audit_events SET tail = ? WHERE id = ? AND action = 'audit:scrub'", auditScrubTail(stats), receiptID); err != nil {
			tx.Rollback()
			auditScrubFailReceipt(ctx, db, receiptID, stats)
			return AuditScrubExitFailure, fmt.Errorf("update audit scrub receipt: %w", err)
		}
		if err := tx.Commit(); err != nil {
			auditScrubFailReceipt(ctx, db, receiptID, stats)
			return AuditScrubExitFailure, fmt.Errorf("commit audit scrub batch: %w", err)
		}
		afterID = rows[len(rows)-1].ID
	}
	if err := auditScrubSucceedReceipt(ctx, db, receiptID, stats); err != nil {
		return AuditScrubExitFailure, err
	}
	fmt.Fprintf(out, "audit scrub complete: updated=%d batches=%d deleted=0\n", auditScrubChangedRows(stats), stats.Batches)
	fmt.Fprintf(out, "  command_summary candidates=%d rewrite=%d\n", stats.CommandCandidates, stats.CommandRewrites)
	fmt.Fprintf(out, "  error            candidates=%d rewrite=%d\n", stats.ErrorCandidates, stats.ErrorRewrites)
	fmt.Fprintf(out, "  backup=%s (0600; use -rollback -backup %s to restore)\n", backupPath, backupPath)
	return AuditScrubExitSuccess, nil
}

func auditScrubChangedRows(stats auditScrubStats) int64 {
	return stats.CommandRewrites + stats.ErrorRewrites
}

func auditScrubTail(stats auditScrubStats) string {
	data, _ := json.Marshal(map[string]int64{
		"deleted": 0,
		"updated": auditScrubChangedRows(stats),
		"batches": stats.Batches,
	})
	return string(data)
}

func auditScrubAppendBackup(backup *os.File, changes []auditScrubChange) error {
	encoder := json.NewEncoder(backup)
	for _, change := range changes {
		if err := encoder.Encode(auditScrubBackupEntry{ID: change.ID, Column: change.Column, Old: change.Old}); err != nil {
			return err
		}
	}
	return backup.Sync()
}

func auditScrubOpenReceipt(ctx context.Context, db *sql.DB) (int64, error) {
	now := time.Now().UTC()
	res, err := db.ExecContext(ctx, "INSERT INTO audit_events "+
		"(user_id, action, status, command_summary, error, tail, scope_json, started_at, started_at_ms, ended_at) "+
		"VALUES (?, 'audit:scrub', 'running', ?, '', '', '', ?, ?, '')",
		"audit-scrub", "store audit-scrub", formatTime(now), now.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("open audit scrub receipt: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("open audit scrub receipt: %w", err)
	}
	return id, nil
}

func auditScrubSucceedReceipt(ctx context.Context, db *sql.DB, receiptID int64, stats auditScrubStats) error {
	res, err := db.ExecContext(ctx, "UPDATE audit_events SET status = 'success', tail = ?, ended_at = ? "+
		"WHERE id = ? AND action = 'audit:scrub'", auditScrubTail(stats), formatTime(time.Now().UTC()), receiptID)
	if err != nil {
		return fmt.Errorf("complete audit scrub receipt: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete audit scrub receipt: %w", err)
	}
	if affected == 1 {
		return nil
	}
	return auditScrubConfirmReceiptStatus(ctx, db, receiptID, "success")
}

// auditScrubFailReceipt records a failure after any already committed batches.
// Like the retention receipt, audit:scrub starts as success only at the end, so
// this update has to reach a running row; it is guarded by the action name.
func auditScrubFailReceipt(ctx context.Context, db *sql.DB, receiptID int64, stats auditScrubStats) {
	_, _ = db.ExecContext(ctx, "UPDATE audit_events SET status = 'error', error = 'audit_scrub_failed', tail = ?, ended_at = ? "+
		"WHERE id = ? AND action = 'audit:scrub'", auditScrubTail(stats), formatTime(time.Now().UTC()), receiptID)
}

func auditScrubConfirmReceiptStatus(ctx context.Context, db *sql.DB, receiptID int64, want string) error {
	var status string
	if err := db.QueryRowContext(ctx, "SELECT status FROM audit_events WHERE id = ? AND action = 'audit:scrub'", receiptID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("audit scrub receipt %d is no longer present", receiptID)
		}
		return fmt.Errorf("confirm audit scrub receipt %d: %w", receiptID, err)
	}
	if status != want {
		return fmt.Errorf("audit scrub receipt %d is %s, requested %s", receiptID, status, want)
	}
	return nil
}

func auditScrubRollback(ctx context.Context, db *sql.DB, backupPath string, out io.Writer) (int, error) {
	info, err := os.Stat(backupPath)
	if err != nil {
		return AuditScrubExitUsage, fmt.Errorf("read backup %s: %w", backupPath, err)
	}
	if info.Mode().Perm()&0077 != 0 {
		return AuditScrubExitUsage, fmt.Errorf("backup %s must have mode 0600", backupPath)
	}
	file, err := os.Open(backupPath)
	if err != nil {
		return AuditScrubExitUsage, fmt.Errorf("read backup %s: %w", backupPath, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var entries []auditScrubBackupEntry
	for scanner.Scan() {
		var entry auditScrubBackupEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return AuditScrubExitUsage, fmt.Errorf("backup %s has an invalid JSONL line: %w", backupPath, err)
		}
		if entry.ID <= 0 || (entry.Column != "command_summary" && entry.Column != "error") {
			return AuditScrubExitUsage, fmt.Errorf("backup %s has an invalid entry", backupPath)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return AuditScrubExitUsage, fmt.Errorf("read backup %s: %w", backupPath, err)
	}
	restored := 0
	for end := len(entries); end > 0; {
		start := end - auditScrubBatchSize
		if start < 0 {
			start = 0
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return AuditScrubExitFailure, fmt.Errorf("start rollback transaction: %w", err)
		}
		for i := end - 1; i >= start; i-- {
			entry := entries[i]
			res, err := tx.ExecContext(ctx, "UPDATE audit_events SET "+entry.Column+" = ? WHERE id = ?", entry.Old, entry.ID)
			if err != nil {
				tx.Rollback()
				return AuditScrubExitFailure, fmt.Errorf("restore audit row %d column %s: %w", entry.ID, entry.Column, err)
			}
			affected, err := res.RowsAffected()
			if err != nil {
				tx.Rollback()
				return AuditScrubExitFailure, fmt.Errorf("restore audit row %d column %s: %w", entry.ID, entry.Column, err)
			}
			if affected != 1 {
				// The same MySQL zero-affected rule applies here: rolling back twice
				// restores the value that is already stored, which is success.
				already, matchErr := auditScrubRowHolds(ctx, tx, entry.ID, entry.Column, entry.Old)
				if matchErr != nil {
					tx.Rollback()
					return AuditScrubExitFailure, fmt.Errorf("restore audit row %d column %s: %w", entry.ID, entry.Column, matchErr)
				}
				if !already {
					tx.Rollback()
					return AuditScrubExitFailure, fmt.Errorf("audit row %d column %s is no longer present", entry.ID, entry.Column)
				}
			}
			restored++
		}
		if err := tx.Commit(); err != nil {
			return AuditScrubExitFailure, fmt.Errorf("commit rollback batch: %w", err)
		}
		end = start
	}
	fmt.Fprintf(out, "audit scrub rollback complete: restored=%d\n", restored)
	return AuditScrubExitSuccess, nil
}
