package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Only counts and a digest of the verified, typed contents cross the copy boundary.
type gatewayMySQLCopyReport struct {
	TableRows map[string]int64
	SHA256    string
	// LegacyTablesMissing names frozen legacy tables the source no longer has.
	// They are left empty at the destination and reported, never silently
	// treated as copied data.
	LegacyTablesMissing []string
	// LegacyColumnsSkipped names frozen legacy columns that were deliberately
	// not carried. The destination keeps its DDL default.
	LegacyColumnsSkipped []gatewayMySQLCopySkippedColumn
	// DestinationOnlyTables names tables the embedded native schema creates but
	// the source snapshot never had. A fresh or never-migrated SQLite store
	// misses whole feature modules; the category is accepted only while the
	// destination table is empty, and every accepted name is reported.
	DestinationOnlyTables []gatewayCopyDestinationOnlyTable
	// Files records the filesystem half when the caller supplied data dirs.
	Files *gatewayCopyFileReport
	// UTF8RepairedCells counts non-PK SQLite TEXT values containing invalid
	// UTF-8 byte sequences (for example historical byte-sliced audit rows)
	// normalized to U+FFFD so strict-mode MySQL utf8mb4 accepts them and
	// source/destination SHA-256 readback checks the exact stored strings.
	UTF8RepairedCells  int64
	UTF8RepairedTables map[string]int64
}

// gatewayCopyDestinationOnlyTable is one native-schema table the snapshot does
// not contain. Rows is the destination count at plan time and must be zero; it
// is recorded so the report carries the evidence instead of a bare verdict.
type gatewayCopyDestinationOnlyTable struct {
	Name string
	Rows int64
}

// The two destination-only buckets. A table the snapshot lacks is never copied;
// the bucket only explains why the destination keeps it and where the empty
// proof came from.
const (
	gatewayCopyBucketNativeSchemaOnly      = "native_schema_only"
	gatewayCopyBucketLegacyDestinationOnly = "legacy_destination_only"
)

// gatewayCopyClassifyDestinationOnly is the one bucketing rule shared by the
// dry-run preview and the real copy. The snapshot lacks this table; rows is the
// destination row count. A frozen legacy table and a table the embedded native
// schema creates are both accepted only while empty, and both are rejected as
// stale otherwise. Anything the native schema does not create is schema drift.
// gatewayCopyLegacyTables stays the only legacy list.
func gatewayCopyClassifyDestinationOnly(name string, inSource bool, rows int64) (string, error) {
	if inSource {
		return "", nil
	}
	if gatewayCopyLegacyTable(name) {
		if rows != 0 {
			return "", errGatewayCopyStale
		}
		return gatewayCopyBucketLegacyDestinationOnly, nil
	}
	nativeTables, _, err := gatewayMySQLExpectedObjects()
	if err != nil {
		return "", errors.New("mysql_copy_native_schema_unreadable")
	}
	if !nativeTables[name] {
		return "", gatewayCopyTableFailure(name, errGatewayCopySchema)
	}
	if rows != 0 {
		return "", fmt.Errorf("%w: table %s holds %d row(s) but the snapshot has no such table", errGatewayCopyStale, name, rows)
	}
	return gatewayCopyBucketNativeSchemaOnly, nil
}

// gatewayMySQLCopySkippedColumn is one frozen legacy column the copier left
// behind. Direction says which side still carried it, because a fresh store
// lacks the column while an old production database keeps it.
type gatewayMySQLCopySkippedColumn struct {
	Table    string
	Column   string
	InSource bool
	InTarget bool
}

type gatewayCopyColumn struct {
	name string
	kind byte // i = integer, r = real, t = text, b = blob
}

type gatewayCopyTable struct {
	name       string
	columns    []gatewayCopyColumn
	key        []string
	parents    []string
	autoID     string
	autoSource bool
	highID     int64 // includes sqlite_sequence, even if the highest row was deleted
	seed       bool  // deleted IDs require an allocator reservation before row DML
	// deferColumns names self-referencing foreign-key columns. They are
	// inserted as NULL first and backfilled in this same transaction once all
	// parent rows exist, so a data-aware cutover never drops a constraint.
	deferColumns []string
}

// gatewayCopyTableError keeps a copier message byte-identical while recording
// the table that failed. Existing callers compare err.Error() and errors.Is
// against the sanitized sentinels, so wrapping must not change either; the
// store mysql-copy command uses the recorded name to point at the table.
type gatewayCopyTableError struct {
	table string
	err   error
}

func (e *gatewayCopyTableError) Error() string { return e.err.Error() }

func (e *gatewayCopyTableError) Unwrap() error { return e.err }

func gatewayCopyTableFailure(table string, err error) error {
	if err == nil {
		return nil
	}
	return &gatewayCopyTableError{table: table, err: err}
}

// gatewayCopyTableName reports the table a copier error came from, or "".
func gatewayCopyTableName(err error) string {
	var failure *gatewayCopyTableError
	if errors.As(err, &failure) {
		return failure.table
	}
	return ""
}

var (
	errGatewayCopySchema        = errors.New("mysql_copy_schema_mismatch")
	errGatewayCopyStale         = errors.New("mysql_copy_destination_stale")
	errGatewayCopyContent       = errors.New("mysql_copy_content_mismatch")
	errGatewayCopyRead          = errors.New("mysql_copy_read_failed")
	errGatewayCopySourceFK      = errors.New("mysql_copy_source_foreign_key_failed")
	errGatewayCopyInvalidUTF8PK = errors.New("mysql_copy_invalid_utf8_primary_key")
)

const (
	// Batch multi-row INSERTs so million-row production tables avoid millions of
	// sequential round-trips while staying well below MySQL 5.7's default 4 MiB
	// max_allowed_packet and the 65535 prepared-parameter protocol ceiling.
	gatewayCopyBatchMaxRows  = 500
	gatewayCopyBatchMaxBytes = 2 * 1024 * 1024
)

// copyGatewaySQLiteToMySQL requires an offline destination with the reviewed
// MySQL schema already installed. Both modes exercise the same inserts and
// readback. Dry run rolls back rows. InnoDB allocator reservations survive a
// rollback, so a failed/rehearsed target may require fresh provisioning.
// gatewayCopyDataDirs is the filesystem half of a cutover. Both roots are
// absolute data directories (the same DOOPS_GATEWAY_DATA_DIR the Gateway
// runs with). Paths stored in the database are relative to these roots, so the
// files must move with the rows or the new Gateway reports missing artifacts.
type gatewayCopyDataDirs struct{ Source, Target string }

// gatewayCopyFileReport records the filesystem half of the cutover.
type gatewayCopyFileReport struct {
	ArtifactsCopied int64
	SkillsCopied    int64
	Files           int64
	Bytes           int64
	ArtifactRows    int64
	SkillRows       int64
	// PathsRewritten counts legacy absolute artifact_dir/storage_dir values
	// normalized to the data-dir-relative form MySQL reads. Current stores
	// already write relative paths, so this is normally zero.
	PathsRewritten int64
}

// copyGatewaySQLiteToMySQL keeps the database-only entry point used by callers
// without a filesystem half. A production cutover moves the data directory
// too; see gatewayCopyRun.
func copyGatewaySQLiteToMySQL(ctx context.Context, sqliteDB, mysqlDB *sql.DB, dryRun bool) (*gatewayMySQLCopyReport, error) {
	return gatewayCopyRun(ctx, sqliteDB, mysqlDB, dryRun, nil)
}

// gatewayCopyRun copies the whole store: rows, the self-referencing inventory
// rows a plain insert order cannot satisfy, and — when dirs is set — the
// artifact and Skill trees the row indexes point into.
func gatewayCopyRun(ctx context.Context, sqliteDB, mysqlDB *sql.DB, dryRun bool, dirs *gatewayCopyDataDirs) (*gatewayMySQLCopyReport, error) {
	src, err := sqliteDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, errGatewayCopyRead
	}
	defer src.Rollback()
	conn, err := mysqlDB.Conn(ctx)
	if err != nil {
		return nil, errors.New("mysql_copy_connect_failed")
	}
	defer conn.Close()
	serverVersion, err := gatewayMySQLQueryVersion(ctx, conn)
	if err != nil {
		return nil, err
	}
	// information_schema normally caches AUTO_INCREMENT for 24 hours on MySQL 8.
	// MySQL 5.7 has no information_schema_stats_expiry variable and always reads live values.
	// Always enable STRICT_ALL_TABLES explicitly so copier behavior does not
	// depend on server defaults or whether schema install reused this pool conn.
	sessionInitSQL := `SET SESSION information_schema_stats_expiry=0,
		sql_mode=CONCAT_WS(',',NULLIF(@@SESSION.sql_mode,''),'STRICT_ALL_TABLES','NO_AUTO_VALUE_ON_ZERO')`
	if serverVersion.isMySQL57() {
		sessionInitSQL = `SET SESSION sql_mode=CONCAT_WS(',',NULLIF(@@SESSION.sql_mode,''),'STRICT_ALL_TABLES','NO_AUTO_VALUE_ON_ZERO')`
	}
	if _, err := conn.ExecContext(ctx, sessionInitSQL); err != nil {
		return nil, errors.New("mysql_copy_unsafe_session")
	}
	opts := &sql.TxOptions{Isolation: sql.LevelSerializable}
	dst, err := conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, errors.New("mysql_copy_begin_failed")
	}
	defer dst.Rollback()
	report := &gatewayMySQLCopyReport{
		TableRows:          make(map[string]int64),
		UTF8RepairedTables: make(map[string]int64),
	}
	tables, err := gatewayCopyPlan(ctx, src, dst, report, serverVersion.isMySQL57())
	if err != nil {
		return nil, err
	}
	if err := gatewayCopyCheckEmpty(ctx, dst, tables, false); err != nil {
		return nil, err
	}
	if slices.ContainsFunc(tables, func(t *gatewayCopyTable) bool { return t.seed }) {
		// ALTER implicitly commits in MySQL. Reserve deleted SQLite IDs only
		// after checking the entire target and strictly BEFORE any row inserts.
		// An interrupted reservation is stale on the next invocation.
		if err := dst.Rollback(); err != nil {
			return nil, errors.New("mysql_copy_rollback_failed")
		}
		for _, t := range tables {
			if t.seed {
				q := "ALTER TABLE " + doSQLQuoteIdentifier("mysql", t.name) + " AUTO_INCREMENT=" + strconv.FormatInt(t.highID+1, 10)
				if _, err := conn.ExecContext(ctx, q); err != nil {
					return nil, errors.New("mysql_copy_allocator_reservation_failed")
				}
			}
		}
		dst, err = conn.BeginTx(ctx, opts)
		if err != nil {
			return nil, errors.New("mysql_copy_begin_failed")
		}
		defer dst.Rollback()
		if err := gatewayCopyCheckEmpty(ctx, dst, tables, true); err != nil {
			return nil, err
		}
	}

	digests := make(map[string]string, len(tables))
	for _, t := range tables {
		n, digest, repaired, err := t.copyToDestination(ctx, src, dst)
		if err != nil {
			return nil, gatewayCopyTableFailure(t.name, err)
		}
		report.TableRows[t.name], digests[t.name] = n, digest
		if repaired > 0 {
			report.UTF8RepairedCells += repaired
			report.UTF8RepairedTables[t.name] = repaired
		}
	}
	// Backfill deferred self-references now that every parent row exists. The
	// destination foreign key stays enforced for the whole transaction.
	for _, t := range tables {
		if len(t.deferColumns) == 0 {
			continue
		}
		if err := gatewayCopyBackfillSelfRefs(ctx, src, dst, t); err != nil {
			return nil, gatewayCopyTableFailure(t.name, err)
		}
	}
	// Read every destination after ALL inserts, including tables copied first.
	// This catches coercions and changes from constraints or triggers at equal count.
	overall := sha256.New()
	for _, t := range tables {
		n, digest, err := t.stream(ctx, dst, "mysql", nil)
		if err != nil {
			return nil, gatewayCopyTableFailure(t.name, err)
		}
		if n != report.TableRows[t.name] || digest != digests[t.name] {
			return nil, gatewayCopyTableFailure(t.name, errGatewayCopyContent)
		}
		if t.autoID != "" {
			next, err := gatewayCopyNextID(ctx, dst, t.name)
			if err != nil || (next != t.highID+1 && !(next == 1 && t.highID == 0)) {
				return nil, gatewayCopyTableFailure(t.name, errors.New("mysql_copy_allocator_mismatch"))
			}
		}
		fmt.Fprintf(overall, "%q %d %x %d\n", t.name, n, digest, t.highID)
	}
	report.SHA256 = hex.EncodeToString(overall.Sum(nil))
	// The filesystem half runs inside the same transaction: a database row must
	// never commit without the files it indexes. cleanData removes a partially
	// materialized target tree when anything below fails.
	cleanData := func() {}
	if dirs != nil {
		files, cleanup, err := gatewayCopyMigrateDataDirs(ctx, dst, dirs)
		if err != nil {
			return nil, err
		}
		report.Files, cleanData = files, cleanup
	}
	if err := src.Commit(); err != nil {
		return nil, errGatewayCopyRead
	}
	if dryRun {
		cleanData()
		if err := dst.Rollback(); err != nil {
			return nil, errors.New("mysql_copy_rollback_failed")
		}
	} else if err := dst.Commit(); err != nil {
		// A broken connection during COMMIT has an indeterminate outcome.
		cleanData()
		return nil, errors.New("mysql_copy_commit_unconfirmed")
	}
	return report, nil
}

func gatewayCopyPlan(ctx context.Context, src, dst *sql.Tx, report *gatewayMySQLCopyReport, isMySQL57 bool) ([]*gatewayCopyTable, error) {
	var safe bool
	if err := dst.QueryRowContext(ctx, `SELECT @@foreign_key_checks=1 AND @@unique_checks=1 AND
		@@auto_increment_increment=1 AND @@auto_increment_offset=1 AND @@character_set_client='utf8mb4'
		AND @@character_set_connection='utf8mb4' AND @@character_set_results='utf8mb4'
		AND FIND_IN_SET('NO_AUTO_VALUE_ON_ZERO', @@sql_mode) > 0`).Scan(&safe); err != nil || !safe {
		return nil, errors.New("mysql_copy_unsafe_session")
	}
	if err := gatewayCopyCheckSourceForeignKeys(ctx, src); err != nil {
		return nil, err
	}
	byName := make(map[string]*gatewayCopyTable)
	hasSeq := false
	err := gatewayCopyRows(ctx, src, `SELECT name, type FROM pragma_table_list WHERE schema='main'`, func(r *sql.Rows) error {
		var name, kind string
		if err := r.Scan(&name, &kind); err != nil {
			return errGatewayCopyRead
		}
		if strings.HasPrefix(name, "sqlite_") {
			switch name {
			case "sqlite_schema", "sqlite_temp_schema", "sqlite_stat1", "sqlite_stat2", "sqlite_stat3", "sqlite_stat4":
				return nil // SQLite-owned catalog/statistics are not application data.
			case "sqlite_sequence":
				hasSeq = true
				return nil // The sequence is validated and applied below.
			default:
				return errGatewayCopySchema
			}
		}
		if kind != "table" {
			return errGatewayCopySchema
		}
		byName[name] = &gatewayCopyTable{name: name}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = gatewayCopyRows(ctx, src, `SELECT name FROM main.sqlite_schema
		WHERE type='table' AND upper(COALESCE(sql,'')) LIKE '%AUTOINCREMENT%'`, func(r *sql.Rows) error {
		var name string
		if r.Scan(&name) != nil || byName[name] == nil {
			return errGatewayCopySchema
		}
		byName[name].autoSource = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	seen := 0
	seenNames := make(map[string]bool, len(byName))
	var destinationOnly []string
	err = gatewayCopyRows(ctx, dst, `SELECT TABLE_NAME, TABLE_TYPE, COALESCE(ENGINE,'') FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()`, func(r *sql.Rows) error {
		var name, kind, engine string
		if r.Scan(&name, &kind, &engine) != nil || kind != "BASE TABLE" || engine != "InnoDB" {
			return errGatewayCopySchema
		}
		if byName[name] == nil {
			// A destination table the snapshot does not have is classified by the
			// same rule the dry run uses, after its row count is known.
			if !slices.Contains(destinationOnly, name) {
				destinationOnly = append(destinationOnly, name)
			}
			return nil
		}
		seen++
		seenNames[name] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if seen == 0 || seen != len(byName) {
		missing := make([]string, 0, len(byName)-seen)
		for name := range byName {
			if !seenNames[name] {
				missing = append(missing, name)
			}
		}
		slices.Sort(missing)
		if len(missing) == 0 {
			return nil, errGatewayCopySchema
		}
		return nil, gatewayCopyTableFailure(missing[0], errGatewayCopySchema)
	}
	// A destination table outside the snapshot is classified by the same rule the
	// dry run uses, after its row count is known. Neither bucket is ever copied:
	// the copy must neither erase rows the source does not have nor pretend it
	// reproduced them. Both are accepted only while empty.
	slices.Sort(destinationOnly)
	for _, name := range destinationOnly {
		var rows int64
		if err := dst.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+doSQLQuoteIdentifier("mysql", name)).Scan(&rows); err != nil {
			return nil, gatewayCopyTableFailure(name, errGatewayCopyRead)
		}
		bucket, err := gatewayCopyClassifyDestinationOnly(name, false, rows)
		if err != nil {
			return nil, err
		}
		switch bucket {
		case gatewayCopyBucketLegacyDestinationOnly:
			// Keep the row lock the legacy acceptance has always taken: a concurrent
			// insert between the count and the copy must still fail closed.
			var dummy int
			switch err := dst.QueryRowContext(ctx, "SELECT 1 FROM "+doSQLQuoteIdentifier("mysql", name)+" LIMIT 1 FOR UPDATE").Scan(&dummy); err {
			case nil:
				return nil, errGatewayCopyStale
			case sql.ErrNoRows:
			default:
				return nil, gatewayCopyTableFailure(name, errGatewayCopyRead)
			}
			report.LegacyTablesMissing = append(report.LegacyTablesMissing, name)
		case gatewayCopyBucketNativeSchemaOnly:
			report.DestinationOnlyTables = append(report.DestinationOnlyTables, gatewayCopyDestinationOnlyTable{Name: name, Rows: rows})
		default:
			return nil, gatewayCopyTableFailure(name, errGatewayCopySchema)
		}
		report.TableRows[name] = 0
	}
	slices.Sort(report.LegacyTablesMissing)
	err = gatewayCopyRows(ctx, dst, `SELECT TABLE_NAME, REFERENCED_TABLE_NAME, REFERENCED_TABLE_SCHEMA=DATABASE(), COLUMN_NAME
		FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND REFERENCED_TABLE_NAME IS NOT NULL`, func(r *sql.Rows) error {
		var child, parent, column string
		var local bool
		if r.Scan(&child, &parent, &local, &column) != nil || !local {
			return errGatewayCopySchema
		}
		if byName[child] == nil {
			// The child table is not copied: it is either a frozen legacy object or
			// a table the embedded native schema creates and this deployment never
			// migrated. It stays empty (the empty check above proves it), so its
			// references cannot make the copy incomplete.
			return nil
		}
		if byName[parent] == nil {
			// A copied child that references a table outside the snapshot would be
			// incomplete. The only accepted exception is a reference that stays
			// entirely outside the copy (both ends on the frozen legacy list).
			if gatewayCopyLegacyTable(parent) && gatewayCopyLegacyTable(child) {
				return nil
			}
			return errGatewayCopySchema
		}
		if child == parent {
			// A self-reference is data, not a table cycle: the column is inserted
			// NULL and backfilled after every parent row exists.
			byName[child].deferColumns = append(byName[child].deferColumns, column)
			return nil
		}
		byName[child].parents = append(byName[child].parents, parent)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sequences := map[string]int64{}
	if hasSeq {
		err = gatewayCopyRows(ctx, src, `SELECT name, seq FROM main.sqlite_sequence`, func(r *sql.Rows) error {
			var name string
			var seq int64
			if r.Scan(&name, &seq) != nil || byName[name] == nil || seq < 0 {
				return errGatewayCopySchema
			}
			if _, exists := sequences[name]; exists {
				return errGatewayCopySchema
			}
			sequences[name] = seq
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	// Use a deterministic topological order. Cycles and self-references need
	// an explicit data-aware migration before they can be accepted here.
	remaining := make([]string, 0, len(byName))
	for name := range byName {
		remaining = append(remaining, name)
	}
	slices.Sort(remaining)
	var ordered []*gatewayCopyTable
	for len(remaining) != 0 {
		i := slices.IndexFunc(remaining, func(name string) bool {
			return !slices.ContainsFunc(byName[name].parents, func(parent string) bool { return slices.Contains(remaining, parent) })
		})
		if i < 0 {
			return nil, errors.New("mysql_copy_foreign_key_cycle")
		}
		t := byName[remaining[i]]
		if err := t.planColumns(ctx, src, dst, sequences, report, isMySQL57); err != nil {
			return nil, err
		}
		ordered = append(ordered, t)
		remaining = slices.Delete(remaining, i, i+1)
	}
	autoTables := 0
	for _, t := range ordered {
		if t.autoID != "" {
			autoTables++
		}
	}
	if autoTables == 0 && len(sequences) != 0 {
		return nil, errGatewayCopySchema
	}
	for name := range sequences {
		if byName[name] == nil || !byName[name].autoSource {
			return nil, errGatewayCopySchema
		}
	}
	return ordered, nil
}

func (t *gatewayCopyTable) planColumns(ctx context.Context, src, dst *sql.Tx, sequences map[string]int64, report *gatewayMySQLCopyReport, isMySQL57 bool) error {
	keyParts := make(map[int]string)
	sourceColumns := map[string]bool{}
	// This copier handles the Gateway's native SQLite storage classes. Unknown
	// legacy types and generated/hidden source columns require an explicit map.
	err := gatewayCopyRows(ctx, src, `SELECT name, upper(type), pk, hidden FROM pragma_table_xinfo(?, 'main') ORDER BY cid`, func(r *sql.Rows) error {
		var name, typ string
		var pk, hidden int
		if r.Scan(&name, &typ, &pk, &hidden) != nil {
			return errGatewayCopySchema
		}
		sourceColumns[name] = true
		if gatewayCopyLegacyColumn(t.name, name) {
			return nil // recorded after both sides are known
		}
		if hidden != 0 {
			return errGatewayCopySchema
		}
		kind := map[string]byte{"INTEGER": 'i', "REAL": 'r', "TEXT": 't', "BLOB": 'b'}[typ]
		if kind == 0 {
			return errGatewayCopySchema
		}
		t.columns = append(t.columns, gatewayCopyColumn{name, kind})
		if pk > 0 {
			keyParts[pk] = name
		}
		return nil
	}, t.name)
	if err != nil {
		return err
	}
	for i := 1; i <= len(keyParts); i++ {
		t.key = append(t.key, keyParts[i])
	}
	if len(t.key) == 0 {
		return errGatewayCopySchema
	}
	if len(t.key) == 1 && gatewayCopyColumnKind(t.columns, t.key[0]) == 'i' {
		// SQLite's INTEGER PRIMARY KEY is a rowid alias even without the
		// AUTOINCREMENT keyword. The destination must preserve that generator.
		t.autoSource = true
	}
	columns := map[string]byte{}
	targetColumns := map[string]bool{}
	err = gatewayCopyRows(ctx, dst, `SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, func(r *sql.Rows) error {
		var name, typ, columnType, extra string
		if r.Scan(&name, &typ, &columnType, &extra) != nil {
			return errGatewayCopyRead
		}
		targetColumns[name] = true
		if gatewayCopyLegacyColumn(t.name, name) {
			return nil // recorded after both sides are known
		}
		if isMySQL57 && gatewayMySQL57VirtualKeyColumn(t.name, name) && strings.EqualFold(strings.TrimSpace(extra), "virtual generated") {
			return nil // frozen MySQL 5.7 virtual unique-key backing column
		}
		if strings.Contains(strings.ToLower(extra), "generated") || strings.Contains(strings.ToLower(columnType), "unsigned") {
			return errGatewayCopySchema
		}
		switch typ {
		case "bigint", "int", "mediumint", "smallint", "tinyint":
			columns[name] = 'i'
		case "double":
			columns[name] = 'r'
		case "varchar", "char", "tinytext", "text", "mediumtext", "longtext":
			columns[name] = 't'
		case "varbinary", "binary", "tinyblob", "blob", "mediumblob", "longblob":
			columns[name] = 'b'
		default:
			return errGatewayCopySchema
		}
		if strings.Contains(extra, "auto_increment") {
			t.autoID = name
		}
		return nil
	}, t.name)
	if err != nil {
		return err
	}
	for _, name := range gatewayCopyLegacyColumns[t.name] {
		if sourceColumns[name] || targetColumns[name] {
			report.LegacyColumnsSkipped = append(report.LegacyColumnsSkipped, gatewayMySQLCopySkippedColumn{
				Table: t.name, Column: name, InSource: sourceColumns[name], InTarget: targetColumns[name],
			})
		}
	}
	if len(columns) != len(t.columns) {
		return errGatewayCopySchema
	}
	for _, c := range t.columns {
		if columns[c.name] != c.kind {
			return errGatewayCopySchema
		}
	}
	var destKey []string
	err = gatewayCopyRows(ctx, dst, `SELECT COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND CONSTRAINT_NAME='PRIMARY' ORDER BY ORDINAL_POSITION`, func(r *sql.Rows) error {
		var name string
		if r.Scan(&name) != nil {
			return errGatewayCopyRead
		}
		destKey = append(destKey, name)
		return nil
	}, t.name)
	if err != nil {
		return err
	}
	if !slices.Equal(t.key, destKey) {
		return errGatewayCopySchema
	}
	seq, hasSeq := sequences[t.name]
	if t.autoSource != (t.autoID != "") {
		return errGatewayCopySchema
	}
	if t.autoID == "" {
		if hasSeq {
			return errGatewayCopySchema
		}
		return nil
	}
	if len(t.key) != 1 || t.key[0] != t.autoID || columns[t.autoID] != 'i' {
		return errGatewayCopySchema
	}
	var maxID int64
	q := "SELECT COALESCE(MAX(" + doSQLQuoteIdentifier("sqlite", t.autoID) + "),0) FROM main." + doSQLQuoteIdentifier("sqlite", t.name)
	if err := src.QueryRowContext(ctx, q).Scan(&maxID); err != nil {
		return errGatewayCopyRead
	}
	t.highID = max(0, seq, maxID)
	if t.highID == math.MaxInt64 {
		return errors.New("mysql_copy_allocator_exhausted")
	}
	t.seed = seq > max(0, maxID)
	return nil
}

func gatewayCopyColumnKind(columns []gatewayCopyColumn, name string) byte {
	for _, column := range columns {
		if column.name == name {
			return column.kind
		}
	}
	return 0
}

func gatewayCopyRows(ctx context.Context, tx *sql.Tx, query string, visit func(*sql.Rows) error, args ...any) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return errGatewayCopyRead
	}
	defer rows.Close()
	for rows.Next() {
		if err := visit(rows); err != nil {
			return err
		}
	}
	if rows.Err() != nil {
		return errGatewayCopyRead
	}
	return nil
}

func gatewayCopyNextID(ctx context.Context, tx *sql.Tx, table string) (int64, error) {
	var next int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(AUTO_INCREMENT,1) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table).Scan(&next)
	return next, err
}

func gatewayCopyCheckEmpty(ctx context.Context, dst *sql.Tx, tables []*gatewayCopyTable, reserved bool) error {
	for _, t := range tables {
		var dummy int
		err := dst.QueryRowContext(ctx, "SELECT 1 FROM "+doSQLQuoteIdentifier("mysql", t.name)+" LIMIT 1 FOR UPDATE").Scan(&dummy)
		switch err {
		case nil:
			return errGatewayCopyStale
		case sql.ErrNoRows: // holds the empty InnoDB range until commit/rollback
		default:
			return errGatewayCopyRead
		}
		if t.autoID != "" {
			want := int64(1)
			if reserved && t.seed {
				want = t.highID + 1
			}
			got, err := gatewayCopyNextID(ctx, dst, t.name)
			if err != nil || got != want {
				return errGatewayCopyStale
			}
		}
	}
	return nil
}

// gatewayCopySQLiteQueryer matches both *sql.DB (used by dry-run previews) and
// *sql.Tx (used inside the copier transaction) so source FK validation runs one
// shared implementation in both paths.
type gatewayCopySQLiteQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// gatewayCopyCheckSourceForeignKeys verifies every SQLite foreign-key edge
// except the one legacy SQLite constraint the native MySQL schema intentionally
// removed: credentials.owner_id -> users(id) for platform-scope credentials
// storing owner_id=” (see gateway_mysql_schema.sql). Every other violation —
// including broken credentials.created_by, personal credentials with a missing
// owner, or tokens.user_id=” — still fails closed and names the offending table.
func gatewayCopyCheckSourceForeignKeys(ctx context.Context, q gatewayCopySQLiteQueryer) error {
	type fkViolation struct {
		table  string
		rowid  int64
		parent string
		fkid   int
	}
	rows, err := q.QueryContext(ctx, `SELECT "table", rowid, parent, fkid FROM main.pragma_foreign_key_check`)
	if err != nil {
		return errGatewayCopySourceFK
	}
	var violations []fkViolation
	for rows.Next() {
		var v fkViolation
		if err := rows.Scan(&v.table, &v.rowid, &v.parent, &v.fkid); err != nil {
			rows.Close()
			return errGatewayCopySourceFK
		}
		violations = append(violations, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return errGatewayCopySourceFK
	}
	if err := rows.Close(); err != nil {
		return errGatewayCopySourceFK
	}
	if len(violations) == 0 {
		return nil
	}

	var credFKFrom map[int]string
	for _, v := range violations {
		if v.table == "credentials" && v.parent == "users" {
			if credFKFrom == nil {
				credFKFrom = make(map[int]string)
				fkRows, fkErr := q.QueryContext(ctx, `SELECT id, "from" FROM main.pragma_foreign_key_list('credentials')`)
				if fkErr != nil {
					return gatewayCopyTableFailure(v.table, errGatewayCopySourceFK)
				}
				for fkRows.Next() {
					var id int
					var from string
					if err := fkRows.Scan(&id, &from); err != nil {
						fkRows.Close()
						return gatewayCopyTableFailure(v.table, errGatewayCopySourceFK)
					}
					credFKFrom[id] = from
				}
				if err := fkRows.Err(); err != nil {
					fkRows.Close()
					return gatewayCopyTableFailure(v.table, errGatewayCopySourceFK)
				}
				if err := fkRows.Close(); err != nil {
					return gatewayCopyTableFailure(v.table, errGatewayCopySourceFK)
				}
			}
			if credFKFrom[v.fkid] == "owner_id" {
				var scope, ownerID string
				if err := q.QueryRowContext(ctx, `SELECT scope, owner_id FROM main.credentials WHERE rowid = ?`, v.rowid).Scan(&scope, &ownerID); err == nil && scope == "platform" && ownerID == "" {
					continue
				}
			}
		}
		return gatewayCopyTableFailure(v.table, errGatewayCopySourceFK)
	}
	return nil
}

func (t *gatewayCopyTable) selectOrderedQuery(engine string) (string, hash.Hash) {
	h := sha256.New()
	var projection, order []string
	text := map[string]bool{}
	for _, c := range t.columns {
		name := doSQLQuoteIdentifier(engine, c.name)
		projection = append(projection, name)
		text[c.name] = c.kind == 't'
		fmt.Fprintf(h, "%q %c\n", c.name, c.kind)
	}
	for _, k := range t.key {
		key := doSQLQuoteIdentifier(engine, k)
		if text[k] && engine == "sqlite" {
			key += " COLLATE BINARY"
		} else if text[k] {
			key = "CAST(" + key + " AS BINARY)"
		}
		order = append(order, key)
	}
	table := doSQLQuoteIdentifier(engine, t.name)
	if engine == "sqlite" {
		table = "main." + table
	}
	return "SELECT " + strings.Join(projection, ",") + " FROM " + table + " ORDER BY " + strings.Join(order, ","), h
}

func (t *gatewayCopyTable) hashAndValidateRow(h hash.Hash, engine string, values []any) (int, error) {
	rowBytes := 64
	for i, c := range t.columns {
		if values[i] == nil {
			h.Write([]byte{'n'})
			continue
		}
		var data []byte
		switch v := values[i].(type) {
		case int64:
			if c.kind == 'i' {
				data = binary.BigEndian.AppendUint64(nil, uint64(v))
			} else if c.kind == 'r' {
				data = binary.BigEndian.AppendUint64(nil, math.Float64bits(float64(v)))
			} else {
				return 0, errGatewayCopyContent
			}
		case float64:
			if c.kind != 'r' {
				return 0, errGatewayCopyContent
			}
			data = binary.BigEndian.AppendUint64(nil, math.Float64bits(v))
		case string:
			if c.kind != 't' {
				return 0, errGatewayCopyContent
			}
			data = []byte(v)
		case []byte:
			// MySQL returns []byte for both TEXT and BLOB. SQLite returns
			// string for TEXT and typed nil []byte for a zero-length BLOB.
			if c.kind != 'b' && !(engine == "mysql" && c.kind == 't') {
				return 0, errGatewayCopyContent
			}
			if v == nil {
				v = []byte{}
				values[i] = v
			}
			data = v
		default:
			return 0, errGatewayCopyContent
		}
		h.Write([]byte{c.kind})
		h.Write(binary.AppendUvarint(nil, uint64(len(data))))
		h.Write(data)
		rowBytes += len(data)
	}
	return rowBytes, nil
}

// copyToDestination streams rows from SQLite in binary PK order, normalizes
// invalid UTF-8 sequences in non-PK TEXT columns to U+FFFD (reporting exact
// counts and failing closed on invalid UTF-8 in primary keys), hashes the
// typed values sent to MySQL, and writes them in multi-row INSERT batches
// bounded by both row/parameter count and packet byte size.
func (t *gatewayCopyTable) copyToDestination(ctx context.Context, src, dst *sql.Tx) (int64, string, int64, error) {
	q, h := t.selectOrderedQuery("sqlite")
	colCount := len(t.columns)
	names := make([]string, colCount)
	deferIdx := make([]bool, colCount)
	pkIdx := make([]bool, colCount)
	for i, c := range t.columns {
		names[i] = c.name
		if slices.Contains(t.deferColumns, c.name) {
			deferIdx[i] = true
		}
		if slices.Contains(t.key, c.name) {
			pkIdx[i] = true
		}
	}
	batchRows := gatewayCopyBatchMaxRows
	if maxByParams := doSQLCopyMaxParams / colCount; maxByParams < batchRows {
		batchRows = maxByParams
	}
	if batchRows < 1 {
		batchRows = 1
	}

	var fullStmt *sql.Stmt
	defer func() {
		if fullStmt != nil {
			_ = fullStmt.Close()
		}
	}()

	batchArgs := make([]any, 0, batchRows*colCount)
	pending := 0
	pendingBytes := 0
	flush := func() error {
		if pending == 0 {
			return nil
		}
		var (
			res sql.Result
			err error
		)
		if pending == batchRows {
			if fullStmt == nil {
				fullStmt, err = dst.PrepareContext(ctx, doSQLCopyInsert("mysql", t.name, names, batchRows))
				if err != nil {
					return errors.New("mysql_copy_prepare_failed")
				}
			}
			res, err = fullStmt.ExecContext(ctx, batchArgs...)
		} else {
			res, err = dst.ExecContext(ctx, doSQLCopyInsert("mysql", t.name, names, pending), batchArgs...)
		}
		if err != nil {
			return errors.New("mysql_copy_insert_failed")
		}
		if n, err := res.RowsAffected(); err != nil || n != int64(pending) {
			return errGatewayCopyContent
		}
		batchArgs = batchArgs[:0]
		pending = 0
		pendingBytes = 0
		return nil
	}

	values, pointers := make([]any, colCount), make([]any, colCount)
	for i := range values {
		pointers[i] = &values[i]
	}
	var count int64
	var utf8Repaired int64
	err := gatewayCopyRows(ctx, src, q, func(r *sql.Rows) error {
		if err := r.Scan(pointers...); err != nil {
			return errGatewayCopyRead
		}
		for i, c := range t.columns {
			if c.kind != 't' {
				continue
			}
			if s, ok := values[i].(string); ok && !utf8.ValidString(s) {
				if pkIdx[i] {
					return errGatewayCopyInvalidUTF8PK
				}
				values[i] = strings.ToValidUTF8(s, "\uFFFD")
				utf8Repaired++
			}
		}
		rowBytes, err := t.hashAndValidateRow(h, "sqlite", values)
		if err != nil {
			return err
		}
		if pending > 0 && (pending >= batchRows || pendingBytes+rowBytes > gatewayCopyBatchMaxBytes) {
			if err := flush(); err != nil {
				return err
			}
		}
		for i := range values {
			if deferIdx[i] {
				// The self-referencing parent may sort after this row; store NULL
				// now and let gatewayCopyBackfillSelfRefs fill it in.
				batchArgs = append(batchArgs, nil)
				continue
			}
			if b, ok := values[i].([]byte); ok {
				batchArgs = append(batchArgs, bytes.Clone(b))
			} else {
				batchArgs = append(batchArgs, values[i])
			}
		}
		pending++
		pendingBytes += rowBytes
		count++
		if pending >= batchRows || pendingBytes >= gatewayCopyBatchMaxBytes {
			return flush()
		}
		return nil
	})
	if err != nil {
		return 0, "", 0, err
	}
	if err := flush(); err != nil {
		return 0, "", 0, err
	}
	if fullStmt != nil {
		stmt := fullStmt
		fullStmt = nil
		if err := stmt.Close(); err != nil {
			return 0, "", 0, errors.New("mysql_copy_statement_failed")
		}
	}
	return count, string(h.Sum(nil)), utf8Repaired, nil
}

// stream hashes length-framed, typed values in primary-key order with explicit
// binary text ordering on both engines. Memory use is bounded by one row.
func (t *gatewayCopyTable) stream(ctx context.Context, tx *sql.Tx, engine string, insert *sql.Stmt) (int64, string, error) {
	q, h := t.selectOrderedQuery(engine)
	values, pointers := make([]any, len(t.columns)), make([]any, len(t.columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	var count int64
	err := gatewayCopyRows(ctx, tx, q, func(r *sql.Rows) error {
		if err := r.Scan(pointers...); err != nil {
			return errGatewayCopyRead
		}
		if _, err := t.hashAndValidateRow(h, engine, values); err != nil {
			return err
		}
		if insert != nil && len(t.deferColumns) != 0 {
			// The self-referencing parent may sort after this row; store NULL now
			// and let gatewayCopyBackfillSelfRefs fill it in.
			for i, c := range t.columns {
				if slices.Contains(t.deferColumns, c.name) {
					values[i] = nil
				}
			}
		}
		if insert != nil {
			res, err := insert.ExecContext(ctx, values...)
			if err != nil {
				return errors.New("mysql_copy_insert_failed")
			}
			if n, err := res.RowsAffected(); err != nil || n != 1 {
				return errGatewayCopyContent
			}
		}
		count++
		return nil
	})
	return count, string(h.Sum(nil)), err
}

// gatewayCopyBackfillSelfRefs fills a self-referencing foreign key after every
// parent row exists. Rows were inserted with the column NULL, so the constraint
// was enforced the whole time. A genuine parent cycle is refused by name
// instead of being written: the destination would accept it, and the Gateway
// would then walk a loop.
func gatewayCopyBackfillSelfRefs(ctx context.Context, src, dst *sql.Tx, t *gatewayCopyTable) error {
	if len(t.key) != 1 || len(t.deferColumns) != 1 {
		return errors.New("mysql_copy_self_reference_unsupported")
	}
	key, column := t.key[0], t.deferColumns[0]
	q := "SELECT " + doSQLQuoteIdentifier("sqlite", key) + "," + doSQLQuoteIdentifier("sqlite", column) +
		" FROM main." + doSQLQuoteIdentifier("sqlite", t.name) + " ORDER BY " + doSQLQuoteIdentifier("sqlite", key)
	type row struct {
		id     any
		parent any
	}
	var rows []row
	edges := map[string]string{}
	err := gatewayCopyRows(ctx, src, q, func(r *sql.Rows) error {
		var id, parent any
		if err := r.Scan(&id, &parent); err != nil {
			return errGatewayCopyRead
		}
		rows = append(rows, row{id: id, parent: parent})
		if parent == nil {
			return nil
		}
		from, to := fmt.Sprintf("%v", id), fmt.Sprintf("%v", parent)
		edges[from] = to
		return nil
	})
	if err != nil {
		return err
	}
	for start := range edges {
		seen := map[string]bool{}
		for node := start; ; {
			if seen[node] {
				return fmt.Errorf("mysql_copy_parent_cycle: table %s row %s loops through %s; refusing to copy a self-referencing cycle", t.name, start, column)
			}
			seen[node] = true
			next, ok := edges[node]
			if !ok {
				break
			}
			node = next
		}
	}
	update := "UPDATE " + doSQLQuoteIdentifier("mysql", t.name) + " SET " + doSQLQuoteIdentifier("mysql", column) +
		"=? WHERE " + doSQLQuoteIdentifier("mysql", key) + "=?"
	for _, r := range rows {
		if r.parent == nil {
			continue
		}
		if _, err := dst.ExecContext(ctx, update, r.parent, r.id); err != nil {
			return errors.New("mysql_copy_backfill_failed")
		}
	}
	return nil
}

// gatewayCopyCleanDataDir accepts only an absolute, existing directory.
func gatewayCopyCleanDataDir(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("mysql_copy_data_dir_invalid: data directories must be absolute paths")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", errors.New("mysql_copy_data_dir_invalid: data directory " + path + " is not a readable directory")
	}
	return filepath.Clean(path), nil
}

// gatewayCopyMigrateDataDirs copies the managed trees below <data dir> into the
// target data dir and proves every database reference resolves there. The
// returned cleanup removes what it created; the copier calls it unless the
// database transaction commits.
func gatewayCopyMigrateDataDirs(ctx context.Context, dst *sql.Tx, dirs *gatewayCopyDataDirs) (*gatewayCopyFileReport, func(), error) {
	source, err := gatewayCopyCleanDataDir(dirs.Source)
	if err != nil {
		return nil, nil, err
	}
	target, err := gatewayCopyCleanDataDir(dirs.Target)
	if err != nil {
		return nil, nil, err
	}
	if source == target {
		return nil, nil, errors.New("mysql_copy_data_dir_not_distinct: the source and target data directories are the same directory")
	}
	for _, name := range []string{"artifacts", "skills"} {
		if _, err := os.Stat(filepath.Join(target, name)); err == nil {
			return nil, nil, fmt.Errorf("mysql_copy_data_dir_not_empty: %s already exists under %s; a cutover never merges files", name, target)
		}
	}
	cleanup := func() {
		for _, name := range []string{"artifacts", "skills"} {
			_ = os.RemoveAll(filepath.Join(target, name))
		}
	}
	report := &gatewayCopyFileReport{}
	for _, name := range []string{"artifacts", "skills"} {
		files, bytes, err := gatewayCopyTree(filepath.Join(source, name), filepath.Join(target, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			cleanup()
			return nil, nil, fmt.Errorf("mysql_copy_files_failed: %s: %w", name, err)
		}
		if name == "artifacts" {
			report.ArtifactsCopied = files
		} else {
			report.SkillsCopied = files
		}
		report.Files += files
		report.Bytes += bytes
	}
	if report.ArtifactRows, report.PathsRewritten, err = gatewayCopyVerifyArtifactRows(ctx, dst, source, target, report.PathsRewritten); err != nil {
		cleanup()
		return nil, nil, err
	}
	if report.SkillRows, report.PathsRewritten, err = gatewayCopyVerifySkillRows(ctx, dst, source, target, report.PathsRewritten); err != nil {
		cleanup()
		return nil, nil, err
	}
	return report, cleanup, nil
}

// gatewayCopyTree copies regular files and directories, preserving the entry
// mode, and then proves the copy with size and sha256 per file.
func gatewayCopyTree(source, target string) (int64, int64, error) {
	var files, bytes int64
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(target, 0700)
		}
		dstPath := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dstPath, 0700)
		}
		if !entry.Type().IsRegular() {
			return errors.New("unsupported file type " + rel)
		}
		srcInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0700); err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, body, srcInfo.Mode().Perm()); err != nil {
			return err
		}
		srcDigest := sha256.Sum256(body)
		copied, err := os.ReadFile(dstPath)
		if err != nil {
			return err
		}
		if sha256.Sum256(copied) != srcDigest || len(copied) != len(body) {
			return errors.New("copy verification failed for " + rel)
		}
		info, err := os.Stat(dstPath)
		if err != nil || info.Mode().Perm() != srcInfo.Mode().Perm() {
			return errors.New("copy lost the file mode of " + rel)
		}
		files++
		bytes += int64(len(body))
		return nil
	})
	return files, bytes, err
}

// gatewayCopyArtifactDigest reproduces the index the Gateway writes in
// monitor_artifact_store.go for one item directory.
func gatewayCopyArtifactDigest(dir string) (int64, string, error) {
	digest := sha256.New()
	var size int64
	for _, name := range []string{monitorArtifactMarkdownName, monitorArtifactJSONName, monitorArtifactRawName} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, "", err
		}
		if len(body) == 0 {
			continue
		}
		fmt.Fprintf(digest, "%s\x00%d\x00", name, len(body))
		digest.Write(body)
		size += int64(len(body))
	}
	return size, hex.EncodeToString(digest.Sum(nil)), nil
}

// gatewayCopyResolveArtifactDir resolves a stored monitor_run_items.artifact_dir
// value to a normalized artifacts-relative path. Legacy absolute paths that stay
// strictly below <source>/artifacts are accepted with needsRewrite=true so both
// dry-run and real copy apply the exact same containment rule.
func gatewayCopyResolveArtifactDir(source, runID string, id int64, dir string) (rel string, needsRewrite bool, err error) {
	if rel, ok := cleanMonitorArtifactRelativeDir(dir); ok {
		return rel, false, nil
	}
	abs, absErr := filepath.Abs(dir)
	artifactsRoot := filepath.Join(source, "artifacts")
	if absErr != nil || !strings.HasPrefix(abs, artifactsRoot+string(filepath.Separator)) {
		return "", false, fmt.Errorf("mysql_copy_path_outside_data_dir: run_id=%s item_id=%d artifact_dir=%s is not below %s", runID, id, dir, artifactsRoot)
	}
	rel, err = filepath.Rel(artifactsRoot, abs)
	if err != nil {
		return "", false, errGatewayCopyRead
	}
	rel, ok := cleanMonitorArtifactRelativeDir(filepath.ToSlash(rel))
	if !ok {
		return "", false, fmt.Errorf("mysql_copy_path_outside_data_dir: run_id=%s item_id=%d artifact_dir=%s is not below %s", runID, id, dir, artifactsRoot)
	}
	return rel, true, nil
}

// gatewayCopyResolveSkillDir resolves a stored platform_skills.storage_dir value
// to a normalized "skills/..." path relative to the data dir. Legacy absolute
// paths strictly below <source>/skills are accepted with needsRewrite=true.
func gatewayCopyResolveSkillDir(source, slug, dir string) (rel string, needsRewrite bool, err error) {
	rel = filepath.ToSlash(strings.TrimSpace(dir))
	if filepath.IsAbs(dir) {
		abs := filepath.Clean(dir)
		skillsRoot := filepath.Join(source, "skills")
		if !strings.HasPrefix(abs, skillsRoot+string(filepath.Separator)) {
			return "", false, fmt.Errorf("mysql_copy_path_outside_data_dir: platform_skills slug=%s storage_dir=%s is not below %s", slug, dir, skillsRoot)
		}
		rel, err = filepath.Rel(source, abs)
		if err != nil {
			return "", false, errGatewayCopyRead
		}
		rel = filepath.ToSlash(rel)
		needsRewrite = true
	}
	if !strings.HasPrefix(rel, "skills/") || strings.Contains(rel, "..") {
		return "", false, fmt.Errorf("mysql_copy_skill_path_invalid: platform_skills slug=%s storage_dir=%s is not a normalized skills/ path", slug, dir)
	}
	return rel, needsRewrite, nil
}

// gatewayCopyVerifyArtifactRows proves every indexed artifact exists at the
// target with the byte count and digest the row recorded. A missing file is a
// failed cutover, never a skipped row. Rows are drained before any UPDATE runs
// because MySQL's single-connection transaction cannot multiplex an Exec while
// a Query result set is still open.
func gatewayCopyVerifyArtifactRows(ctx context.Context, dst *sql.Tx, source, target string, rewritten int64) (int64, int64, error) {
	type artifactIndexRow struct {
		id      int64
		runID   string
		dir     string
		size    int64
		wantSHA string
	}
	rows, err := dst.QueryContext(ctx, "SELECT id, run_id, artifact_dir, artifact_bytes, artifact_sha256 FROM monitor_run_items WHERE artifact_dir != ''")
	if err != nil {
		return 0, rewritten, errGatewayCopyRead
	}
	var items []artifactIndexRow
	for rows.Next() {
		var item artifactIndexRow
		if err := rows.Scan(&item.id, &item.runID, &item.dir, &item.size, &item.wantSHA); err != nil {
			rows.Close()
			return 0, rewritten, errGatewayCopyRead
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, rewritten, errGatewayCopyRead
	}
	if err := rows.Close(); err != nil {
		return 0, rewritten, errGatewayCopyRead
	}

	var count int64
	for _, item := range items {
		rel, needsRewrite, err := gatewayCopyResolveArtifactDir(source, item.runID, item.id, item.dir)
		if err != nil {
			return 0, rewritten, err
		}
		if needsRewrite {
			if _, err := dst.ExecContext(ctx, "UPDATE monitor_run_items SET artifact_dir=? WHERE id=?", rel, item.id); err != nil {
				return 0, rewritten, errGatewayCopyRead
			}
			rewritten++
		}
		sourceDir := filepath.Join(source, "artifacts", filepath.FromSlash(rel))
		gotBytes, gotSHA, err := gatewayCopyArtifactDigest(sourceDir)
		if err != nil || gotBytes == 0 {
			return 0, rewritten, fmt.Errorf("mysql_copy_artifact_missing: run_id=%s item_id=%d artifact_dir=%s has no files under %s", item.runID, item.id, rel, sourceDir)
		}
		if item.wantSHA != "" && (gotSHA != item.wantSHA || gotBytes != item.size) {
			return 0, rewritten, fmt.Errorf("mysql_copy_artifact_mismatch: run_id=%s item_id=%d artifact_dir=%s records %d bytes sha256=%s but the files are %d bytes sha256=%s", item.runID, item.id, rel, item.size, item.wantSHA, gotBytes, gotSHA)
		}
		if _, err := os.Stat(filepath.Join(target, "artifacts", filepath.FromSlash(rel))); err != nil {
			return 0, rewritten, fmt.Errorf("mysql_copy_artifact_missing: run_id=%s item_id=%d artifact_dir=%s was not copied to %s", item.runID, item.id, rel, target)
		}
		count++
	}
	return count, rewritten, nil
}

// gatewayCopyVerifySkillRows proves every platform skill package exists at the
// target. storage_dir is data-dir relative in current stores; a legacy absolute
// value inside the source root is rewritten after draining the SELECT cursor.
func gatewayCopyVerifySkillRows(ctx context.Context, dst *sql.Tx, source, target string, rewritten int64) (int64, int64, error) {
	type skillIndexRow struct {
		slug string
		dir  string
	}
	rows, err := dst.QueryContext(ctx, "SELECT slug, storage_dir FROM platform_skills WHERE storage_dir != ''")
	if err != nil {
		return 0, rewritten, errGatewayCopyRead
	}
	var skills []skillIndexRow
	for rows.Next() {
		var item skillIndexRow
		if err := rows.Scan(&item.slug, &item.dir); err != nil {
			rows.Close()
			return 0, rewritten, errGatewayCopyRead
		}
		skills = append(skills, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, rewritten, errGatewayCopyRead
	}
	if err := rows.Close(); err != nil {
		return 0, rewritten, errGatewayCopyRead
	}

	var count int64
	for _, item := range skills {
		rel, needsRewrite, err := gatewayCopyResolveSkillDir(source, item.slug, item.dir)
		if err != nil {
			return 0, rewritten, err
		}
		if needsRewrite {
			if _, err := dst.ExecContext(ctx, "UPDATE platform_skills SET storage_dir=? WHERE slug=?", rel, item.slug); err != nil {
				return 0, rewritten, errGatewayCopyRead
			}
			rewritten++
		}
		if info, err := os.Stat(filepath.Join(source, filepath.FromSlash(rel))); err != nil || !info.IsDir() {
			return 0, rewritten, fmt.Errorf("mysql_copy_skill_missing: platform_skills slug=%s storage_dir=%s does not exist under %s", item.slug, rel, source)
		}
		if info, err := os.Stat(filepath.Join(target, filepath.FromSlash(rel))); err != nil || !info.IsDir() {
			return 0, rewritten, fmt.Errorf("mysql_copy_skill_missing: platform_skills slug=%s storage_dir=%s was not copied to %s", item.slug, rel, target)
		}
		count++
	}
	return count, rewritten, nil
}
