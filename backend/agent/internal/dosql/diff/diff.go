package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/user/doops/agent/internal/dosql/inspect"
)

type ChangeKind string

const (
	ChangeAddTable       ChangeKind = "add_table"
	ChangeDropTable      ChangeKind = "drop_table"
	ChangeAddColumn      ChangeKind = "add_column"
	ChangeDropColumn     ChangeKind = "drop_column"
	ChangeModifyColumn   ChangeKind = "modify_column"
	ChangeAddIndex       ChangeKind = "add_index"
	ChangeDropIndex      ChangeKind = "drop_index"
	ChangeAddForeignKey  ChangeKind = "add_foreign_key"
	ChangeDropForeignKey ChangeKind = "drop_foreign_key"
)

type Change struct {
	Kind        ChangeKind `json:"kind"`
	Table       string     `json:"table"`
	Object      string     `json:"object,omitempty"`
	Before      string     `json:"before,omitempty"`
	After       string     `json:"after,omitempty"`
	Destructive bool       `json:"destructive,omitempty"`
	DDL         string     `json:"ddl,omitempty"`
}

type SchemaDiff struct {
	Engine          string   `json:"engine"`
	FromDatabase    string   `json:"from_database"`
	ToDatabase      string   `json:"to_database"`
	FromHash        string   `json:"from_hash"`
	ToHash          string   `json:"to_hash"`
	Identical       bool     `json:"identical"`
	HasDestructive  bool     `json:"has_destructive,omitempty"`
	Changes         []Change `json:"changes"`
	SuggestedDDL    []string `json:"suggested_ddl,omitempty"`
	PossibleRenames []string `json:"possible_renames,omitempty"`
}

// CompareSchemas computes a structural diff to transition from `current` to `desired`,
// ordering CREATE TABLE by FK topology (parents first) and DROP TABLE in reverse order.
func CompareSchemas(current, desired *inspect.SchemaIR, tablesFilter []string) SchemaDiff {
	engine := desired.Engine
	if engine == "" {
		engine = current.Engine
	}
	res := SchemaDiff{
		Engine:       engine,
		FromDatabase: current.Database,
		ToDatabase:   desired.Database,
		FromHash:     current.SchemaHash,
		ToHash:       desired.SchemaHash,
	}
	filter := map[string]bool{}
	for _, t := range tablesFilter {
		if t = strings.TrimSpace(t); t != "" {
			filter[t] = true
		}
	}

	curMap := map[string]inspect.TableIR{}
	for _, t := range current.Tables {
		if len(filter) == 0 || filter[t.Name] {
			curMap[t.Name] = t
		}
	}
	desMap := map[string]inspect.TableIR{}
	for _, t := range desired.Tables {
		if len(filter) == 0 || filter[t.Name] {
			desMap[t.Name] = t
		}
	}

	// 1. Added tables (sorted topologically by FK dependencies)
	var added []inspect.TableIR
	for name, dt := range desMap {
		if _, ok := curMap[name]; !ok {
			added = append(added, dt)
		}
	}
	for _, dt := range topoSortTables(added) {
		ddls := renderCreateTableStatements(engine, dt)
		res.Changes = append(res.Changes, Change{
			Kind: ChangeAddTable, Table: dt.Name, DDL: strings.Join(ddls, "\n"),
		})
		res.SuggestedDDL = append(res.SuggestedDDL, ddls...)
	}

	// 2. Modified tables (deterministic name order)
	var commonNames []string
	for name := range desMap {
		if _, ok := curMap[name]; ok {
			commonNames = append(commonNames, name)
		}
	}
	sort.Strings(commonNames)
	for _, name := range commonNames {
		tblChanges, renames := CompareTables(engine, curMap[name], desMap[name])
		res.Changes = append(res.Changes, tblChanges...)
		res.PossibleRenames = append(res.PossibleRenames, renames...)
		for _, c := range tblChanges {
			if c.Destructive {
				res.HasDestructive = true
			}
			if c.DDL != "" {
				res.SuggestedDDL = append(res.SuggestedDDL, c.DDL)
			}
		}
	}

	// 3. Dropped tables (reverse topological order: children before parents)
	var dropped []inspect.TableIR
	for name, ct := range curMap {
		if _, ok := desMap[name]; !ok {
			dropped = append(dropped, ct)
		}
	}
	sortedDropped := topoSortTables(dropped)
	for i := len(sortedDropped) - 1; i >= 0; i-- {
		ct := sortedDropped[i]
		ddl := fmt.Sprintf("DROP TABLE %s;", quoteIdent(engine, ct.Name))
		res.Changes = append(res.Changes, Change{
			Kind: ChangeDropTable, Table: ct.Name, Destructive: true, DDL: ddl,
		})
		res.HasDestructive = true
		res.SuggestedDDL = append(res.SuggestedDDL, ddl)
	}

	res.Identical = len(res.Changes) == 0
	return res
}

// CompareTables compares columns, indexes, and foreign keys between two TableIRs.
func CompareTables(engine string, current, desired inspect.TableIR) ([]Change, []string) {
	var changes []Change
	var renames []string

	// Foreign keys dropped first so column/index alters succeed.
	curFKs := indexFKs(current.ForeignKeys)
	desFKs := indexFKs(desired.ForeignKeys)
	for _, name := range sortedKeys(curFKs) {
		if dfk, ok := desFKs[name]; !ok || !fkEqual(curFKs[name], dfk) {
			changes = append(changes, Change{
				Kind: ChangeDropForeignKey, Table: current.Name, Object: name,
				Before: formatFK(curFKs[name]), Destructive: !ok,
				DDL: renderDropFK(engine, current.Name, name),
			})
		}
	}

	// Indexes dropped before column drops/modifies.
	curIdxs := indexIndexes(current.Indexes)
	desIdxs := indexIndexes(desired.Indexes)
	for _, name := range sortedKeys(curIdxs) {
		if name == "PRIMARY" || curIdxs[name].Primary {
			continue
		}
		if didx, ok := desIdxs[name]; !ok || !indexEqual(curIdxs[name], didx) {
			changes = append(changes, Change{
				Kind: ChangeDropIndex, Table: current.Name, Object: name,
				Before: formatIndex(curIdxs[name]), Destructive: !ok,
				DDL: renderDropIndex(engine, current.Name, name),
			})
		}
	}

	// Columns
	curCols := indexCols(current.Columns)
	desCols := indexCols(desired.Columns)
	var addedCols, droppedCols []inspect.ColumnIR

	for _, dc := range desired.Columns {
		cc, exists := curCols[dc.Name]
		if !exists {
			addedCols = append(addedCols, dc)
			changes = append(changes, Change{
				Kind: ChangeAddColumn, Table: current.Name, Object: dc.Name,
				After: formatColumn(engine, dc),
				DDL:   fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", quoteIdent(engine, current.Name), formatColumn(engine, dc)),
			})
			continue
		}
		if !columnEqual(engine, cc, dc) {
			changes = append(changes, Change{
				Kind: ChangeModifyColumn, Table: current.Name, Object: dc.Name,
				Before: formatColumn(engine, cc), After: formatColumn(engine, dc),
				Destructive: isTypeNarrowing(cc.Type, dc.Type) || (cc.Nullable && !dc.Nullable),
				DDL:         renderModifyColumn(engine, current.Name, cc, dc),
			})
		}
	}

	for _, cc := range current.Columns {
		if _, exists := desCols[cc.Name]; !exists {
			droppedCols = append(droppedCols, cc)
			changes = append(changes, Change{
				Kind: ChangeDropColumn, Table: current.Name, Object: cc.Name,
				Before: formatColumn(engine, cc), Destructive: true,
				DDL: fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", quoteIdent(engine, current.Name), quoteIdent(engine, cc.Name)),
			})
		}
	}

	// Detect possible column renames (same normalized type dropped & added) so
	// operators are warned before running destructive DROP + ADD.
	for _, dc := range droppedCols {
		for _, ac := range addedCols {
			if normalizeColType(engine, dc.Type) == normalizeColType(engine, ac.Type) {
				renames = append(renames, fmt.Sprintf("%s: possible rename %s -> %s (%s)", current.Name, dc.Name, ac.Name, normalizeColType(engine, dc.Type)))
			}
		}
	}

	// Added or modified indexes
	for _, name := range sortedKeys(desIdxs) {
		if name == "PRIMARY" || desIdxs[name].Primary {
			continue
		}
		if cidx, ok := curIdxs[name]; !ok || !indexEqual(cidx, desIdxs[name]) {
			changes = append(changes, Change{
				Kind: ChangeAddIndex, Table: current.Name, Object: name,
				After: formatIndex(desIdxs[name]),
				DDL:   renderAddIndex(engine, current.Name, desIdxs[name]),
			})
		}
	}

	// Added or modified foreign keys
	for _, name := range sortedKeys(desFKs) {
		if cfk, ok := curFKs[name]; !ok || !fkEqual(cfk, desFKs[name]) {
			changes = append(changes, Change{
				Kind: ChangeAddForeignKey, Table: current.Name, Object: name,
				After: formatFK(desFKs[name]),
				DDL:   renderAddFK(engine, current.Name, desFKs[name]),
			})
		}
	}

	return changes, renames
}

// topoSortTables uses Kahn's algorithm so referenced parent tables come before child tables.
func topoSortTables(tables []inspect.TableIR) []inspect.TableIR {
	if len(tables) <= 1 {
		return tables
	}
	byName := map[string]inspect.TableIR{}
	inDegree := map[string]int{}
	dependents := map[string][]string{}
	for _, t := range tables {
		byName[t.Name] = t
		inDegree[t.Name] = 0
	}
	for _, t := range tables {
		seen := map[string]bool{}
		for _, fk := range t.ForeignKeys {
			if fk.ReferencedTable == t.Name || seen[fk.ReferencedTable] {
				continue
			}
			if _, inSet := byName[fk.ReferencedTable]; inSet {
				seen[fk.ReferencedTable] = true
				inDegree[t.Name]++
				dependents[fk.ReferencedTable] = append(dependents[fk.ReferencedTable], t.Name)
			}
		}
	}
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)
	var out []inspect.TableIR
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		out = append(out, byName[curr])
		next := dependents[curr]
		sort.Strings(next)
		for _, child := range next {
			inDegree[child]--
			if inDegree[child] == 0 {
				queue = append(queue, child)
			}
		}
		sort.Strings(queue)
	}
	// Cycle fallback: append any remaining tables deterministically.
	if len(out) < len(tables) {
		emitted := map[string]bool{}
		for _, t := range out {
			emitted[t.Name] = true
		}
		var rem []string
		for name := range byName {
			if !emitted[name] {
				rem = append(rem, name)
			}
		}
		sort.Strings(rem)
		for _, name := range rem {
			out = append(out, byName[name])
		}
	}
	return out
}

func normalizeColType(engine, typ string) string {
	t := strings.ToLower(strings.TrimSpace(typ))
	if engine == "mysql" {
		switch t {
		case "bool", "boolean":
			return "tinyint(1)"
		case "int(11)", "integer", "integer(11)":
			return "int"
		case "bigint(20)":
			return "bigint"
		case "smallint(6)":
			return "smallint"
		}
	}
	return t
}

func columnEqual(engine string, a, b inspect.ColumnIR) bool {
	return normalizeColType(engine, a.Type) == normalizeColType(engine, b.Type) &&
		a.Nullable == b.Nullable &&
		strings.TrimSpace(a.Default) == strings.TrimSpace(b.Default) &&
		strings.EqualFold(strings.TrimSpace(a.Extra), strings.TrimSpace(b.Extra))
}

func indexEqual(a, b inspect.IndexIR) bool {
	if a.Unique != b.Unique || a.Primary != b.Primary || len(a.Columns) != len(b.Columns) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	return true
}

func fkEqual(a, b inspect.ForeignKeyIR) bool {
	if a.ReferencedTable != b.ReferencedTable || len(a.Columns) != len(b.Columns) || len(a.ReferencedColumns) != len(b.ReferencedColumns) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	for i := range a.ReferencedColumns {
		if a.ReferencedColumns[i] != b.ReferencedColumns[i] {
			return false
		}
	}
	return strings.EqualFold(a.OnUpdate, b.OnUpdate) && strings.EqualFold(a.OnDelete, b.OnDelete)
}

func isTypeNarrowing(before, after string) bool {
	b, a := strings.ToLower(before), strings.ToLower(after)
	return (strings.HasPrefix(b, "bigint") && strings.HasPrefix(a, "int")) ||
		(strings.HasPrefix(b, "text") && strings.HasPrefix(a, "varchar"))
}

func quoteIdent(engine, name string) string {
	if engine == "mysql" {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func formatColumn(engine string, c inspect.ColumnIR) string {
	var parts []string
	parts = append(parts, quoteIdent(engine, c.Name), c.Type)
	if !c.Nullable {
		parts = append(parts, "NOT NULL")
	}
	if def := formatDefaultExpr(engine, c); def != "" {
		parts = append(parts, "DEFAULT "+def)
	}
	if c.Extra != "" && !strings.EqualFold(strings.TrimSpace(c.Extra), "DEFAULT_GENERATED") {
		parts = append(parts, c.Extra)
	}
	return strings.Join(parts, " ")
}

func formatDefaultExpr(engine string, c inspect.ColumnIR) string {
	def := strings.TrimSpace(c.Default)
	if def == "" {
		return ""
	}
	// PostgreSQL pg_get_expr already returns valid SQL expressions (quoted strings, casts, functions).
	if engine != "mysql" {
		return def
	}
	if strings.EqualFold(def, "NULL") || strings.EqualFold(def, "CURRENT_TIMESTAMP") ||
		strings.HasPrefix(strings.ToUpper(def), "CURRENT_TIMESTAMP(") ||
		strings.EqualFold(def, "TRUE") || strings.EqualFold(def, "FALSE") {
		return def
	}
	if (strings.HasPrefix(def, "'") && strings.HasSuffix(def, "'")) ||
		(strings.HasPrefix(def, "(") && strings.HasSuffix(def, ")")) ||
		strings.HasPrefix(strings.ToLower(def), "b'") {
		return def
	}
	if isMySQLNumericType(c.Type) && isSimpleNumber(def) {
		return def
	}
	return "'" + strings.ReplaceAll(def, "'", "''") + "'"
}

func isMySQLNumericType(colType string) bool {
	t := strings.ToLower(strings.TrimSpace(colType))
	for _, prefix := range []string{"tinyint", "smallint", "mediumint", "int", "integer", "bigint", "decimal", "numeric", "float", "double", "real", "bit"} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

func isSimpleNumber(s string) bool {
	if s == "" {
		return false
	}
	dot := false
	for i, r := range s {
		if i == 0 && (r == '-' || r == '+') {
			continue
		}
		if r == '.' && !dot {
			dot = true
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func formatIndex(idx inspect.IndexIR) string {
	prefix := "INDEX"
	if idx.Unique {
		prefix = "UNIQUE INDEX"
	}
	return fmt.Sprintf("%s %s (%s)", prefix, idx.Name, strings.Join(idx.Columns, ", "))
}

func formatFK(fk inspect.ForeignKeyIR) string {
	return fmt.Sprintf("FOREIGN KEY %s (%s) REFERENCES %s (%s)", fk.Name, strings.Join(fk.Columns, ", "), fk.ReferencedTable, strings.Join(fk.ReferencedColumns, ", "))
}

func formatFKActions(fk inspect.ForeignKeyIR) string {
	var parts []string
	if upd := strings.TrimSpace(fk.OnUpdate); upd != "" {
		parts = append(parts, "ON UPDATE "+upd)
	}
	if del := strings.TrimSpace(fk.OnDelete); del != "" {
		parts = append(parts, "ON DELETE "+del)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func renderCreateTableStatements(engine string, t inspect.TableIR) []string {
	var defs []string
	var postStmts []string
	for _, c := range t.Columns {
		defs = append(defs, "  "+formatColumn(engine, c))
	}
	for _, idx := range t.Indexes {
		cols := quoteJoin(engine, idx.Columns)
		if idx.Primary {
			defs = append(defs, fmt.Sprintf("  PRIMARY KEY (%s)", cols))
		} else if idx.Unique {
			if engine == "mysql" {
				defs = append(defs, fmt.Sprintf("  UNIQUE KEY %s (%s)", quoteIdent(engine, idx.Name), cols))
			} else {
				defs = append(defs, fmt.Sprintf("  CONSTRAINT %s UNIQUE (%s)", quoteIdent(engine, idx.Name), cols))
			}
		} else {
			if engine == "mysql" {
				defs = append(defs, fmt.Sprintf("  KEY %s (%s)", quoteIdent(engine, idx.Name), cols))
			} else {
				postStmts = append(postStmts, fmt.Sprintf("CREATE INDEX %s ON %s (%s);", quoteIdent(engine, idx.Name), quoteIdent(engine, t.Name), cols))
			}
		}
	}
	for _, fk := range t.ForeignKeys {
		defs = append(defs, fmt.Sprintf("  CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)%s",
			quoteIdent(engine, fk.Name), quoteJoin(engine, fk.Columns), quoteIdent(engine, fk.ReferencedTable), quoteJoin(engine, fk.ReferencedColumns), formatFKActions(fk)))
	}
	suffix := ""
	if engine == "mysql" {
		suffix = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	}
	createStmt := fmt.Sprintf("CREATE TABLE %s (\n%s\n)%s;", quoteIdent(engine, t.Name), strings.Join(defs, ",\n"), suffix)
	return append([]string{createStmt}, postStmts...)
}

func renderModifyColumn(engine, table string, before, after inspect.ColumnIR) string {
	if engine == "postgres" {
		var stmts []string
		t := quoteIdent(engine, table)
		c := quoteIdent(engine, after.Name)
		if normalizeColType(engine, before.Type) != normalizeColType(engine, after.Type) {
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;", t, c, after.Type))
		}
		if strings.TrimSpace(before.Default) != strings.TrimSpace(after.Default) {
			if strings.TrimSpace(after.Default) == "" {
				stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT;", t, c))
			} else {
				stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;", t, c, formatDefaultExpr(engine, after)))
			}
		}
		if before.Nullable != after.Nullable {
			if !after.Nullable {
				stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;", t, c))
			} else {
				stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;", t, c))
			}
		}
		if len(stmts) == 0 {
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;", t, c, after.Type))
		}
		return strings.Join(stmts, "\n")
	}
	return fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s;", quoteIdent(engine, table), formatColumn(engine, after))
}

func renderAddIndex(engine, table string, idx inspect.IndexIR) string {
	unique := ""
	if idx.Unique {
		unique = "UNIQUE "
	}
	if engine == "postgres" {
		return fmt.Sprintf("CREATE %sINDEX CONCURRENTLY %s ON %s (%s);", unique, quoteIdent(engine, idx.Name), quoteIdent(engine, table), quoteJoin(engine, idx.Columns))
	}
	return fmt.Sprintf("CREATE %sINDEX %s ON %s (%s);", unique, quoteIdent(engine, idx.Name), quoteIdent(engine, table), quoteJoin(engine, idx.Columns))
}

func renderDropIndex(engine, table, name string) string {
	if engine == "postgres" {
		return fmt.Sprintf("DROP INDEX CONCURRENTLY %s;", quoteIdent(engine, name))
	}
	return fmt.Sprintf("DROP INDEX %s ON %s;", quoteIdent(engine, name), quoteIdent(engine, table))
}

func renderAddFK(engine, table string, fk inspect.ForeignKeyIR) string {
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)%s;",
		quoteIdent(engine, table), quoteIdent(engine, fk.Name), quoteJoin(engine, fk.Columns), quoteIdent(engine, fk.ReferencedTable), quoteJoin(engine, fk.ReferencedColumns), formatFKActions(fk))
}

func renderDropFK(engine, table, name string) string {
	if engine == "mysql" {
		return fmt.Sprintf("ALTER TABLE %s DROP FOREIGN KEY %s;", quoteIdent(engine, table), quoteIdent(engine, name))
	}
	return fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", quoteIdent(engine, table), quoteIdent(engine, name))
}

func quoteJoin(engine string, cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = quoteIdent(engine, c)
	}
	return strings.Join(out, ", ")
}

func indexCols(cols []inspect.ColumnIR) map[string]inspect.ColumnIR {
	m := make(map[string]inspect.ColumnIR, len(cols))
	for _, c := range cols {
		m[c.Name] = c
	}
	return m
}

func indexIndexes(idxs []inspect.IndexIR) map[string]inspect.IndexIR {
	m := make(map[string]inspect.IndexIR, len(idxs))
	for _, idx := range idxs {
		m[idx.Name] = idx
	}
	return m
}

func indexFKs(fks []inspect.ForeignKeyIR) map[string]inspect.ForeignKeyIR {
	m := make(map[string]inspect.ForeignKeyIR, len(fks))
	for _, fk := range fks {
		m[fk.Name] = fk
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
