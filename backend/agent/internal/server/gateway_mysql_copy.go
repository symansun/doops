package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Only counts and a digest of the verified, typed contents cross the copy boundary.
type gatewayMySQLCopyReport struct {
	TableRows map[string]int64
	SHA256    string
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
}

var (
	errGatewayCopySchema  = errors.New("mysql_copy_schema_mismatch")
	errGatewayCopyStale   = errors.New("mysql_copy_destination_stale")
	errGatewayCopyContent = errors.New("mysql_copy_content_mismatch")
	errGatewayCopyRead    = errors.New("mysql_copy_read_failed")
)

// copyGatewaySQLiteToMySQL requires an offline destination with the reviewed
// MySQL schema already installed. Both modes exercise the same inserts and
// readback. Dry run rolls back rows. InnoDB allocator reservations survive a
// rollback, so a failed/rehearsed target may require fresh provisioning.
func copyGatewaySQLiteToMySQL(ctx context.Context, sqliteDB, mysqlDB *sql.DB, dryRun bool) (*gatewayMySQLCopyReport, error) {
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
	// information_schema normally caches AUTO_INCREMENT for 24 hours. Use live
	// dictionary values on this private session, which is closed before return.
	if _, err := conn.ExecContext(ctx, `SET SESSION information_schema_stats_expiry=0,
		sql_mode=CONCAT_WS(',',NULLIF(@@SESSION.sql_mode,''),'NO_AUTO_VALUE_ON_ZERO')`); err != nil {
		return nil, errors.New("mysql_copy_unsafe_session")
	}
	opts := &sql.TxOptions{Isolation: sql.LevelSerializable}
	dst, err := conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, errors.New("mysql_copy_begin_failed")
	}
	defer dst.Rollback()
	tables, err := gatewayCopyPlan(ctx, src, dst)
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

	report := &gatewayMySQLCopyReport{TableRows: make(map[string]int64, len(tables))}
	digests := make(map[string]string, len(tables))
	for _, t := range tables {
		names := make([]string, len(t.columns))
		for i, c := range t.columns {
			names[i] = c.name
		}
		stmt, err := dst.PrepareContext(ctx, doSQLCopyInsert("mysql", t.name, names, 1))
		if err != nil {
			return nil, errors.New("mysql_copy_prepare_failed")
		}
		n, digest, err := t.stream(ctx, src, "sqlite", stmt)
		closeErr := stmt.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, errors.New("mysql_copy_statement_failed")
		}
		report.TableRows[t.name], digests[t.name] = n, digest
	}
	// Read every destination after ALL inserts, including tables copied first.
	// This catches coercions and changes from constraints or triggers at equal count.
	overall := sha256.New()
	for _, t := range tables {
		n, digest, err := t.stream(ctx, dst, "mysql", nil)
		if err != nil {
			return nil, err
		}
		if n != report.TableRows[t.name] || digest != digests[t.name] {
			return nil, errGatewayCopyContent
		}
		if t.autoID != "" {
			next, err := gatewayCopyNextID(ctx, dst, t.name)
			if err != nil || (next != t.highID+1 && !(next == 1 && t.highID == 0)) {
				return nil, errors.New("mysql_copy_allocator_mismatch")
			}
		}
		fmt.Fprintf(overall, "%q %d %x %d\n", t.name, n, digest, t.highID)
	}
	report.SHA256 = hex.EncodeToString(overall.Sum(nil))
	if err := src.Commit(); err != nil {
		return nil, errGatewayCopyRead
	}
	if dryRun {
		if err := dst.Rollback(); err != nil {
			return nil, errors.New("mysql_copy_rollback_failed")
		}
	} else if err := dst.Commit(); err != nil {
		// A broken connection during COMMIT has an indeterminate outcome.
		return nil, errors.New("mysql_copy_commit_unconfirmed")
	}
	return report, nil
}

func gatewayCopyPlan(ctx context.Context, src, dst *sql.Tx) ([]*gatewayCopyTable, error) {
	var safe bool
	if err := dst.QueryRowContext(ctx, `SELECT @@foreign_key_checks=1 AND @@unique_checks=1 AND
		@@auto_increment_increment=1 AND @@auto_increment_offset=1 AND @@character_set_client='utf8mb4'
		AND @@character_set_connection='utf8mb4' AND @@character_set_results='utf8mb4'
		AND FIND_IN_SET('NO_AUTO_VALUE_ON_ZERO', @@sql_mode) > 0`).Scan(&safe); err != nil || !safe {
		return nil, errors.New("mysql_copy_unsafe_session")
	}
	var orphan bool
	if err := src.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM main.pragma_foreign_key_check)`).Scan(&orphan); err != nil || orphan {
		return nil, errors.New("mysql_copy_source_foreign_key_failed")
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
	err = gatewayCopyRows(ctx, dst, `SELECT TABLE_NAME, TABLE_TYPE, COALESCE(ENGINE,'') FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()`, func(r *sql.Rows) error {
		var name, kind, engine string
		if r.Scan(&name, &kind, &engine) != nil || byName[name] == nil || kind != "BASE TABLE" || engine != "InnoDB" {
			return errGatewayCopySchema
		}
		seen++
		return nil
	})
	if err != nil {
		return nil, err
	}
	if seen == 0 || seen != len(byName) {
		return nil, errGatewayCopySchema
	}
	err = gatewayCopyRows(ctx, dst, `SELECT TABLE_NAME, REFERENCED_TABLE_NAME, REFERENCED_TABLE_SCHEMA=DATABASE()
		FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND REFERENCED_TABLE_NAME IS NOT NULL`, func(r *sql.Rows) error {
		var child, parent string
		var local bool
		if r.Scan(&child, &parent, &local) != nil || !local || byName[parent] == nil || byName[child] == nil {
			return errGatewayCopySchema
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
		if err := t.planColumns(ctx, src, dst, sequences); err != nil {
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

func (t *gatewayCopyTable) planColumns(ctx context.Context, src, dst *sql.Tx, sequences map[string]int64) error {
	keyParts := make(map[int]string)
	// This copier handles the Gateway's native SQLite storage classes. Unknown
	// legacy types and generated/hidden source columns require an explicit map.
	err := gatewayCopyRows(ctx, src, `SELECT name, upper(type), pk, hidden FROM pragma_table_xinfo(?, 'main') ORDER BY cid`, func(r *sql.Rows) error {
		var name, typ string
		var pk, hidden int
		if r.Scan(&name, &typ, &pk, &hidden) != nil || hidden != 0 {
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
	err = gatewayCopyRows(ctx, dst, `SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, func(r *sql.Rows) error {
		var name, typ, columnType, extra string
		if r.Scan(&name, &typ, &columnType, &extra) != nil {
			return errGatewayCopyRead
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

// stream hashes length-framed, typed values in primary-key order with explicit
// binary text ordering on both engines. Memory use is bounded by one row.
func (t *gatewayCopyTable) stream(ctx context.Context, tx *sql.Tx, engine string, insert *sql.Stmt) (int64, string, error) {
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
	q := "SELECT " + strings.Join(projection, ",") + " FROM " + table + " ORDER BY " + strings.Join(order, ",")
	values, pointers := make([]any, len(t.columns)), make([]any, len(t.columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	var count int64
	err := gatewayCopyRows(ctx, tx, q, func(r *sql.Rows) error {
		if err := r.Scan(pointers...); err != nil {
			return errGatewayCopyRead
		}
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
					return errGatewayCopyContent
				}
			case float64:
				if c.kind != 'r' {
					return errGatewayCopyContent
				}
				data = binary.BigEndian.AppendUint64(nil, math.Float64bits(v))
			case string:
				if c.kind != 't' {
					return errGatewayCopyContent
				}
				data = []byte(v)
			case []byte:
				// MySQL returns []byte for both TEXT and BLOB. SQLite returns
				// string for TEXT and typed nil []byte for a zero-length BLOB.
				if c.kind != 'b' && !(engine == "mysql" && c.kind == 't') {
					return errGatewayCopyContent
				}
				if v == nil {
					v = []byte{}
					values[i] = v
				}
				data = v
			default:
				return errGatewayCopyContent
			}
			h.Write([]byte{c.kind})
			h.Write(binary.AppendUvarint(nil, uint64(len(data))))
			h.Write(data)
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
