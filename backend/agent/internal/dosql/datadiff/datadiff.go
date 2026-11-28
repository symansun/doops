package datadiff

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

type DiffKind string

const (
	DiffMissingInTarget DiffKind = "missing_in_target"
	DiffExtraInTarget   DiffKind = "extra_in_target"
	DiffValueMismatch   DiffKind = "value_mismatch"
)

type RowDiff struct {
	Kind   DiffKind          `json:"kind"`
	Key    string            `json:"key"`
	Source map[string]string `json:"source,omitempty"`
	Target map[string]string `json:"target,omitempty"`
	FixSQL string            `json:"fix_sql,omitempty"`
}

type TableDiffResult struct {
	Engine        string    `json:"engine"`
	Table         string    `json:"table"`
	KeyColumn     string    `json:"key_column"`
	ChunksChecked int       `json:"chunks_checked"`
	ChunksMatched int       `json:"chunks_matched"`
	ChunksDiffed  int       `json:"chunks_diffed"`
	Identical     bool      `json:"identical"`
	DiffCount     int       `json:"diff_count"`
	Truncated     bool      `json:"truncated,omitempty"`
	Diffs         []RowDiff `json:"diffs"`
}

type Options struct {
	Engine      string
	Table       string
	KeyColumn   string
	Columns     []string
	ChunkSize   int64 // default 10000
	LeafSize    int64 // default 500 (below this, stream rows and merge-join)
	MaxDiffs    int   // default 100
	GenerateFix bool
}

type chunkRange struct {
	Low  int64
	High int64 // exclusive
}

// CompareIntKeyTables compares two tables sharing an integer primary/unique key
// using the TiDB sync-diff-inspector / pt-table-checksum algorithm:
// 1) Split [MIN(key), MAX(key)] into chunks
// 2) Push down COUNT(*) + order-independent hash checksum per chunk
// 3) Recursively bisect mismatched chunks until <= LeafSize
// 4) Merge-join leaf rows in memory and optionally generate fix SQL.
func CompareIntKeyTables(ctx context.Context, srcDB, dstDB *sql.DB, opts Options) (*TableDiffResult, error) {
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = 10000
	}
	if opts.LeafSize <= 0 {
		opts.LeafSize = 500
	}
	if opts.MaxDiffs <= 0 {
		opts.MaxDiffs = 100
	}
	if opts.Table == "" || opts.KeyColumn == "" || len(opts.Columns) == 0 {
		return nil, fmt.Errorf("dosql_datadiff_invalid: table, key_column, and columns are required")
	}

	minKey, maxKey, hasRows, err := keyBounds(ctx, srcDB, dstDB, opts.Engine, opts.Table, opts.KeyColumn)
	if err != nil {
		return nil, err
	}
	res := &TableDiffResult{
		Engine:    opts.Engine,
		Table:     opts.Table,
		KeyColumn: opts.KeyColumn,
		Identical: true,
	}
	if !hasRows {
		return res, nil
	}

	var queue []chunkRange
	for lo := minKey; lo <= maxKey; lo += opts.ChunkSize {
		hi := lo + opts.ChunkSize
		if hi > maxKey+1 {
			hi = maxKey + 1
		}
		queue = append(queue, chunkRange{Low: lo, High: hi})
	}

	for len(queue) > 0 {
		ch := queue[0]
		queue = queue[1:]
		res.ChunksChecked++

		srcCnt, srcSum, err := chunkChecksum(ctx, srcDB, opts, ch)
		if err != nil {
			return nil, err
		}
		dstCnt, dstSum, err := chunkChecksum(ctx, dstDB, opts, ch)
		if err != nil {
			return nil, err
		}
		if srcCnt == dstCnt && srcSum == dstSum {
			res.ChunksMatched++
			continue
		}
		res.ChunksDiffed++
		res.Identical = false

		span := ch.High - ch.Low
		if span > opts.LeafSize && (srcCnt > opts.LeafSize || dstCnt > opts.LeafSize) {
			mid := ch.Low + span/2
			queue = append([]chunkRange{{Low: ch.Low, High: mid}, {Low: mid, High: ch.High}}, queue...)
			continue
		}

		// Leaf chunk: fetch ordered rows from both sides and merge-join.
		srcRows, err := fetchChunkRows(ctx, srcDB, opts, ch)
		if err != nil {
			return nil, err
		}
		dstRows, err := fetchChunkRows(ctx, dstDB, opts, ch)
		if err != nil {
			return nil, err
		}
		diffs := mergeCompareRows(opts, srcRows, dstRows)
		for _, d := range diffs {
			res.DiffCount++
			if len(res.Diffs) < opts.MaxDiffs {
				res.Diffs = append(res.Diffs, d)
			} else {
				res.Truncated = true
			}
		}
	}
	return res, nil
}

type orderedRow struct {
	Key    int64
	Values map[string]string
}

func keyBounds(ctx context.Context, srcDB, dstDB *sql.DB, engine, table, keyCol string) (int64, int64, bool, error) {
	q := fmt.Sprintf("SELECT MIN(%s), MAX(%s) FROM %s", quoteID(engine, keyCol), quoteID(engine, keyCol), quoteID(engine, table))
	var sMin, sMax, dMin, dMax sql.NullInt64
	if err := srcDB.QueryRowContext(ctx, q).Scan(&sMin, &sMax); err != nil {
		return 0, 0, false, err
	}
	if err := dstDB.QueryRowContext(ctx, q).Scan(&dMin, &dMax); err != nil {
		return 0, 0, false, err
	}
	if !sMin.Valid && !dMin.Valid {
		return 0, 0, false, nil
	}
	minVal, maxVal := sMin.Int64, sMax.Int64
	if !sMin.Valid || (dMin.Valid && dMin.Int64 < minVal) {
		minVal = dMin.Int64
	}
	if !sMax.Valid || (dMax.Valid && dMax.Int64 > maxVal) {
		maxVal = dMax.Int64
	}
	return minVal, maxVal, true, nil
}

// chunkChecksum computes an order-independent chunk hash. On MySQL/PG it pushes
// down BIT_XOR(MD5(...)); on SQLite (used in local unit tests) it falls back to
// streaming XOR over SHA-256 of canonical row representations.
func chunkChecksum(ctx context.Context, db *sql.DB, opts Options, ch chunkRange) (int64, string, error) {
	switch opts.Engine {
	case "mysql":
		exprs := make([]string, len(opts.Columns))
		for i, col := range opts.Columns {
			exprs[i] = fmt.Sprintf("IFNULL(CAST(%s AS CHAR), '__DOSQL_NULL__')", quoteID("mysql", col))
		}
		q := fmt.Sprintf(
			"SELECT COUNT(*), COALESCE(BIT_XOR(CAST(CONV(SUBSTRING(MD5(CONCAT_WS('#', %s)), 1, 16), 16, 10) AS UNSIGNED)), 0) FROM %s WHERE %s >= ? AND %s < ?",
			strings.Join(exprs, ", "), quoteID("mysql", opts.Table), quoteID("mysql", opts.KeyColumn), quoteID("mysql", opts.KeyColumn),
		)
		var cnt int64
		var cksum string
		if err := db.QueryRowContext(ctx, q, ch.Low, ch.High).Scan(&cnt, &cksum); err != nil {
			return 0, "", err
		}
		return cnt, cksum, nil

	case "postgres":
		exprs := make([]string, len(opts.Columns))
		for i, col := range opts.Columns {
			exprs[i] = fmt.Sprintf("COALESCE(CAST(%s AS TEXT), '__DOSQL_NULL__')", quoteID("postgres", col))
		}
		q := fmt.Sprintf(
			"SELECT COUNT(*), COALESCE(BIT_XOR(('x' || substr(md5(concat_ws('#', %s)), 1, 16))::bit(64)::bigint), 0)::text FROM %s WHERE %s >= $1 AND %s < $2",
			strings.Join(exprs, ", "), quoteID("postgres", opts.Table), quoteID("postgres", opts.KeyColumn), quoteID("postgres", opts.KeyColumn),
		)
		var cnt int64
		var cksum string
		if err := db.QueryRowContext(ctx, q, ch.Low, ch.High).Scan(&cnt, &cksum); err != nil {
			return 0, "", err
		}
		return cnt, cksum, nil

	default:
		// Generic fallback (e.g. SQLite): compute order-independent XOR of row SHA-256 hashes.
		rows, err := fetchChunkRows(ctx, db, opts, ch)
		if err != nil {
			return 0, "", err
		}
		var xor [32]byte
		for _, r := range rows {
			var b strings.Builder
			for _, col := range opts.Columns {
				b.WriteString(r.Values[col])
				b.WriteByte(0x1f)
			}
			sum := sha256.Sum256([]byte(b.String()))
			for i := range xor {
				xor[i] ^= sum[i]
			}
		}
		return int64(len(rows)), hex.EncodeToString(xor[:]), nil
	}
}

func fetchChunkRows(ctx context.Context, db *sql.DB, opts Options, ch chunkRange) ([]orderedRow, error) {
	cols := make([]string, 0, len(opts.Columns)+1)
	cols = append(cols, quoteID(opts.Engine, opts.KeyColumn))
	for _, c := range opts.Columns {
		cols = append(cols, quoteID(opts.Engine, c))
	}
	p1, p2 := "?", "?"
	if opts.Engine == "postgres" {
		p1, p2 = "$1", "$2"
	}
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s >= %s AND %s < %s ORDER BY %s ASC",
		strings.Join(cols, ", "), quoteID(opts.Engine, opts.Table),
		quoteID(opts.Engine, opts.KeyColumn), p1, quoteID(opts.Engine, opts.KeyColumn), p2,
		quoteID(opts.Engine, opts.KeyColumn))
	rows, err := db.QueryContext(ctx, q, ch.Low, ch.High)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []orderedRow
	for rows.Next() {
		raw := make([]interface{}, len(opts.Columns)+1)
		ptrs := make([]interface{}, len(raw))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		key, err := toInt64(raw[0])
		if err != nil {
			return nil, err
		}
		vals := make(map[string]string, len(opts.Columns))
		for i, col := range opts.Columns {
			vals[col] = cellString(raw[i+1])
		}
		out = append(out, orderedRow{Key: key, Values: vals})
	}
	return out, rows.Err()
}

func mergeCompareRows(opts Options, src, dst []orderedRow) []RowDiff {
	var diffs []RowDiff
	i, j := 0, 0
	for i < len(src) && j < len(dst) {
		switch {
		case src[i].Key < dst[j].Key:
			d := RowDiff{Kind: DiffMissingInTarget, Key: fmt.Sprint(src[i].Key), Source: src[i].Values}
			if opts.GenerateFix {
				d.FixSQL = renderInsertSQL(opts, src[i])
			}
			diffs = append(diffs, d)
			i++
		case src[i].Key > dst[j].Key:
			d := RowDiff{Kind: DiffExtraInTarget, Key: fmt.Sprint(dst[j].Key), Target: dst[j].Values}
			if opts.GenerateFix {
				d.FixSQL = fmt.Sprintf("DELETE FROM %s WHERE %s = %d;", quoteID(opts.Engine, opts.Table), quoteID(opts.Engine, opts.KeyColumn), dst[j].Key)
			}
			diffs = append(diffs, d)
			j++
		default:
			if !mapsEqual(src[i].Values, dst[j].Values) {
				d := RowDiff{Kind: DiffValueMismatch, Key: fmt.Sprint(src[i].Key), Source: src[i].Values, Target: dst[j].Values}
				if opts.GenerateFix {
					d.FixSQL = renderUpdateSQL(opts, src[i])
				}
				diffs = append(diffs, d)
			}
			i++
			j++
		}
	}
	for ; i < len(src); i++ {
		d := RowDiff{Kind: DiffMissingInTarget, Key: fmt.Sprint(src[i].Key), Source: src[i].Values}
		if opts.GenerateFix {
			d.FixSQL = renderInsertSQL(opts, src[i])
		}
		diffs = append(diffs, d)
	}
	for ; j < len(dst); j++ {
		d := RowDiff{Kind: DiffExtraInTarget, Key: fmt.Sprint(dst[j].Key), Target: dst[j].Values}
		if opts.GenerateFix {
			d.FixSQL = fmt.Sprintf("DELETE FROM %s WHERE %s = %d;", quoteID(opts.Engine, opts.Table), quoteID(opts.Engine, opts.KeyColumn), dst[j].Key)
		}
		diffs = append(diffs, d)
	}
	return diffs
}

func renderInsertSQL(opts Options, row orderedRow) string {
	hasKey := false
	for _, c := range opts.Columns {
		if c == opts.KeyColumn {
			hasKey = true
			break
		}
	}
	cols := make([]string, 0, len(opts.Columns)+1)
	vals := make([]string, 0, len(opts.Columns)+1)
	if !hasKey {
		cols = append(cols, quoteID(opts.Engine, opts.KeyColumn))
		vals = append(vals, fmt.Sprint(row.Key))
	}
	for _, c := range opts.Columns {
		cols = append(cols, quoteID(opts.Engine, c))
		vals = append(vals, sqlLiteral(opts.Engine, row.Values[c]))
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", quoteID(opts.Engine, opts.Table), strings.Join(cols, ", "), strings.Join(vals, ", "))
}

func renderUpdateSQL(opts Options, row orderedRow) string {
	var sets []string
	for _, c := range opts.Columns {
		if c == opts.KeyColumn {
			continue
		}
		sets = append(sets, fmt.Sprintf("%s = %s", quoteID(opts.Engine, c), sqlLiteral(opts.Engine, row.Values[c])))
	}
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s = %d;", quoteID(opts.Engine, opts.Table), strings.Join(sets, ", "), quoteID(opts.Engine, opts.KeyColumn), row.Key)
}

func sqlLiteral(engine, v string) string {
	if v == "__DOSQL_NULL__" {
		return "NULL"
	}
	if engine == "mysql" {
		v = strings.ReplaceAll(v, `\`, `\\`)
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func cellString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "__DOSQL_NULL__"
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

func toInt64(v interface{}) (int64, error) {
	switch t := v.(type) {
	case int64:
		return t, nil
	case int:
		return int64(t), nil
	case int32:
		return int64(t), nil
	case int16:
		return int64(t), nil
	case int8:
		return int64(t), nil
	case uint64:
		return int64(t), nil
	case uint32:
		return int64(t), nil
	case uint16:
		return int64(t), nil
	case uint8:
		return int64(t), nil
	case []byte:
		return strconv.ParseInt(strings.TrimSpace(string(t)), 10, 64)
	case string:
		return strconv.ParseInt(strings.TrimSpace(t), 10, 64)
	default:
		return 0, fmt.Errorf("non-integer key value %T", v)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func quoteID(engine, name string) string {
	if engine == "mysql" {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
