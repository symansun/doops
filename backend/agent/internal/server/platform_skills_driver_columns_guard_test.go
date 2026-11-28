package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The two column definitions the frozen SQLite DDL must keep verbatim. Pinning
// the whole line means an edit that drops a column or changes a default fails
// here instead of silently changing what an upgraded database gets.
const (
	platformSkillsDriverModeDDL = "driver_mode TEXT NOT NULL DEFAULT 'agent_prompt'"
	platformSkillsStrategyIDDDL = "strategy_id TEXT NOT NULL DEFAULT ''"
)

// platformSkillsRetiredColumns are the names SF-02 retired when it removed the
// builtin_strategy double track.
var platformSkillsRetiredColumns = []string{"driver_mode", "strategy_id"}

// TestPlatformSkillsDriverColumnsHaveNoRuntimeSQL pins the two platform_skills
// columns SF-02 left behind: the columns survive in the DDL (removing a column
// needs a migration and trips the MySQL column-parity guard), but no runtime SQL
// may read or write them.
//
// The guard parses every non-test Go file in this package and inspects the SQL
// string literals themselves. Go comments never reach the AST as string
// literals, so the retirement notes in skills_registry.go and
// monitor_engine_runner.go stay legal by construction. A re-added read is
// reported with file:line and the statement head.
//
// The MySQL schema and the frozen SQLite export are checked as text: both must
// still declare the columns, so "the columns are only in the DDL" cannot quietly
// become false. Dropping them means writing a migration and updating this guard
// together with GATEWAY_MYSQL_MIGRATION_PLAN.md.
func TestPlatformSkillsDriverColumnsHaveNoRuntimeSQL(t *testing.T) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the package directory")
	}
	dir := filepath.Dir(self)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot list %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	scanned := 0
	ddlSites := map[string]int{}
	ddlLines := map[string]int{}
	var failures []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("cannot parse %s: %v", name, err)
		}
		scanned++
		ast.Inspect(file, func(node ast.Node) bool {
			literal, isLiteral := node.(*ast.BasicLit)
			if !isLiteral || literal.Kind != token.STRING {
				return true
			}
			value := literal.Value
			named := platformSkillsColumnsIn(value)
			if len(named) == 0 {
				return true
			}
			isFrozenDDL := name == "gateway_store_schema.go" &&
				strings.Contains(value, "CREATE TABLE IF NOT EXISTS platform_skills") &&
				strings.Contains(value, platformSkillsDriverModeDDL) &&
				strings.Contains(value, platformSkillsStrategyIDDDL)
			for _, column := range named {
				// Report the line the column name sits on, not the line the
				// literal opens on: multi-line SQL puts them on different lines.
				offset := strings.Index(value, column)
				line := fset.Position(literal.Pos()).Line + strings.Count(value[:offset], "\n")
				location := name + ":" + strconv.Itoa(line)
				if !isFrozenDDL {
					failures = append(failures, location+" runtime SQL still names "+column+": "+statementHead(strings.Trim(value, "\"\u0060")))
					continue
				}
				ddlSites[column]++
			}
			if isFrozenDDL {
				for _, definition := range []string{platformSkillsDriverModeDDL, platformSkillsStrategyIDDDL} {
					if strings.Contains(value, definition) {
						ddlLines[definition]++
					}
				}
			}
			return true
		})
	}
	for _, path := range []string{"gateway_mysql_schema.sql", filepath.Join("testdata", "gateway-sqlite-schema.sql")} {
		body, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatalf("cannot read %s: %v", path, err)
		}
		text := string(body)
		for _, column := range platformSkillsRetiredColumns {
			if !sqlDeclaresColumn(text, "platform_skills", column) {
				failures = append(failures, path+" no longer declares platform_skills."+column+"; dropping a retired column needs a migration and an update to this guard and GATEWAY_MYSQL_MIGRATION_PLAN.md")
			}
		}
	}
	sort.Strings(failures)
	if len(failures) != 0 {
		t.Fatalf("platform_skills.driver_mode / strategy_id are retired from runtime SQL (SF-02); only the frozen DDL and the retirement comments may keep them:\n  %s", strings.Join(failures, "\n  "))
	}
	// Floor: the package has well over 100 non-test Go files today. A walk that
	// silently stops scanning (moved directory, changed suffix) must fail loudly
	// instead of passing with zero matches.
	if scanned <= 100 {
		t.Fatalf("only %d non-test Go files were scanned in %s; the guard is not covering the package", scanned, dir)
	}
	for _, column := range platformSkillsRetiredColumns {
		if ddlSites[column] != 1 {
			t.Fatalf("expected exactly one frozen platform_skills DDL statement naming %s, saw %d", column, ddlSites[column])
		}
	}
	for _, definition := range []string{platformSkillsDriverModeDDL, platformSkillsStrategyIDDDL} {
		if ddlLines[definition] != 1 {
			t.Fatalf("the frozen platform_skills DDL no longer carries %q verbatim, saw %d", definition, ddlLines[definition])
		}
	}
}

// platformSkillsColumnsIn reports which retired columns the literal names.
func platformSkillsColumnsIn(value string) []string {
	var named []string
	for _, column := range platformSkillsRetiredColumns {
		if strings.Contains(value, column) {
			named = append(named, column)
		}
	}
	return named
}

// sqlDeclaresColumn reports whether the CREATE TABLE body for table declares the
// named column, so the check survives formatting differences between the Go DDL
// and the MySQL and frozen SQL files.
func sqlDeclaresColumn(schema, table, column string) bool {
	start := strings.Index(schema, "CREATE TABLE "+table+" (")
	if start < 0 {
		return false
	}
	body := schema[start:]
	if end := strings.Index(body, "\n);"); end >= 0 {
		body = body[:end]
	}
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 0 && fields[0] == column {
			return true
		}
	}
	return false
}
