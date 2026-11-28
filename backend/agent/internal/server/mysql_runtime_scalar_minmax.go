package server

// INT-40: SQLite's two-argument MIN(a, b) / MAX(a, b) are scalar functions.
// MySQL's MIN/MAX are aggregate-only, so passing a second argument is a parse
// error -- Error 1064 (42000) -- before any row is read. The rule lives in the
// dialect audit so the next statement written the SQLite way fails at test
// time, and the rendering pair below gives MySQL the LEAST/GREATEST spelling.

import (
	"regexp"
	"strings"
)

// gatewayMySQLScalarMinMaxPattern matches the uppercase MIN/MAX words that can
// start a call. The \b boundaries keep identifiers such as minimum( out, and
// the pattern is case-sensitive so Go's builtin min(a, b)/max(a, b) -- always
// lowercase -- cannot be reported.
var gatewayMySQLScalarMinMaxPattern = regexp.MustCompile("\\b(?:MIN|MAX)[ \\t\\r\\n]*\\(")

// gatewayMySQLScalarMinMax reports a MIN/MAX call with two or more top-level
// arguments. One argument stays the aggregate form and is not a finding.
// Comments and string literals are blanked first, so neither commentary nor a
// literal that merely spells MIN(a, b) can trip the rule.
func gatewayMySQLScalarMinMax(query string) bool {
	text := gatewayMySQLWithoutComments(query)
	for _, match := range gatewayMySQLScalarMinMaxPattern.FindAllStringIndex(text, -1) {
		word := strings.TrimSpace(strings.TrimSuffix(text[match[0]:match[1]], "("))
		if word != "MIN" && word != "MAX" {
			continue
		}
		body, ok := gatewayMySQLParenthesized(text, match[1]-1)
		if !ok {
			continue
		}
		if gatewayMySQLTopLevelArgumentCount(body) >= 2 {
			return true
		}
	}
	return false
}

// gatewayMySQLTopLevelArgumentCount counts the arguments in one call body by
// counting commas outside nested parentheses. The body is expected to come from
// gatewayMySQLWithoutComments, so quoted commas are already blanked.
func gatewayMySQLTopLevelArgumentCount(body string) int {
	depth, count := 0, 1
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				count++
			}
		}
	}
	return count
}

// gatewayScalarLeastSQL renders a two-argument scalar minimum for the dialect.
// SQLite spells it MIN(a, b); MySQL has no scalar MIN, so the same value is
// LEAST(a, b). Both return NULL when either argument is NULL -- unlike a
// comparison or COALESCE -- which is the property the INT-40 test asserts on
// real SQLite and real MySQL.
func gatewayScalarLeastSQL(dialect gatewaySQLDialect, a, b string) string {
	if dialect == gatewayDialectMySQL {
		return "LEAST(" + a + ", " + b + ")"
	}
	return "MIN(" + a + ", " + b + ")"
}

// gatewayScalarGreatestSQL is the MAX(a, b) counterpart of gatewayScalarLeastSQL.
func gatewayScalarGreatestSQL(dialect gatewaySQLDialect, a, b string) string {
	if dialect == gatewayDialectMySQL {
		return "GREATEST(" + a + ", " + b + ")"
	}
	return "MAX(" + a + ", " + b + ")"
}

// gatewayScalarMinMaxSQL renders an arbitrary scalar MIN/MAX pair. The caller
// names the SQLite function and the MySQL function because not every pair is
// LEAST/GREATEST-shaped after rewriting (for example a clamp needs both).
func gatewayScalarMinMaxSQL(dialect gatewaySQLDialect, sqliteName, mysqlName, a, b string) string {
	if dialect == gatewayDialectMySQL {
		return mysqlName + "(" + a + ", " + b + ")"
	}
	return sqliteName + "(" + a + ", " + b + ")"
}
