package server

import (
	"regexp"
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
		replacement: "Use ON DUPLICATE KEY UPDATE with the table's explicit unique key and row alias",
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
