package server

import (
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
	// INT-35 adds the two constructs the first sixteen rules missed. Both break at
	// MySQL parse time, not at the semantics layer, and both were found by
	// scanning backtick SQL literals for SQLite-only tokens.
	gatewayMySQLIssueSQLiteRowid     gatewayMySQLRuntimeIssueCode = "sqlite_rowid"
	gatewayMySQLIssueLimitInSubquery gatewayMySQLRuntimeIssueCode = "limit_in_subquery"
	// INT-40 adds the scalar MIN/MAX rule: SQLite's two-argument MIN(a, b) and
	// MAX(a, b) are scalar functions, while MySQL's MIN/MAX are aggregate-only,
	// so a second argument is Error 1064 (42000) at parse time.
	gatewayMySQLIssueScalarMinMax gatewayMySQLRuntimeIssueCode = "scalar_minmax"
	// INT-44 adds the bare-reserved-identifier rule: MySQL 8.4 reserves KEY, so
	// the Gateway column "key" is Error 1064 (42000) "near 'key'" wherever it is
	// spelled unquoted. USE already had its own rule; this one covers the column
	// that the target resource sync writes and reads on every tick.
	gatewayMySQLIssueReservedBareIdentifier gatewayMySQLRuntimeIssueCode = "reserved_bare_identifier"
)

type gatewayMySQLRuntimeIssueRule struct {
	code        gatewayMySQLRuntimeIssueCode
	description string
	replacement string
	pattern     *regexp.Regexp
	// match carries the rules a single regular expression cannot express. A
	// LIMIT in an IN subquery is only a defect when it belongs to the subquery
	// itself, so that rule has to scan brackets instead of matching text.
	match func(query string) bool
}

// matches reports whether rule applies to query. Pattern rules keep their
// regular expression; the bracket-aware rules use match.
func (rule gatewayMySQLRuntimeIssueRule) matches(query string) bool {
	if rule.match != nil {
		return rule.match(query)
	}
	return rule.pattern.MatchString(query)
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
		replacement: "Express the statement as a gatewayUpsert with doNothing and the exact conflict key the SQLite statement relied on, then render it with gatewayUpsertSQL",
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
		replacement: "Convert stored RFC3339 text with gatewayEpochSecondsFor and compare integer epoch seconds on both dialects",
		pattern:     regexp.MustCompile(`(?i)\bjulianday\s*\(`),
	},
	{
		code:        gatewayMySQLIssueUnixEpoch,
		description: "SQLite unixepoch is not a MySQL function",
		replacement: "Render the database clock with gatewayEpochNowSQL: unixepoch() on SQLite, UNIX_TIMESTAMP() on MySQL, both integer UTC epoch seconds",
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
		replacement: "Use gatewayConcatSQL: SQLite || and MySQL CONCAT() both return NULL when any operand is NULL, unlike CONCAT_WS which drops NULLs",
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
		pattern:     regexp.MustCompile(`(?i)\bPRAGMA\b|\bsqlite_schema\b|\bsqlite_master\b|\bpragma_[a-z_]+\b`),
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
	// INT-35: rowid is the implicit SQLite row identifier. MySQL has no such
	// column, so a live statement fails with Error 1054 (42000) "Unknown column
	// 'rowid' in 'order clause'". The matcher strips SQL comments first, because
	// a query that merely explains why it avoids rowid must not be audited as
	// one that uses it, and it ignores the WITHOUT ROWID table option, which is
	// SQLite-only DDL outside this runtime guard. Go comments and the copier in
	// gateway_mysql_copy.go are outside the literal scan itself: the copier
	// reads SQLite rows through its own connection and never sends this SQL
	// through a MySQL Gateway.
	{
		code:        gatewayMySQLIssueSQLiteRowid,
		description: "SQLite rowid is an implicit column MySQL does not have, so the statement fails with Error 1054",
		replacement: "Order by an explicit column plus a deterministic tie-break, for example ORDER BY frozen_at DESC, id DESC, instead of insertion order",
		match:       gatewayMySQLSQLiteRowid,
	},
	// INT-35: MySQL 8.4 rejects a LIMIT that belongs to an IN/ALL/ANY/SOME
	// subquery with Error 1235 (42000) "This version of MySQL doesn't yet
	// support 'LIMIT & IN/ALL/ANY/SOME subquery'", while SQLite accepts it. The
	// matcher only reports a LIMIT at the subquery's own level; a LIMIT inside a
	// nested derived table (IN (SELECT ... FROM (SELECT ... LIMIT ?) AS t)) is
	// legal MySQL and is left alone.
	{
		code:        gatewayMySQLIssueLimitInSubquery,
		description: "MySQL 8.4 rejects LIMIT inside an IN/ALL/ANY/SOME subquery with Error 1235",
		replacement: "Render the batch through the dialect layer: SQLite keeps IN (SELECT ... LIMIT ?) and MySQL names the target table again in a multi-table DELETE joined to an ordered derived table",
		match:       gatewayMySQLLimitInSubquery,
	},
	// INT-40: SQLite's MIN(a, b) / MAX(a, b) are scalar functions, so a statement
	// such as "misses=MIN(misses+1, ?)" is valid there and Error 1064 (42000) on
	// MySQL, whose MIN/MAX only exist as aggregates taking one argument. The
	// matcher is case-sensitive on purpose: SQL keywords in this package are
	// written uppercase, while Go's builtin max(a, b)/min(a, b) are lowercase and
	// must not be reported. Single-argument MIN(col)/MAX(col) is the aggregate
	// form and stays legal.
	{
		code:        gatewayMySQLIssueScalarMinMax,
		description: "SQLite scalar MIN(a, b)/MAX(a, b) is a MySQL aggregate-only name, so a second argument fails with Error 1064",
		replacement: "Render the pair through gatewayScalarLeastSQL/gatewayScalarGreatestSQL: SQLite keeps MIN/MAX and MySQL emits LEAST/GREATEST",
		match:       gatewayMySQLScalarMinMax,
	},
	// INT-44: MySQL 8.4 reserves KEY (as it does USE), so a statement that names
	// the agent_observations key column as a bare identifier is Error 1064 (42000)
	// "You have an error in your SQL syntax ... near 'key' at line 1" -- both in
	// ORDER BY (the read path) and in WHERE (inventory lookups). The rule reports
	// the word only in a position where MySQL parses an identifier, and only in a
	// statement whose first keyword can start one, so prose, JSON keys and
	// regular expressions never match.
	{
		code:        gatewayMySQLIssueReservedBareIdentifier,
		description: "MySQL 8.4 reserves KEY, so the Gateway column key unquoted is a parse error (Error 1064 near 'key')",
		replacement: "Render the column with gatewayKeyColumn/gatewayIdentifierFor: bare key on SQLite, backticked key on MySQL, in the INSERT column list, ORDER BY and WHERE",
		match:       gatewayMySQLReservedBareIdentifier,
	},
}

// gatewayMySQLBareReservedIdentifier is the word table behind the
// reserved_bare_identifier rule. KEY was the word that broke the target resource
// sync; a word joins this list only after a live MySQL 8.4 rejection has been
// reproduced, so the rule never guesses at the reserved-word list.
var gatewayMySQLBareReservedIdentifier = regexp.MustCompile(`(?i)\b(?:key)\b`)

// gatewayMySQLStatementStart limits the rule to a complete statement. A fragment
// such as "details=excluded.details" or a documentation sentence must not be
// reported, and the scan only claims literals that could reach a MySQL
// connection as a statement.
var gatewayMySQLStatementStart = regexp.MustCompile(`(?is)^\s*(?:SELECT|INSERT|UPDATE|DELETE|REPLACE|WITH)\b`)

// gatewayMySQLKeyKeywordPosition matches the reserved word when it belongs to a
// multi-word keyword rather than to an identifier: PRIMARY KEY, FOREIGN KEY,
// UNIQUE KEY, ON DUPLICATE KEY UPDATE and an index declaration.
var gatewayMySQLKeyKeywordPosition = regexp.MustCompile(`(?i)(?:PRIMARY|FOREIGN|UNIQUE|DUPLICATE|INDEX)\s+$`)

// gatewayMySQLBareIdentifierFollow matches the text MySQL accepts after an
// identifier in the positions the Gateway writes: an operator, a list
// separator, a closing parenthesis, the end of the statement, or the next SQL
// keyword.
var gatewayMySQLBareIdentifierFollow = regexp.MustCompile(`(?is)^\s*(?:=|<|>|,|\)|$|(?:FROM|WHERE|ORDER|GROUP|LIMIT|AS|IS|IN|NOT|AND|OR|DESC|ASC|USING)\b)`)

// gatewayMySQLReservedBareIdentifier reports a reserved word used as a bare
// identifier in a statement. Quoted spans are already blanked by
// gatewayMySQLWithoutComments, so a backticked or stringified key never matches,
// and the INSERT column list of the observations upsert is scanned too: the
// renderer quotes it wholesale, but the rule must stay armed against a new
// statement that forgets to.
func gatewayMySQLReservedBareIdentifier(query string) bool {
	if !gatewayMySQLStatementStart.MatchString(query) {
		return false
	}
	text := gatewayMySQLWithoutComments(query)
	for _, match := range gatewayMySQLBareReservedIdentifier.FindAllStringIndex(text, -1) {
		if gatewayMySQLKeyKeywordPosition.MatchString(text[:match[0]]) {
			continue
		}
		if gatewayMySQLBareIdentifierFollow.MatchString(text[match[1]:]) {
			return true
		}
	}
	return false
}

func gatewayMySQLRuntimeIssues(query string) []gatewayMySQLRuntimeIssueRule {
	issues := make([]gatewayMySQLRuntimeIssueRule, 0, len(gatewayMySQLRuntimeIssueRules))
	for _, rule := range gatewayMySQLRuntimeIssueRules {
		if rule.matches(query) {
			issues = append(issues, rule)
		}
	}
	return issues
}

// gatewayMySQLSQLiteRowidPattern is the textual half of the sqlite_rowid rule.
var gatewayMySQLSQLiteRowidPattern = regexp.MustCompile(`(?i)\browid\b`)

// gatewayMySQLWithoutRowidSuffix matches the table option that ends just before a
// rowid token, i.e. the WITHOUT ROWID suffix some SQLite schemas carry.
var gatewayMySQLWithoutRowidSuffix = regexp.MustCompile(`(?i)\bWITHOUT\s+$`)

// gatewayMySQLSQLiteRowid reports a rowid column reference. SQL comments and
// string literals are stripped first, so a query that documents why it avoids
// rowid, or stores the word in a string, is not audited as one that uses it.
// Backtick-quoted identifiers are the exception: MySQL spells a column the same
// way, and `rowid` still names the implicit column MySQL does not have, so those
// runs are re-checked against the original text. The WITHOUT ROWID table option
// is ignored: it is SQLite-only DDL, which this runtime guard does not own.
func gatewayMySQLSQLiteRowid(query string) bool {
	for i := 0; i < len(query); i++ {
		if query[i] != '`' {
			continue
		}
		end := gatewayMySQLSkipQuoted(query, i)
		if gatewayMySQLSQLiteRowidPattern.MatchString(query[i:end]) {
			return true
		}
		i = end - 1
	}
	text := gatewayMySQLWithoutComments(query)
	for _, match := range gatewayMySQLSQLiteRowidPattern.FindAllStringIndex(text, -1) {
		if gatewayMySQLWithoutRowidSuffix.MatchString(text[:match[0]]) {
			continue
		}
		return true
	}
	return false
}

// gatewayMySQLWithoutComments blanks out -- and /* */ comments and quoted runs,
// keeping the text length, so neither commentary nor a string literal that merely
// spells a keyword can trip a rule. A comment becomes one space so its removal
// cannot weld two tokens into a different word.
func gatewayMySQLWithoutComments(query string) string {
	var out strings.Builder
	out.Grow(len(query))
	for i := 0; i < len(query); {
		switch query[i] {
		case '\'', '"', '`':
			end := gatewayMySQLSkipQuoted(query, i)
			out.WriteString(strings.Repeat(" ", end-i))
			i = end
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				i = gatewayMySQLSkipLineComment(query, i)
				out.WriteByte(' ')
				continue
			}
			out.WriteByte(query[i])
			i++
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				i = gatewayMySQLSkipBlockComment(query, i)
				out.WriteByte(' ')
				continue
			}
			out.WriteByte(query[i])
			i++
		default:
			out.WriteByte(query[i])
			i++
		}
	}
	return out.String()
}

// gatewayMySQLSkipQuoted returns the index just past the quoted run that starts
// at i. A doubled quote is an escaped quote, which is how both SQLite and MySQL
// spell it inside a string.
func gatewayMySQLSkipQuoted(query string, i int) int {
	quote := query[i]
	i++
	for i < len(query) {
		if query[i] == quote {
			if i+1 < len(query) && query[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return len(query)
}

func gatewayMySQLSkipLineComment(query string, i int) int {
	i += 2
	for i < len(query) && query[i] != '\n' {
		i++
	}
	return i
}

func gatewayMySQLSkipBlockComment(query string, i int) int {
	i += 2
	for i+1 < len(query) {
		if query[i] == '*' && query[i+1] == '/' {
			return i + 2
		}
		i++
	}
	return len(query)
}

// gatewayMySQLIdentifierByte reports whether b can continue an SQL word, so a
// scan can tell the IN operator from the IN inside JOIN or index.
func gatewayMySQLIdentifierByte(b byte) bool {
	return b == '_' || b == '$' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

func gatewayMySQLSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

// gatewayMySQLWordAt reports whether word starts at i as a standalone SQL word.
func gatewayMySQLWordAt(query string, i int, word string) bool {
	if i < 0 || i+len(word) > len(query) {
		return false
	}
	if !strings.EqualFold(query[i:i+len(word)], word) {
		return false
	}
	if i > 0 && gatewayMySQLIdentifierByte(query[i-1]) {
		return false
	}
	return i+len(word) == len(query) || !gatewayMySQLIdentifierByte(query[i+len(word)])
}

// gatewayMySQLParenthesized returns the text between the parenthesis at open and
// its match. Quoted runs and comments are skipped so a bracket inside a literal
// cannot unbalance the scan.
func gatewayMySQLParenthesized(query string, open int) (string, bool) {
	depth := 0
	for i := open; i < len(query); {
		switch query[i] {
		case '\'', '"', '`':
			i = gatewayMySQLSkipQuoted(query, i)
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				i = gatewayMySQLSkipLineComment(query, i)
				continue
			}
			i++
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				i = gatewayMySQLSkipBlockComment(query, i)
				continue
			}
			i++
		case '(':
			depth++
			i++
		case ')':
			depth--
			if depth == 0 {
				return query[open+1 : i], true
			}
			i++
		default:
			i++
		}
	}
	return "", false
}

// gatewayMySQLTopLevelWord reports whether word appears in query outside every
// parenthesis, quoted run and comment.
func gatewayMySQLTopLevelWord(query, word string) bool {
	depth := 0
	for i := 0; i < len(query); {
		switch query[i] {
		case '\'', '"', '`':
			i = gatewayMySQLSkipQuoted(query, i)
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				i = gatewayMySQLSkipLineComment(query, i)
				continue
			}
			i++
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				i = gatewayMySQLSkipBlockComment(query, i)
				continue
			}
			i++
		case '(':
			depth++
			i++
		case ')':
			if depth > 0 {
				depth--
			}
			i++
		default:
			if depth == 0 && gatewayMySQLWordAt(query, i, word) {
				return true
			}
			i++
		}
	}
	return false
}

// gatewayMySQLLimitInSubquery reports a LIMIT that belongs to an IN (SELECT ...)
// subquery, or to the equivalent ALL/ANY/SOME form. MySQL 8.4 answers all four
// with Error 1235, so the statement must be rendered per dialect; SQLite keeps
// the single-statement form. The keyword must be followed directly by '(' and the
// LIMIT must sit at the subquery's own paren level, so an ordered derived table
// inside the subquery -- the MySQL rendering of the same batch -- stays legal.
func gatewayMySQLLimitInSubquery(query string) bool {
	for i := 0; i < len(query); i++ {
		width := 0
		for _, keyword := range [...]string{"IN", "ANY", "ALL", "SOME"} {
			if gatewayMySQLWordAt(query, i, keyword) {
				width = len(keyword)
				break
			}
		}
		if width == 0 {
			continue
		}
		j := i + width
		for j < len(query) && gatewayMySQLSpaceByte(query[j]) {
			j++
		}
		if j >= len(query) || query[j] != '(' {
			continue
		}
		body, ok := gatewayMySQLParenthesized(query, j)
		if !ok {
			continue
		}
		trimmed := strings.TrimLeft(body, " \t\n\r\f\v")
		if !gatewayMySQLWordAt(trimmed, 0, "SELECT") {
			continue
		}
		if gatewayMySQLTopLevelWord(body, "LIMIT") {
			return true
		}
	}
	return false
}

// gatewaySQLDialect names the SQL flavor a Gateway store connection speaks.
type gatewaySQLDialect string

const (
	gatewayDialectSQLite gatewaySQLDialect = "sqlite"
	gatewayDialectMySQL  gatewaySQLDialect = "mysql"
)

// gatewayDialectState holds the dialect every live store statement renders
// for. A Gateway process serves exactly one backend: OpenGatewayStore sets the
// value once from DOOPS_GATEWAY_STORE before the first statement runs, and the
// renderers below read it on every call.
//
// The value is atomic because renderers run on request goroutines. A process
// that hosts two stores at once (tests, offline tooling) must switch it with
// gatewayUseDialect, which holds a write lock for the whole callback so a render
// cannot observe one dialect while another store is mid-render. Production code
// never switches it after startup.
var gatewayDialectState atomic.Value

func gatewayDialect() gatewaySQLDialect {
	if d, ok := gatewayDialectState.Load().(gatewaySQLDialect); ok {
		return d
	}
	return gatewayDialectSQLite
}

func gatewaySetDialect(d gatewaySQLDialect) { gatewayDialectState.Store(d) }

var gatewayDialectScopeMu sync.Mutex

// gatewayUseDialect runs fn with the named dialect and restores the previous
// value afterwards, holding the scope lock for the whole callback.
func gatewayUseDialect(d gatewaySQLDialect, fn func()) {
	gatewayDialectScopeMu.Lock()
	previous := gatewayDialect()
	gatewayDialectState.Store(d)
	defer func() {
		gatewayDialectState.Store(previous)
		gatewayDialectScopeMu.Unlock()
	}()
	fn()
}

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
	return upsert.render(gatewayDialect())
}

// gatewayConcatSQL renders a string concatenation of SQL operands for the
// dialect the live store speaks.
//
// SQLite spells concatenation with the || operator, MySQL with the CONCAT()
// function; the two agree on the property the Gateway relies on, that one NULL
// operand makes the whole result NULL. CONCAT_WS is not equivalent and must not
// be substituted: it skips NULLs and would turn a missing value into a silent
// partial string. TestGatewayMySQLConcatKeepsNullSemantics proves the shared
// behaviour on both servers.
func gatewayConcatSQL(operands ...string) string {
	return gatewayConcatFor(gatewayDialect(), operands...)
}

func gatewayConcatFor(dialect gatewaySQLDialect, operands ...string) string {
	if len(operands) == 0 {
		panic("gatewayConcat needs at least one operand")
	}
	if dialect == gatewayDialectMySQL {
		return "CONCAT(" + strings.Join(operands, ", ") + ")"
	}
	return strings.Join(operands, " || ")
}

// gatewayEpochNowSQL renders "the database server's current time" as integer
// epoch seconds for the dialect the live store speaks.
func gatewayEpochNowSQL() string { return gatewayEpochNowFor(gatewayDialect()) }

// gatewayEpochNowFor renders the same clock per dialect: SQLite unixepoch(),
// MySQL UNIX_TIMESTAMP() with no argument. Both read the database clock rather
// than the Go process clock, both return integer UTC epoch seconds, and both
// are independent of the session time zone (NOW() and CURRENT_TIMESTAMP would
// follow the session zone and silently shift a comparison by hours).
func gatewayEpochNowFor(dialect gatewaySQLDialect) string {
	if dialect == gatewayDialectMySQL {
		return "UNIX_TIMESTAMP()"
	}
	return "unixepoch()"
}

// gatewayEpochSecondsFor renders the timestamp-to-epoch conversion per dialect.
//
// Precision: both spellings truncate to whole seconds. SQLite parses with
// strftime('%s', ...) and MySQL reads the first 19 characters with
// STR_TO_DATE(..., '%Y-%m-%dT%H:%i:%s'), so a fractional second is dropped by
// both and the comparison stays an integer comparison. MySQL DATETIME can hold
// microseconds but not the nanoseconds RFC3339Nano stores, so no rendering can
// keep them; a second-granular comparison is what both sources actually need.
//
// Time zone: the accepted input is the UTC "Z" form every writer produces
// through formatTime. TIMESTAMPDIFF over two naive DATETIME values is pure
// arithmetic and never applies the session zone (UNIX_TIMESTAMP('...') would
// reinterpret the value in the session zone). A value carrying a non-UTC offset
// is read as if it were UTC; that is a documented limitation of the transition,
// not a silently different comparison, because no writer emits an offset.
//
// NULL: an empty or unparsable value yields NULL in both dialects, so a
// comparison against it is unknown and the row is filtered out, which is what
// SQLite already does today.
func gatewayEpochSecondsFor(dialect gatewaySQLDialect, expression string) string {
	if dialect == gatewayDialectMySQL {
		return "TIMESTAMPDIFF(SECOND, '1970-01-01 00:00:00', STR_TO_DATE(SUBSTRING(" + expression + ", 1, 19), '%Y-%m-%dT%H:%i:%s'))"
	}
	return "CAST(strftime('%s', " + expression + ") AS INTEGER)"
}

// gatewayIdentifierFor renders one identifier for the dialect. SQLite keeps the
// caller's spelling bare because every name the Gateway uses is a plain
// identifier there; MySQL renders a backticked name, which is the only spelling
// MySQL 8.4 accepts for a reserved word such as the credential use column.
func gatewayIdentifierFor(dialect gatewaySQLDialect, name string) string {
	if dialect == gatewayDialectMySQL {
		return gatewayMySQLIdentifier(name)
	}
	return name
}

// gatewayUseColumn renders the credential "use" column for the dialect the live
// store speaks. USE is reserved in MySQL, so the bare spelling is a syntax error
// there (ERROR 1064); SQLite keeps the statements byte-identical to before.
func gatewayUseColumn() string { return gatewayIdentifierFor(gatewayDialect(), "use") }

// gatewayKeyColumn renders the agent_observations key column for the live
// dialect. KEY is reserved in MySQL 8.4 exactly like USE, so a bare spelling is
// Error 1064 (42000) "near 'key'"; SQLite has no reserved word there and keeps
// the statement byte-identical to the one written before INT-44.
func gatewayKeyColumn() string { return gatewayIdentifierFor(gatewayDialect(), "key") }

// gatewayKeyColumnFor is gatewayKeyColumn for an explicit dialect, for callers
// that render a statement once per engine (tests, offline tooling).
func gatewayKeyColumnFor(dialect gatewaySQLDialect) string {
	return gatewayIdentifierFor(dialect, "key")
}

// Identity comparisons must use the same case and trailing-space semantics as SQLite TEXT.
func gatewayIdentityTextFor(dialect gatewaySQLDialect, expression string) string {
	if dialect == gatewayDialectMySQL {
		collation := "utf8mb4_0900_bin"
		if gatewayIsMySQL57() {
			collation = "utf8mb4_bin"
		}
		return expression + " COLLATE " + collation
	}
	return expression
}

// gatewayJSONTextScalarFor renders a json_extract() value that has to read as the
// plain text SQLite returns for the same expression.
//
// Two MySQL facts force an explicit rendering, and both are properties of the
// expression, not of the column it is compared with:
//
//  1. JSON_EXTRACT returns the JSON type. Coerced to a string it keeps the JSON
//     quotes (JSON_EXTRACT('{"i":"x"}','$.i') is the three bytes "x", while
//     SQLite's json_extract returns x), so a Gateway that stored a bare identity
//     would compare and publish a quoted one on MySQL only.
//     JSON_UNQUOTE strips those quotes; numbers, true/false and objects keep the
//     text MySQL already produced.
//  2. The string form of a JSON value carries utf8mb4_bin. A UNION column
//     position (declaredAgentResourcesSQLFor) that also sees a schema column
//     declared utf8mb4_0900_bin (gateway_mysql_schema.sql) therefore aborts with
//     Error 1271 (HY000) "Illegal mix of collations for operation 'UNION'".
//     COLLATE pins the expression to the schema's identity collation.
//
// The collation is named utf8mb4_0900_bin on purpose and must never be relaxed
// to a case- or pad-insensitive rule such as utf8mb4_general_ci: those treat
// distinct identities (case-only, trailing-space) as equal and would silently
// change which resources the manifest declares. Changing the column definitions
// instead is also wrong: utf8mb4_0900_bin is the identity collation of every
// text column in the MySQL schema, the schema installer applies it, and
// relaxing it would change index and equality semantics for all tables to fix
// one expression.
func gatewayJSONTextScalarFor(dialect gatewaySQLDialect, expression string) string {
	if dialect == gatewayDialectMySQL {
		if gatewayIsMySQL57() {
			return "JSON_UNQUOTE(" + expression + ") COLLATE utf8mb4_bin"
		}
		return "JSON_UNQUOTE(" + expression + ") COLLATE utf8mb4_0900_bin"
	}
	return gatewayIdentityTextFor(dialect, expression)
}

// gatewayInPlaceholders renders n comma-separated "?" placeholders for IN / NOT IN
// clauses, avoiding JSON_TABLE (unavailable on MySQL 5.7) when the membership
// list is already materialized in Go.
func gatewayInPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// gatewayExistingTableCountSQL renders a count of how many of the named tables
// exist in the schema this connection reads. SQLite keeps its catalog in
// sqlite_schema; MySQL exposes information_schema.tables for DATABASE(). Both
// count 0 for a missing table, so callers keep a fail-closed comparison: a
// missing table can never be read as present, and the count query itself does
// not need the table to exist.
func gatewayExistingTableCountSQL(dialect gatewaySQLDialect, tableCount int) string {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", tableCount), ",")
	if dialect == gatewayDialectMySQL {
		return "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name IN (" + placeholders + ")"
	}
	return "SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN (" + placeholders + ")"
}

// gatewayJSONEachValuesSQL expands a JSON array of scalars into one row per
// element, so a Go-side list can drive IN (...) / NOT IN (...). SQLite's
// json_each is a table-valued function; MySQL has no such syntax and needs
// JSON_TABLE with an explicit scalar column type.
// gatewayTableExistsSQL renders a single-table existence probe. MySQL reads
// information_schema scoped to the connected database (DATABASE()), so the
// answer follows the pool's own schema instead of a hardcoded name.
func gatewayTableExistsSQL(dialect gatewaySQLDialect) string {
	if dialect == gatewayDialectMySQL {
		return "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?)"
	}
	return "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name = ?)"
}

func gatewayJSONEachValuesSQL(dialect gatewaySQLDialect, expression string) string {
	if dialect == gatewayDialectMySQL {
		return "SELECT jt.value FROM JSON_TABLE(" + expression + ", '$[*]' COLUMNS(value VARCHAR(512) PATH '$[0]')) AS jt"
	}
	return "SELECT value FROM json_each(" + expression + ")"
}

// gatewayJSONEachKeysSQL expands a JSON object into one row per member key.
// json_each yields the key column directly; MySQL must project JSON_KEYS first,
// because JSON_TABLE iterates array elements, not object members.
func gatewayJSONEachKeysSQL(dialect gatewaySQLDialect, expression string) string {
	if dialect == gatewayDialectMySQL {
		return "SELECT jk.k FROM JSON_TABLE(JSON_KEYS(" + expression + "), '$[*]' COLUMNS(k VARCHAR(512) PATH '$')) AS jk"
	}
	return "SELECT key FROM json_each(" + expression + ")"
}

// gatewayJSONObjectJoinSQL renders a join that walks a stored JSON object and
// exposes each member key plus its $.kind value. The dialects differ twice:
// SQLite iterates (key,value) pairs, while MySQL can only iterate keys and must
// read the member back through an escaped JSON path (JSON_QUOTE keeps keys that
// contain dots or quotes addressable).
func gatewayJSONObjectJoinSQL(dialect gatewaySQLDialect, table, expression, alias string) (from, key, kind string) {
	if dialect == gatewayDialectMySQL {
		keyColumn := alias + "_key"
		return table + " JOIN JSON_TABLE(JSON_KEYS(" + expression + "), '$[*]' COLUMNS(" + keyColumn + " VARCHAR(512) PATH '$')) " + alias + " ON TRUE",
			alias + "." + keyColumn,
			"JSON_UNQUOTE(JSON_EXTRACT(JSON_EXTRACT(" + expression + ", CONCAT('$.' , JSON_QUOTE(" + keyColumn + "))), '$.kind'))"
	}
	return table + ", json_each(" + expression + ") " + alias, alias + ".key", "json_extract(" + alias + ".value,'$.kind')"
}

// gatewayJSONOrderedArraySQL aggregates values into a JSON array. SQLite orders
// inside the aggregate; MySQL's JSON_ARRAYAGG accepts no ORDER BY and its row
// order is not a contract, so the MySQL form aggregates unordered and every
// caller that needs order sorts after decoding (order is owned by the reader).
// CAST(... AS CHAR) before COALESCE('[]') works around MySQL 5.7's correlated
// subquery bug where COALESCE(JSON_ARRAYAGG(...), JSON_ARRAY()) returns NULL
// when the subquery matches zero rows, keeping empty-set '[]' identical across
// SQLite, MySQL 5.7, and MySQL 8.0/8.4.
func gatewayJSONOrderedArraySQL(dialect gatewaySQLDialect, expression, order string) string {
	if dialect == gatewayDialectMySQL {
		return "COALESCE(CAST(JSON_ARRAYAGG(" + expression + ") AS CHAR), '[]')"
	}
	return "json_group_array(" + expression + " ORDER BY " + order + ")"
}

// gatewayUpdateIgnoreSQL renders SQLite's UPDATE OR IGNORE. MySQL spells the
// same statement UPDATE IGNORE: a row whose update violates a constraint is
// skipped instead of failing the statement, and RowsAffected stays 0 for it, so
// callers that turn "0 rows" into a conflict error keep their contract. MySQL
// also downgrades some non-constraint errors to warnings, which SQLite does
// not; the one caller only writes identity columns of a single row, so the
// broader spirit does not change its outcome.
func gatewayUpdateIgnoreSQL(dialect gatewaySQLDialect, tail string) string {
	if dialect == gatewayDialectMySQL {
		return "UPDATE IGNORE " + tail
	}
	return "UPDATE OR IGNORE " + tail
}

// gatewayNullSafeEqualsSQL renders SQLite's IS comparison, which treats NULL as
// a value equal to itself. MySQL spells that <=>; plain = would make a NULL
// input never match and silently turn a duplicate into a new row.
func gatewayNullSafeEqualsSQL(dialect gatewaySQLDialect, left, right string) string {
	if dialect == gatewayDialectMySQL {
		return left + " <=> " + right
	}
	return left + " IS " + right
}

// gatewayFilteredCountSQL renders SQLite's count(*) FILTER (WHERE condition).
// MySQL has no FILTER clause; SUM(CASE WHEN condition THEN 1 ELSE 0 END) counts
// the same rows, and COALESCE keeps the empty-set answer 0 like count() (SUM
// over no rows is NULL).
func gatewayFilteredCountSQL(dialect gatewaySQLDialect, condition string) string {
	if dialect == gatewayDialectMySQL {
		return "COALESCE(SUM(CASE WHEN " + condition + " THEN 1 ELSE 0 END), 0)"
	}
	return "count(*) FILTER(WHERE " + condition + ")"
}

func (upsert gatewayUpsert) render(dialect gatewaySQLDialect) string {
	if dialect == gatewayDialectMySQL {
		return upsert.mysqlSQL()
	}
	return upsert.sqliteSQL()
}

var gatewayUpsertValuesTail = regexp.MustCompile(`(?is)(\bVALUES\s*)(\([^()]+\))\s*$`)

// gatewayUpsertBatchInsert repeats the trailing VALUES (...) tuple rowCount
// times so multi-row upserts reuse the exact single-row gatewayUpsert contract
// across both SQLite and MySQL without N+1 round-trips.
func gatewayUpsertBatchInsert(insert string, rowCount int) string {
	if rowCount <= 1 {
		return insert
	}
	trimmed := strings.TrimSpace(insert)
	loc := gatewayUpsertValuesTail.FindStringSubmatchIndex(trimmed)
	if loc == nil {
		return insert
	}
	tuple := trimmed[loc[4]:loc[5]]
	var b strings.Builder
	b.Grow(len(trimmed) + (len(tuple)+1)*(rowCount-1))
	b.WriteString(trimmed[:loc[4]])
	for i := 0; i < rowCount; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(tuple)
	}
	return b.String()
}

// gatewayUpsertBatchSQLFor renders a multi-row upsert for dialect from the
// canonical single-row gatewayUpsert definition.
func gatewayUpsertBatchSQLFor(dialect gatewaySQLDialect, upsert gatewayUpsert, rowCount int) string {
	batched := upsert
	batched.insert = gatewayUpsertBatchInsert(upsert.insert, rowCount)
	return batched.render(dialect)
}

// gatewayUpsertBatchSQL renders a multi-row upsert for the active store dialect.
func gatewayUpsertBatchSQL(upsert gatewayUpsert, rowCount int) string {
	return gatewayUpsertBatchSQLFor(gatewayDialect(), upsert, rowCount)
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
		column = strings.TrimSpace(column)
		// A column that already carries backticks is left alone: the literal
		// spells the reserved word itself, which SQLite accepts as a quoted
		// identifier, and double-quoting would produce invalid MySQL.
		if strings.HasPrefix(column, "`") && strings.HasSuffix(column, "`") {
			quoted = append(quoted, column)
			continue
		}
		quoted = append(quoted, gatewayMySQLIdentifier(column))
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

// gatewayMySQLUpsertSelect detects an INSERT ... SELECT, whose ON DUPLICATE KEY
// UPDATE assignments cannot use a bare column name: the source table carries the
// same name, so MySQL rejects the statement with ERROR 1052.
var gatewayMySQLUpsertSelect = regexp.MustCompile("(?is)\\bSELECT\\b")

func gatewayMySQLUpsertNoOpColumn(upsert gatewayUpsert) string {
	match := gatewayMySQLUpsertInsertHead.FindStringSubmatch(strings.TrimSpace(upsert.insert))
	if match != nil {
		columns := strings.Split(match[3], ",")
		column := gatewayMySQLIdentifier(strings.TrimSpace(columns[0]))
		if gatewayMySQLUpsertSelect.MatchString(upsert.insert) {
			return gatewayMySQLIdentifier(match[2]) + "." + column
		}
		return column
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

// gatewayListBaseTablesSQLFor returns the query listing user base tables in
// deterministic alphabetical order for dialect.
func gatewayListBaseTablesSQLFor(dialect gatewaySQLDialect) string {
	if dialect == gatewayDialectMySQL {
		return `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name`
	}
	return `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
}

// gatewayTableColumnsSQLFor returns the query (and bind args) for discovering
// non-generated column names of table in ordinal order for dialect.
func gatewayTableColumnsSQLFor(dialect gatewaySQLDialect, table string) (string, []any) {
	if dialect == gatewayDialectMySQL {
		return `SELECT column_name FROM information_schema.columns
			 WHERE table_schema = DATABASE() AND table_name = ? AND extra NOT LIKE '%GENERATED%'
			 ORDER BY ordinal_position`, []any{table}
	}
	return "PRAGMA table_info(" + gatewayIdentifierFor(gatewayDialectSQLite, table) + ")", nil
}

// gatewaySQLiteWALCheckpointSQL returns the SQLite WAL truncation checkpoint
// statement used by storage reclamation on the SQLite backend.
func gatewaySQLiteWALCheckpointSQL() string {
	return "PRAGMA wal_checkpoint(TRUNCATE)"
}
