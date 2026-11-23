package server

import (
	"regexp"
	"strings"
)

// gatewayMySQLRuntimeIssueCode identifies SQLite SQL that cannot be sent to a
// MySQL 8.4 Gateway connection unchanged. The runtime still uses SQLite today
// so these rules are an audit contract for the future MySQL store path.
type gatewayMySQLRuntimeIssueCode string

const (
	gatewayMySQLIssueNumberedParameter   gatewayMySQLRuntimeIssueCode = "numbered_parameter"
	gatewayMySQLIssueOnConflict          gatewayMySQLRuntimeIssueCode = "on_conflict"
	gatewayMySQLIssueInsertOrIgnore      gatewayMySQLRuntimeIssueCode = "insert_or_ignore"
	gatewayMySQLIssueUpdateOrIgnore      gatewayMySQLRuntimeIssueCode = "update_or_ignore"
	gatewayMySQLIssueJSONEach            gatewayMySQLRuntimeIssueCode = "json_each"
	gatewayMySQLIssueJSONAggregate       gatewayMySQLRuntimeIssueCode = "json_aggregate"
	gatewayMySQLIssueAggregateFilter     gatewayMySQLRuntimeIssueCode = "aggregate_filter"
	gatewayMySQLIssueJulianDay           gatewayMySQLRuntimeIssueCode = "julianday"
	gatewayMySQLIssueUnixEpoch           gatewayMySQLRuntimeIssueCode = "unixepoch"
	gatewayMySQLIssueMaterializedCTE     gatewayMySQLRuntimeIssueCode = "materialized_cte"
	gatewayMySQLIssueSQLiteConcat        gatewayMySQLRuntimeIssueCode = "sqlite_concat"
	gatewayMySQLIssueNullSafeParameter   gatewayMySQLRuntimeIssueCode = "null_safe_parameter"
	gatewayMySQLIssueSQLiteIntrospection gatewayMySQLRuntimeIssueCode = "sqlite_introspection"
	gatewayMySQLIssueSQLiteExplainPlan   gatewayMySQLRuntimeIssueCode = "sqlite_explain_query_plan"
	gatewayMySQLIssueCreateIndexIfAbsent gatewayMySQLRuntimeIssueCode = "create_index_if_not_exists"
	gatewayMySQLIssueDropIndexIfAbsent   gatewayMySQLRuntimeIssueCode = "drop_index_if_exists"
	gatewayMySQLIssueAutoIncrement       gatewayMySQLRuntimeIssueCode = "sqlite_autoincrement"
	gatewayMySQLIssuePartialIndex        gatewayMySQLRuntimeIssueCode = "partial_index"
	gatewayMySQLIssueReservedUse         gatewayMySQLRuntimeIssueCode = "reserved_use"
)

type gatewayMySQLRuntimeIssueRule struct {
	code        gatewayMySQLRuntimeIssueCode
	description string
	replacement string
	pattern     *regexp.Regexp
}

// gatewayMySQLRuntimeIssueRules is deliberately explicit. A generic SQL
// rewrite would change conflict handling and JSON semantics without knowing
// the table's keys or the shape of its stored document.
var gatewayMySQLRuntimeIssueRules = []gatewayMySQLRuntimeIssueRule{
	{
		code:        gatewayMySQLIssueNumberedParameter,
		description: "SQLite numbered parameters such as ?1 are not accepted by the MySQL driver",
		replacement: "Use unnumbered ? placeholders in argument order",
		pattern:     regexp.MustCompile(`\?\d+`),
	},
	{
		code:        gatewayMySQLIssueOnConflict,
		description: "SQLite ON CONFLICT syntax is not MySQL syntax",
		replacement: "Express the upsert as a gatewayUpsert and render it with gatewayUpsertSQL: the dialect layer keeps ON CONFLICT for SQLite and emits ON DUPLICATE KEY UPDATE for MySQL",
		pattern:     regexp.MustCompile(`(?i)\bON\s+CONFLICT\b`),
	},
	{
		code:        gatewayMySQLIssueInsertOrIgnore,
		description: "SQLite INSERT OR IGNORE syntax is not MySQL syntax",
		replacement: "Use an explicit ON DUPLICATE KEY UPDATE no-op for the intended key",
		pattern:     regexp.MustCompile(`(?i)\bINSERT\s+OR\s+IGNORE\b`),
	},
	{
		code:        gatewayMySQLIssueUpdateOrIgnore,
		description: "SQLite UPDATE OR IGNORE syntax is not MySQL syntax",
		replacement: "Use UPDATE IGNORE only when its broader MySQL error handling is intended",
		pattern:     regexp.MustCompile(`(?i)\bUPDATE\s+OR\s+IGNORE\b`),
	},
	{
		code:        gatewayMySQLIssueJSONEach,
		description: "SQLite json_each table-valued function has no MySQL equivalent with the same syntax",
		replacement: "Use JSON_TABLE with explicit columns and an alias",
		pattern:     regexp.MustCompile(`(?i)\bjson_each\s*\(`),
	},
	{
		code:        gatewayMySQLIssueJSONAggregate,
		description: "SQLite json_group_array aggregate syntax has no MySQL 8.4 spelling",
		replacement: "Order source rows in a subquery and use JSON_ARRAYAGG",
		pattern:     regexp.MustCompile(`(?is)\bjson_group_array\s*\(.*\)`),
	},
	{
		code:        gatewayMySQLIssueAggregateFilter,
		description: "SQLite aggregate FILTER syntax is not accepted by MySQL",
		replacement: "Use SUM(CASE WHEN predicate THEN 1 ELSE 0 END)",
		pattern:     regexp.MustCompile(`(?i)\bFILTER\s*\(`),
	},
	{
		code:        gatewayMySQLIssueJulianDay,
		description: "SQLite julianday is not a MySQL function",
		replacement: "Parse RFC3339 text with STR_TO_DATE, or use FROM_UNIXTIME for the unixepoch modifier",
		pattern:     regexp.MustCompile(`(?i)\bjulianday\s*\(`),
	},
	{
		code:        gatewayMySQLIssueUnixEpoch,
		description: "SQLite unixepoch is not a MySQL function",
		replacement: "Use UNIX_TIMESTAMP for a typed timestamp or compare the stored epoch integer directly",
		pattern:     regexp.MustCompile(`(?i)\bunixepoch\s*\(`),
	},
	{
		code:        gatewayMySQLIssueMaterializedCTE,
		description: "SQLite MATERIALIZED CTE syntax is not accepted by MySQL 8.4",
		replacement: "Remove the MATERIALIZED keyword and verify the resulting query plan",
		pattern:     regexp.MustCompile(`(?i)\bMATERIALIZED\b`),
	},
	{
		code:        gatewayMySQLIssueSQLiteConcat,
		description: "SQLite || string concatenation is logical OR under the default MySQL mode",
		replacement: "Use CONCAT with the same operands",
		pattern:     regexp.MustCompile(`\|\|`),
	},
	{
		code:        gatewayMySQLIssueNullSafeParameter,
		description: "SQLite IS ? null comparison is not valid MySQL syntax",
		replacement: "Use the MySQL null-safe equality operator <=> ?",
		pattern:     regexp.MustCompile(`(?i)\bIS\s+\?`),
	},
	{
		code:        gatewayMySQLIssueSQLiteIntrospection,
		description: "SQLite PRAGMA and sqlite_schema introspection do not exist in MySQL",
		replacement: "Use information_schema queries with explicit schema and table names",
		pattern:     regexp.MustCompile(`(?i)\bPRAGMA\b|\bsqlite_schema\b|\bpragma_[a-z_]+\b`),
	},
	{
		code:        gatewayMySQLIssueSQLiteExplainPlan,
		description: "SQLite EXPLAIN QUERY PLAN syntax is not accepted by MySQL 8.4",
		replacement: "Use EXPLAIN or EXPLAIN FORMAT=JSON with the MySQL statement",
		pattern:     regexp.MustCompile(`(?i)\bEXPLAIN\s+QUERY\s+PLAN\b`),
	},
	{
		code:        gatewayMySQLIssueCreateIndexIfAbsent,
		description: "MySQL CREATE INDEX does not accept SQLite's IF NOT EXISTS clause",
		replacement: "Check information_schema.STATISTICS before CREATE INDEX, or make the migration versioned",
		pattern:     regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+IF\s+NOT\s+EXISTS\b`),
	},
	{
		code:        gatewayMySQLIssueDropIndexIfAbsent,
		description: "MySQL DROP INDEX does not accept SQLite's IF EXISTS clause",
		replacement: "Check information_schema.STATISTICS before DROP INDEX",
		pattern:     regexp.MustCompile(`(?i)\bDROP\s+INDEX\s+IF\s+EXISTS\b`),
	},
	{
		code:        gatewayMySQLIssueAutoIncrement,
		description: "SQLite AUTOINCREMENT is not a MySQL column definition",
		replacement: "Use an integer AUTO_INCREMENT primary key",
		pattern:     regexp.MustCompile(`(?i)\bAUTOINCREMENT\b`),
	},
	{
		code:        gatewayMySQLIssuePartialIndex,
		description: "SQLite partial-index WHERE clauses are not supported by MySQL indexes",
		replacement: "Use a nullable generated key column or an explicit uniqueness constraint that preserves the predicate",
		pattern:     regexp.MustCompile(`(?is)\bCREATE\s+(?:UNIQUE\s+)?INDEX\b.*\bWHERE\b`),
	},
	{
		code:        gatewayMySQLIssueReservedUse,
		description: "MySQL 8.4 reserves USE and requires the Gateway column to be quoted",
		replacement: "Quote the column as `use` in every MySQL statement",
		pattern:     regexp.MustCompile("(?i)(?:^|[\\s,(])use(?:[\\s,)=])"),
	},
}

func gatewayMySQLRuntimeIssues(query string) []gatewayMySQLRuntimeIssueRule {
	issues := make([]gatewayMySQLRuntimeIssueRule, 0, len(gatewayMySQLRuntimeIssueRules))
	for _, rule := range gatewayMySQLRuntimeIssueRules {
		if rule.pattern.MatchString(query) {
			issues = append(issues, rule)
		}
	}
	return issues
}

// gatewaySQLDialect names the SQL flavor a Gateway store connection speaks.
type gatewaySQLDialect string

const (
	gatewayDialectSQLite gatewaySQLDialect = "sqlite"
	gatewayDialectMySQL  gatewaySQLDialect = "mysql"
)

// gatewayDialectStore is the dialect every live store statement renders for.
// The Gateway still opens SQLite only, so this stays SQLite until the MySQL
// store path is wired; that wiring sets this one value.
var gatewayDialectStore = gatewayDialectSQLite

// gatewayMySQLUpsertNewRowPrefix marks the MySQL spelling of SQLite's excluded
// pseudo table. VALUES(col) reads the values the INSERT proposed, and unlike the
// 8.0.19+ row alias it leaves unqualified column names meaning the stored row:
// with an alias every bare column inside the ON DUPLICATE KEY UPDATE clause is
// ambiguous (ERROR 1052), and the DO UPDATE filter is written by callers as
// SQLite SQL full of bare columns. VALUES() is deprecated in MySQL 8.4 but still
// supported, and this constant is the one place to change when it is removed.
const gatewayMySQLUpsertNewRowPrefix = "VALUES("

// gatewayUpsert is one runtime upsert written once and rendered per dialect.
//
// insert is the INSERT statement up to and including its VALUES clause. target
// lists the conflict key columns of the SQLite ON CONFLICT clause; MySQL has no
// conflict target, so every site that sets target must be able to show that the
// table has no other unique constraint that a caller could hit first, otherwise
// MySQL would update a row that SQLite would reject. doNothing renders DO
// NOTHING on SQLite and a no-op self assignment on MySQL. set holds the
// "column=expression" assignments of DO UPDATE SET, written in SQLite terms
// (excluded.col). where holds a SQLite-only DO UPDATE filter.
//
// A spec never contains the SQLite ON CONFLICT keyword: sqliteSQL adds it, so
// the keyword lives in exactly one place and the runtime SQL audit stays at
// zero findings.
type gatewayUpsert struct {
	insert    string
	target    []string
	doNothing bool
	set       []string
	where     string
}

// gatewayUpsertSQL renders an upsert for the dialect the live store speaks.
func gatewayUpsertSQL(upsert gatewayUpsert) string {
	return upsert.render(gatewayDialectStore)
}

func (upsert gatewayUpsert) render(dialect gatewaySQLDialect) string {
	if dialect == gatewayDialectMySQL {
		return upsert.mysqlSQL()
	}
	return upsert.sqliteSQL()
}

func (upsert gatewayUpsert) sqliteSQL() string {
	query := strings.TrimSpace(upsert.insert) + " ON CONFLICT"
	if len(upsert.target) > 0 {
		query += "(" + strings.Join(upsert.target, ",") + ")"
	}
	if upsert.doNothing {
		return query + " DO NOTHING"
	}
	query += " DO UPDATE SET " + strings.Join(upsert.set, ", ")
	if upsert.where != "" {
		query += " WHERE " + upsert.where
	}
	return query
}

func (upsert gatewayUpsert) mysqlSQL() string {
	insert := gatewayMySQLUpsertInsert(upsert.insert)
	if upsert.doNothing {
		column := gatewayMySQLUpsertNoOpColumn(upsert)
		return insert + " ON DUPLICATE KEY UPDATE " + column + " = " + column
	}
	return insert + " ON DUPLICATE KEY UPDATE " + strings.Join(gatewayMySQLUpsertAssignments(upsert), ", ")
}

// gatewayMySQLUpsertInsert quotes the insert column list, because MySQL parses
// the Gateway's use column as the reserved USE keyword.
func gatewayMySQLUpsertInsert(insert string) string {
	insert = strings.TrimSpace(insert)
	match := gatewayMySQLUpsertInsertHead.FindStringSubmatch(insert)
	if match == nil {
		return insert
	}
	columns := strings.Split(match[3], ",")
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, gatewayMySQLIdentifier(strings.TrimSpace(column)))
	}
	return match[1] + gatewayMySQLIdentifier(match[2]) + " (" + strings.Join(quoted, ", ") + ")" + insert[len(match[0]):]
}

var gatewayMySQLUpsertInsertHead = regexp.MustCompile("(?is)^\\s*(INSERT\\s+INTO\\s+)([A-Za-z_][A-Za-z0-9_]*)\\s*\\(([^)]*)\\)")

var gatewayMySQLUpsertExcluded = regexp.MustCompile("(?i)\\bexcluded\\s*\\.\\s*([A-Za-z_][A-Za-z0-9_]*)")

type gatewayMySQLAssignment struct {
	name   string
	column string
	value  string
}

// gatewayMySQLUpsertAssignments renders DO UPDATE SET. MySQL has no per-row
// WHERE, so every assignment becomes IF(condition, new, old): a false predicate
// leaves every stored column untouched, which is what SQLite does when it
// declines the update. MySQL evaluates the assignments left to right against
// values the statement has already changed, so the predicate columns are
// assigned last; otherwise a freshly written predicate column could turn the
// condition false and freeze a column that SQLite would have updated. A
// predicate column that is still unassigned keeps the condition true whenever
// the row really differs, and once every predicate column already holds its new
// value the update is a no-op anyway.
func gatewayMySQLUpsertAssignments(upsert gatewayUpsert) []string {
	assignments := make([]gatewayMySQLAssignment, 0, len(upsert.set))
	for _, item := range upsert.set {
		name, expression, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		assignments = append(assignments, gatewayMySQLAssignment{
			name:   name,
			column: gatewayMySQLIdentifier(name),
			value:  gatewayMySQLUpsertExpression(strings.TrimSpace(expression)),
		})
	}
	if upsert.where == "" {
		rendered := make([]string, 0, len(assignments))
		for _, assignment := range assignments {
			rendered = append(rendered, assignment.column+" = "+assignment.value)
		}
		return rendered
	}
	condition := gatewayMySQLUpsertExpression(upsert.where)
	ordered := make([]gatewayMySQLAssignment, 0, len(assignments))
	var conditional []gatewayMySQLAssignment
	for _, assignment := range assignments {
		if gatewayMySQLUpsertMentions(upsert.where, assignment.name) {
			conditional = append(conditional, assignment)
			continue
		}
		ordered = append(ordered, assignment)
	}
	ordered = append(ordered, conditional...)
	rendered := make([]string, 0, len(ordered))
	for _, assignment := range ordered {
		rendered = append(rendered, assignment.column+" = IF("+condition+", "+assignment.value+", "+assignment.column+")")
	}
	return rendered
}

func gatewayMySQLUpsertMentions(predicate, column string) bool {
	pattern := regexp.MustCompile("(?i)(?:^|[^A-Za-z0-9_.])" + regexp.QuoteMeta(column) + "(?:[^A-Za-z0-9_]|$)")
	return pattern.MatchString(predicate)
}

func gatewayMySQLUpsertNoOpColumn(upsert gatewayUpsert) string {
	match := gatewayMySQLUpsertInsertHead.FindStringSubmatch(strings.TrimSpace(upsert.insert))
	if match != nil {
		columns := strings.Split(match[3], ",")
		return gatewayMySQLIdentifier(strings.TrimSpace(columns[0]))
	}
	if len(upsert.target) > 0 {
		return gatewayMySQLIdentifier(upsert.target[0])
	}
	return "id"
}

func gatewayMySQLUpsertExpression(expression string) string {
	return gatewayMySQLUpsertExcluded.ReplaceAllString(gatewayMySQLUpsertScalarFunction(expression), "VALUES(\x60$1\x60)")
}

// gatewayMySQLUpsertScalarFunction rewrites SQLite's two-argument scalar
// max()/min() to MySQL's GREATEST/LEAST. Both return NULL when any argument is
// NULL, so the rewrite keeps the semantics the upsert was written for. The
// one-argument aggregate spellings are left alone.
func gatewayMySQLUpsertScalarFunction(expression string) string {
	var out strings.Builder
	lowered := strings.ToLower(expression)
	for index := 0; index < len(expression); {
		name := ""
		for _, candidate := range []string{"max", "min"} {
			if strings.HasPrefix(lowered[index:], candidate) && gatewayMySQLUpsertFunctionStart(lowered, index+len(candidate)) {
				name = candidate
				break
			}
		}
		if name == "" {
			out.WriteByte(expression[index])
			index++
			continue
		}
		open := index + len(name)
		for open < len(expression) && (expression[open] == ' ' || expression[open] == '\t') {
			open++
		}
		close, ok := gatewayMySQLUpsertClosingParen(expression, open)
		if !ok || gatewayMySQLUpsertArgumentCount(expression[open+1:close]) < 2 {
			out.WriteByte(expression[index])
			index++
			continue
		}
		replacement := "GREATEST"
		if name == "min" {
			replacement = "LEAST"
		}
		out.WriteString(replacement)
		out.WriteString(expression[index+len(name) : open])
		out.WriteString("(")
		out.WriteString(gatewayMySQLUpsertScalarFunction(expression[open+1 : close]))
		out.WriteString(")")
		index = close + 1
	}
	return out.String()
}

func gatewayMySQLUpsertFunctionStart(lowered string, index int) bool {
	for index < len(lowered) {
		if lowered[index] != ' ' && lowered[index] != '\t' {
			return lowered[index] == '('
		}
		index++
	}
	return false
}

func gatewayMySQLUpsertClosingParen(expression string, open int) (int, bool) {
	if open >= len(expression) || expression[open] != '(' {
		return 0, false
	}
	depth := 0
	var quote byte
	for index := open; index < len(expression); index++ {
		character := expression[index]
		switch {
		case quote != 0:
			if character == quote {
				quote = 0
			}
		case character == '\'' || character == '"':
			quote = character
		case character == '(':
			depth++
		case character == ')':
			depth--
			if depth == 0 {
				return index, true
			}
		}
	}
	return 0, false
}

func gatewayMySQLUpsertArgumentCount(arguments string) int {
	depth := 0
	var quote byte
	count := 1
	for index := 0; index < len(arguments); index++ {
		character := arguments[index]
		switch {
		case quote != 0:
			if character == quote {
				quote = 0
			}
		case character == '\'' || character == '"':
			quote = character
		case character == '(':
			depth++
		case character == ')':
			depth--
		case character == ',' && depth == 0:
			count++
		}
	}
	return count
}

// gatewayMySQLIdentifier quotes one (possibly qualified) identifier for MySQL.
func gatewayMySQLIdentifier(name string) string {
	parts := strings.Split(name, ".")
	for index, part := range parts {
		parts[index] = "`" + strings.ReplaceAll(strings.TrimSpace(part), "`", "``") + "`"
	}
	return strings.Join(parts, ".")
}
