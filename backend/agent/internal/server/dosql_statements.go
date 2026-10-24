package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"unicode"
)

const doSQLMaxStatements = 64

var doSQLDollarQuoteTag = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)?\$`)

var doSQLReadKeywords = map[string]bool{
	"SELECT": true, "WITH": true, "SHOW": true, "DESCRIBE": true, "DESC": true,
	"EXPLAIN": true, "VALUES": true, "TABLE": true,
}

// The runner owns the transaction boundary so one approval covers one atomic
// unit; caller-supplied transaction control would silently split or end it.
var doSQLTransactionKeywords = map[string]bool{
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "START": true, "END": true,
	"SAVEPOINT": true, "RELEASE": true, "ABORT": true,
}

// splitDoSQLStatements is the single SQL batch splitter shared by Gateway
// (approval fingerprints) and Agent (execution). Comments are kept verbatim
// because Oracle optimizer hints live in them.
func splitDoSQLStatements(engine, text string) ([]string, error) {
	// JavaScript and Go disagree on whether these are whitespace, which would
	// make the executor's statement fingerprint differ from the signed one.
	if strings.ContainsAny(text, "\u0085\uFEFF") {
		return nil, errors.New("dosql_statement_whitespace_unsupported")
	}
	runes := []rune(text)
	var statements []string
	var current strings.Builder
	hasCode := false
	flush := func() {
		statement := strings.TrimSpace(current.String())
		if hasCode && statement != "" {
			statements = append(statements, statement)
		}
		current.Reset()
		hasCode = false
	}
	for index := 0; index < len(runes); index++ {
		char := runes[index]
		switch {
		case char == ';':
			flush()
			continue
		case char == '-' && index+1 < len(runes) && runes[index+1] == '-',
			char == '#' && engine == "mysql":
			end := index
			for end < len(runes) && runes[end] != '\n' {
				end++
			}
			current.WriteString(string(runes[index:end]))
			index = end - 1
			continue
		case char == '/' && index+1 < len(runes) && runes[index+1] == '*':
			end := strings.Index(string(runes[index+2:]), "*/")
			if end < 0 {
				return nil, errors.New("dosql_statement_unterminated_comment")
			}
			length := len([]rune(string(runes[index+2:])[:end])) + 4
			current.WriteString(string(runes[index : index+length]))
			index += length - 1
			continue
		case char == '\'' || char == '"' || (char == '`' && engine == "mysql"):
			backslash := engine == "mysql" && char != '`' ||
				engine == "postgres" && char == '\'' && index > 0 && (runes[index-1] == 'E' || runes[index-1] == 'e')
			end := doSQLClosingQuote(runes, index, char, backslash)
			if end < 0 {
				return nil, errors.New("dosql_statement_unterminated_quote")
			}
			current.WriteString(string(runes[index : end+1]))
			index = end
			hasCode = true
			continue
		case char == '$' && engine == "postgres" && (index == 0 || !doSQLIdentifierRune(runes[index-1])):
			if tag := doSQLDollarQuoteTag.FindString(string(runes[index:])); tag != "" {
				body := string(runes[index+len([]rune(tag)):])
				end := strings.Index(body, tag)
				if end < 0 {
					return nil, errors.New("dosql_statement_unterminated_dollar_quote")
				}
				length := len([]rune(tag)) + len([]rune(body[:end])) + len([]rune(tag))
				current.WriteString(string(runes[index : index+length]))
				index += length - 1
				hasCode = true
				continue
			}
		}
		if !unicode.IsSpace(char) {
			hasCode = true
		}
		current.WriteRune(char)
	}
	flush()
	if len(statements) == 0 {
		return nil, errors.New("dosql_statement_required")
	}
	if len(statements) > doSQLMaxStatements {
		return nil, errors.New("dosql_statement_batch_too_large")
	}
	for _, statement := range statements {
		words := doSQLLeadingKeywords(statement, 4)
		if len(words) == 0 {
			return nil, errors.New("dosql_statement_invalid")
		}
		if doSQLTransactionKeywords[words[0]] && !(engine == "oracle" && words[0] == "BEGIN") {
			return nil, errors.New("dosql_statement_transaction_control_forbidden: remove BEGIN/COMMIT, DoSql runs the batch in one transaction")
		}
		if engine == "oracle" && doSQLOraclePLSQL(words) {
			return nil, errors.New("dosql_statement_plsql_unsupported")
		}
	}
	return statements, nil
}

func doSQLClosingQuote(runes []rune, start int, quote rune, backslash bool) int {
	for index := start + 1; index < len(runes); index++ {
		if backslash && runes[index] == '\\' {
			index++
			continue
		}
		if runes[index] == quote {
			if index+1 < len(runes) && runes[index+1] == quote {
				index++
				continue
			}
			return index
		}
	}
	return -1
}

func doSQLIdentifierRune(char rune) bool {
	return char == '_' || unicode.IsLetter(char) || unicode.IsDigit(char)
}

func doSQLOraclePLSQL(words []string) bool {
	switch words[0] {
	case "BEGIN", "DECLARE":
		return true
	case "CREATE":
		for _, word := range words[1:] {
			switch word {
			case "PROCEDURE", "FUNCTION", "PACKAGE", "TRIGGER", "TYPE":
				return true
			}
		}
	}
	return false
}

func doSQLLeadingKeywords(statement string, limit int) []string {
	remaining := strings.TrimSpace(statement)
	for {
		switch {
		case strings.HasPrefix(remaining, "--"), strings.HasPrefix(remaining, "#"):
			newline := strings.IndexByte(remaining, '\n')
			if newline < 0 {
				return nil
			}
			remaining = strings.TrimSpace(remaining[newline+1:])
			continue
		case strings.HasPrefix(remaining, "/*"):
			end := strings.Index(remaining, "*/")
			if end < 0 {
				return nil
			}
			remaining = strings.TrimSpace(remaining[end+2:])
			continue
		}
		break
	}
	var words []string
	for _, field := range strings.FieldsFunc(remaining, func(char rune) bool {
		return !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z')
	}) {
		words = append(words, strings.ToUpper(field))
		if len(words) == limit {
			break
		}
	}
	if len(words) == 0 || !strings.HasPrefix(strings.ToUpper(remaining), words[0]) {
		return nil
	}
	return words
}

func doSQLReadOnlyStatement(statement string) bool {
	words := doSQLLeadingKeywords(statement, 1)
	return len(words) == 1 && doSQLReadKeywords[words[0]]
}

// doSQLStatementFingerprint mirrors change-policy.mjs normalizeStatement over
// statements.join(";\n"); the executor recomputes it to verify the approval.
func doSQLStatementFingerprint(statements []string) string {
	normalized := strings.TrimRight(strings.TrimSpace(strings.Join(statements, ";\n")), ";")
	var builder strings.Builder
	inSpace := false
	for _, char := range normalized {
		if unicode.IsSpace(char) {
			if !inSpace {
				builder.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		builder.WriteRune(char)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

// MySQL and Oracle commit the open transaction before and after DDL, so a
// batch containing DDL cannot be rolled back or dry-run on those engines.
var doSQLImplicitCommitKeywords = map[string]bool{
	"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true, "RENAME": true,
	"GRANT": true, "REVOKE": true, "ANALYZE": true, "OPTIMIZE": true, "REPAIR": true,
	"LOCK": true, "UNLOCK": true, "FLUSH": true, "COMMENT": true, "AUDIT": true,
}

// Unknown (external) engines are assumed to commit DDL implicitly: claiming a
// rollback that the engine cannot honour is worse than refusing a dry run.
func doSQLEngineCommitsDDLImplicitly(engine string) bool {
	switch engine {
	case "postgres", "mssql", "mongodb":
		return false
	}
	return true
}

// doSQLCommitsImplicitly reports whether running the statement ends the
// transaction on its own. Postgres DDL is transactional and never matches.
func doSQLCommitsImplicitly(engine, statement string) bool {
	if !doSQLEngineCommitsDDLImplicitly(engine) {
		return false
	}
	words := doSQLLeadingKeywords(statement, 1)
	return len(words) == 1 && doSQLImplicitCommitKeywords[words[0]]
}
