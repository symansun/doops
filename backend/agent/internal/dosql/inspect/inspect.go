package inspect

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SchemaIR is the canonical, noise-stripped structural representation of a database.
// Runtime noise (AUTO_INCREMENT counter, physical storage stats) is excluded from Hash.
type SchemaIR struct {
	Engine     string    `json:"engine"`
	Database   string    `json:"database"`
	SchemaHash string    `json:"schema_hash"`
	TableCount int       `json:"table_count"`
	Tables     []TableIR `json:"tables"`
}

type TableIR struct {
	Name          string         `json:"name"`
	Kind          string         `json:"kind"` // TABLE | VIEW | COLLECTION
	Engine        string         `json:"engine,omitempty"`
	Charset       string         `json:"charset,omitempty"`
	EstimatedRows int64          `json:"estimated_rows,omitempty"`
	Comment       string         `json:"comment,omitempty"`
	Columns       []ColumnIR     `json:"columns,omitempty"`
	Indexes       []IndexIR      `json:"indexes,omitempty"`
	ForeignKeys   []ForeignKeyIR `json:"foreign_keys,omitempty"`
}

type ColumnIR struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
	Extra    string `json:"extra,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

type IndexIR struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary,omitempty"`
	Columns []string `json:"columns"`
	Type    string   `json:"type,omitempty"`
}

type ForeignKeyIR struct {
	Name              string   `json:"name"`
	Columns           []string `json:"columns"`
	ReferencedTable   string   `json:"referenced_table"`
	ReferencedColumns []string `json:"referenced_columns"`
	OnUpdate          string   `json:"on_update,omitempty"`
	OnDelete          string   `json:"on_delete,omitempty"`
}

type Options struct {
	Tables      []string
	SummaryOnly bool
}

// ComputeHash produces a deterministic structural hash ignoring runtime noise
// (EstimatedRows is zeroed before hashing).
func (s *SchemaIR) ComputeHash() string {
	clone := SchemaIR{
		Engine:   s.Engine,
		Database: s.Database,
		Tables:   make([]TableIR, len(s.Tables)),
	}
	for i, t := range s.Tables {
		t.EstimatedRows = 0
		clone.Tables[i] = t
	}
	sort.Slice(clone.Tables, func(i, j int) bool { return clone.Tables[i].Name < clone.Tables[j].Name })
	raw, _ := json.Marshal(clone)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// InspectSQL inspects MySQL or PostgreSQL schemas via batched catalog queries.
func InspectSQL(ctx context.Context, db *sql.DB, engine, database string, opts Options) (*SchemaIR, error) {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "mysql":
		return inspectMySQL(ctx, db, database, opts)
	case "postgres":
		return inspectPostgres(ctx, db, database, opts)
	default:
		return nil, fmt.Errorf("dosql_inspect_unsupported: engine %q is not yet supported for schema inspect", engine)
	}
}

func inspectMySQL(ctx context.Context, db *sql.DB, database string, opts Options) (*SchemaIR, error) {
	filter := toFilterSet(opts.Tables)
	rows, err := db.QueryContext(ctx, `
		SELECT TABLE_NAME, TABLE_TYPE, COALESCE(ENGINE,''), COALESCE(TABLE_COLLATION,''),
		       COALESCE(TABLE_ROWS, 0), COALESCE(TABLE_COMMENT,'')
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ?
		ORDER BY TABLE_NAME`, database)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tableMap := map[string]*TableIR{}
	var order []string
	for rows.Next() {
		var name, kind, eng, coll, comment string
		var est int64
		if err := rows.Scan(&name, &kind, &eng, &coll, &est, &comment); err != nil {
			return nil, err
		}
		if len(filter) > 0 && !filter[name] {
			continue
		}
		tKind := "TABLE"
		if strings.Contains(strings.ToUpper(kind), "VIEW") {
			tKind = "VIEW"
		}
		charset := ""
		if idx := strings.IndexByte(coll, '_'); idx > 0 {
			charset = coll[:idx]
		}
		tableMap[name] = &TableIR{
			Name: name, Kind: tKind, Engine: eng, Charset: charset,
			EstimatedRows: est, Comment: comment,
		}
		order = append(order, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if !opts.SummaryOnly && len(order) > 0 {
		if err := loadMySQLColumns(ctx, db, database, tableMap); err != nil {
			return nil, err
		}
		if err := loadMySQLIndexes(ctx, db, database, tableMap); err != nil {
			return nil, err
		}
		if err := loadMySQLForeignKeys(ctx, db, database, tableMap); err != nil {
			return nil, err
		}
	}

	ir := &SchemaIR{Engine: "mysql", Database: database, TableCount: len(order), Tables: make([]TableIR, 0, len(order))}
	for _, name := range order {
		ir.Tables = append(ir.Tables, *tableMap[name])
	}
	ir.SchemaHash = ir.ComputeHash()
	return ir, nil
}

func loadMySQLColumns(ctx context.Context, db *sql.DB, database string, tables map[string]*TableIR) error {
	rows, err := db.QueryContext(ctx, `
		SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE,
		       COALESCE(COLUMN_DEFAULT,''), COALESCE(EXTRA,''), COALESCE(COLUMN_COMMENT,'')
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ?
		ORDER BY TABLE_NAME, ORDINAL_POSITION`, database)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tbl, name, colType, nullable, def, extra, comment string
		if err := rows.Scan(&tbl, &name, &colType, &nullable, &def, &extra, &comment); err != nil {
			return err
		}
		if t, ok := tables[tbl]; ok {
			t.Columns = append(t.Columns, ColumnIR{
				Name: name, Type: strings.ToLower(colType),
				Nullable: strings.EqualFold(nullable, "YES"),
				Default:  def, Extra: extra, Comment: comment,
			})
		}
	}
	return rows.Err()
}

func loadMySQLIndexes(ctx context.Context, db *sql.DB, database string, tables map[string]*TableIR) error {
	rows, err := db.QueryContext(ctx, `
		SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME, COALESCE(INDEX_TYPE,'')
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ?
		ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`, database)
	if err != nil {
		return err
	}
	defer rows.Close()
	type idxKey struct{ tbl, name string }
	idxMap := map[idxKey]*IndexIR{}
	var keys []idxKey
	for rows.Next() {
		var tbl, name, col, idxType string
		var nonUnique int
		if err := rows.Scan(&tbl, &name, &nonUnique, &col, &idxType); err != nil {
			return err
		}
		if _, ok := tables[tbl]; !ok {
			continue
		}
		k := idxKey{tbl, name}
		entry, seen := idxMap[k]
		if !seen {
			entry = &IndexIR{
				Name: name, Unique: nonUnique == 0, Primary: name == "PRIMARY", Type: idxType,
			}
			idxMap[k] = entry
			keys = append(keys, k)
		}
		entry.Columns = append(entry.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range keys {
		tables[k.tbl].Indexes = append(tables[k.tbl].Indexes, *idxMap[k])
	}
	return nil
}

func loadMySQLForeignKeys(ctx context.Context, db *sql.DB, database string, tables map[string]*TableIR) error {
	rows, err := db.QueryContext(ctx, `
		SELECT kcu.TABLE_NAME, kcu.CONSTRAINT_NAME, kcu.COLUMN_NAME,
		       kcu.REFERENCED_TABLE_NAME, kcu.REFERENCED_COLUMN_NAME,
		       COALESCE(rc.UPDATE_RULE,''), COALESCE(rc.DELETE_RULE,'')
		FROM information_schema.KEY_COLUMN_USAGE kcu
		JOIN information_schema.REFERENTIAL_CONSTRAINTS rc
		  ON rc.CONSTRAINT_SCHEMA = kcu.TABLE_SCHEMA AND rc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME
		WHERE kcu.TABLE_SCHEMA = ? AND kcu.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY kcu.TABLE_NAME, kcu.CONSTRAINT_NAME, kcu.ORDINAL_POSITION`, database)
	if err != nil {
		return err
	}
	defer rows.Close()
	type fkKey struct{ tbl, name string }
	fkMap := map[fkKey]*ForeignKeyIR{}
	var keys []fkKey
	for rows.Next() {
		var tbl, name, col, refTbl, refCol, onUpd, onDel string
		if err := rows.Scan(&tbl, &name, &col, &refTbl, &refCol, &onUpd, &onDel); err != nil {
			return err
		}
		if _, ok := tables[tbl]; !ok {
			continue
		}
		k := fkKey{tbl, name}
		entry, seen := fkMap[k]
		if !seen {
			entry = &ForeignKeyIR{Name: name, ReferencedTable: refTbl, OnUpdate: onUpd, OnDelete: onDel}
			fkMap[k] = entry
			keys = append(keys, k)
		}
		entry.Columns = append(entry.Columns, col)
		entry.ReferencedColumns = append(entry.ReferencedColumns, refCol)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range keys {
		tables[k.tbl].ForeignKeys = append(tables[k.tbl].ForeignKeys, *fkMap[k])
	}
	return nil
}

func inspectPostgres(ctx context.Context, db *sql.DB, database string, opts Options) (*SchemaIR, error) {
	filter := toFilterSet(opts.Tables)
	rows, err := db.QueryContext(ctx, `
		SELECT c.relname, c.relkind, COALESCE(c.reltuples::bigint, 0), COALESCE(obj_description(c.oid, 'pg_class'), '')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relkind IN ('r','p','v','m')
		ORDER BY c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tableMap := map[string]*TableIR{}
	var order []string
	for rows.Next() {
		var name, relkind, comment string
		var est int64
		if err := rows.Scan(&name, &relkind, &est, &comment); err != nil {
			return nil, err
		}
		if len(filter) > 0 && !filter[name] {
			continue
		}
		kind := "TABLE"
		if relkind == "v" || relkind == "m" {
			kind = "VIEW"
		}
		if est < 0 {
			est = 0
		}
		tableMap[name] = &TableIR{Name: name, Kind: kind, EstimatedRows: est, Comment: comment}
		order = append(order, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if !opts.SummaryOnly && len(order) > 0 {
		colRows, err := db.QueryContext(ctx, `
			SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod),
			       NOT a.attnotnull, COALESCE(pg_get_expr(d.adbin, d.adrelid), ''),
			       COALESCE(col_description(c.oid, a.attnum), '')
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
			LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
			WHERE n.nspname = current_schema() AND c.relkind IN ('r','p','v','m')
			ORDER BY c.relname, a.attnum`)
		if err != nil {
			return nil, err
		}
		defer colRows.Close()
		for colRows.Next() {
			var tbl, name, colType, def, comment string
			var nullable bool
			if err := colRows.Scan(&tbl, &name, &colType, &nullable, &def, &comment); err != nil {
				return nil, err
			}
			if t, ok := tableMap[tbl]; ok {
				t.Columns = append(t.Columns, ColumnIR{
					Name: name, Type: colType, Nullable: nullable, Default: def, Comment: comment,
				})
			}
		}
		if err := colRows.Err(); err != nil {
			return nil, err
		}
		if err := loadPostgresIndexes(ctx, db, tableMap); err != nil {
			return nil, err
		}
		if err := loadPostgresForeignKeys(ctx, db, tableMap); err != nil {
			return nil, err
		}
	}

	ir := &SchemaIR{Engine: "postgres", Database: database, TableCount: len(order), Tables: make([]TableIR, 0, len(order))}
	for _, name := range order {
		ir.Tables = append(ir.Tables, *tableMap[name])
	}
	ir.SchemaHash = ir.ComputeHash()
	return ir, nil
}

func loadPostgresIndexes(ctx context.Context, db *sql.DB, tables map[string]*TableIR) error {
	rows, err := db.QueryContext(ctx, `
		SELECT t.relname, i.relname, ix.indisunique, ix.indisprimary,
		       a.attname, COALESCE(am.amname, '')
		FROM pg_index ix
		JOIN pg_class t ON t.oid = ix.indrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_am am ON am.oid = i.relam
		JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		WHERE n.nspname = current_schema() AND t.relkind IN ('r','p')
		ORDER BY t.relname, i.relname, k.ord`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type idxKey struct{ tbl, name string }
	idxMap := map[idxKey]*IndexIR{}
	var keys []idxKey
	for rows.Next() {
		var tbl, name, col, amName string
		var unique, primary bool
		if err := rows.Scan(&tbl, &name, &unique, &primary, &col, &amName); err != nil {
			return err
		}
		if _, ok := tables[tbl]; !ok {
			continue
		}
		k := idxKey{tbl, name}
		entry, seen := idxMap[k]
		if !seen {
			entry = &IndexIR{Name: name, Unique: unique, Primary: primary, Type: strings.ToUpper(amName)}
			idxMap[k] = entry
			keys = append(keys, k)
		}
		entry.Columns = append(entry.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range keys {
		tables[k.tbl].Indexes = append(tables[k.tbl].Indexes, *idxMap[k])
	}
	return nil
}

func loadPostgresForeignKeys(ctx context.Context, db *sql.DB, tables map[string]*TableIR) error {
	rows, err := db.QueryContext(ctx, `
		SELECT src.relname, c.conname, la.attname, ref.relname, ra.attname,
		       c.confupdtype::text, c.confdeltype::text
		FROM pg_constraint c
		JOIN pg_class src ON src.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = src.relnamespace
		JOIN pg_class ref ON ref.oid = c.confrelid
		JOIN LATERAL unnest(c.conkey, c.confkey) WITH ORDINALITY AS cols(lattnum, rattnum, ord) ON true
		JOIN pg_attribute la ON la.attrelid = c.conrelid AND la.attnum = cols.lattnum
		JOIN pg_attribute ra ON ra.attrelid = c.confrelid AND ra.attnum = cols.rattnum
		WHERE n.nspname = current_schema() AND c.contype = 'f'
		ORDER BY src.relname, c.conname, cols.ord`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type fkKey struct{ tbl, name string }
	fkMap := map[fkKey]*ForeignKeyIR{}
	var keys []fkKey
	for rows.Next() {
		var tbl, name, col, refTbl, refCol, updCode, delCode string
		if err := rows.Scan(&tbl, &name, &col, &refTbl, &refCol, &updCode, &delCode); err != nil {
			return err
		}
		if _, ok := tables[tbl]; !ok {
			continue
		}
		k := fkKey{tbl, name}
		entry, seen := fkMap[k]
		if !seen {
			entry = &ForeignKeyIR{
				Name:            name,
				ReferencedTable: refTbl,
				OnUpdate:        pgFKActionName(updCode),
				OnDelete:        pgFKActionName(delCode),
			}
			fkMap[k] = entry
			keys = append(keys, k)
		}
		entry.Columns = append(entry.Columns, col)
		entry.ReferencedColumns = append(entry.ReferencedColumns, refCol)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range keys {
		tables[k.tbl].ForeignKeys = append(tables[k.tbl].ForeignKeys, *fkMap[k])
	}
	return nil
}

func pgFKActionName(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "a":
		return "NO ACTION"
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	default:
		return strings.ToUpper(strings.TrimSpace(code))
	}
}

func toFilterSet(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out[n] = true
		}
	}
	return out
}
