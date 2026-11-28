package server

import (
	"errors"
	"strings"
	"unicode"

	"github.com/user/doops/agent/internal/dosql/lexer"
)

const doSQLMaxStatements = 2048 // Raised from 64 for MongoDB batches; SQL batches use lexer.MaxBatchBytes (4 MiB).

var doSQLReadKeywords = map[string]bool{
	"SELECT": true, "WITH": true, "SHOW": true, "DESCRIBE": true, "DESC": true,
	"EXPLAIN": true, "VALUES": true, "TABLE": true,
}

var doSQLTransactionKeywords = map[string]bool{
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "START": true, "END": true,
	"SAVEPOINT": true, "RELEASE": true, "ABORT": true,
}

var doSQLImplicitCommitKeywords = map[string]bool{
	"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true, "RENAME": true,
	"GRANT": true, "REVOKE": true, "ANALYZE": true, "OPTIMIZE": true, "REPAIR": true,
	"LOCK": true, "UNLOCK": true, "FLUSH": true, "COMMENT": true, "AUDIT": true,
}

// splitDoSQLStatements delegates to the unified Go SQL lexer (supporting MySQL
// DELIMITER/BEGIN..END/mysqldump versioned SET comments, PG dollar quotes and
// CONCURRENTLY, Oracle PL/SQL '/', MSSQL GO, and batches up to 4 MiB).
func splitDoSQLStatements(engine, text string) ([]string, error) {
	parsed, err := lexer.Split(engine, text)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(parsed))
	for i, s := range parsed {
		out[i] = s.Executable
	}
	return out, nil
}

func doSQLIdentifierRune(char rune) bool {
	return char == '_' || unicode.IsLetter(char) || unicode.IsDigit(char)
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
		case strings.HasPrefix(remaining, "("):
			remaining = strings.TrimSpace(remaining[1:])
			continue
		}
		break
	}
	var words []string
	for _, field := range strings.FieldsFunc(remaining, func(char rune) bool {
		return !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char == '_')
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
	if len(words) != 1 || !doSQLReadKeywords[words[0]] {
		return false
	}
	if words[0] == "WITH" {
		stmts, err := lexer.Split("postgres", statement)
		if err == nil && len(stmts) == 1 && stmts[0].Kind != lexer.KindRead {
			return false
		}
	}
	return true
}

func doSQLStatementFingerprint(statements []string) string {
	return lexer.BatchFingerprint(statements)
}

func doSQLMySQLStatementWords(statement string) ([]string, error) {
	runes := []rune(statement)
	var words []string
	for i := 0; i < len(runes); {
		switch {
		case runes[i] == '#' || runes[i] == '-' && i+2 < len(runes) && runes[i+1] == '-' && unicode.IsSpace(runes[i+2]):
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
		case runes[i] == '/' && i+1 < len(runes) && runes[i+1] == '*':
			if i+2 < len(runes) && runes[i+2] == '!' ||
				i+3 < len(runes) && runes[i+2] == 'M' && runes[i+3] == '!' {
				return nil, errors.New("dosql_statement_executable_comment_unsupported")
			}
			i += 2
			for i+1 < len(runes) && !(runes[i] == '*' && runes[i+1] == '/') {
				i++
			}
			if i+1 >= len(runes) {
				return nil, errors.New("dosql_statement_unterminated_comment")
			}
			i += 2
		case runes[i] == '\'' || runes[i] == '"' || runes[i] == '`':
			end := doSQLClosingQuote(runes, i, runes[i], runes[i] != '`')
			if end < 0 {
				return nil, errors.New("dosql_statement_unterminated_quote")
			}
			if runes[i] == '`' || runes[i] == '"' {
				words = append(words, strings.ToUpper(string(runes[i+1:end])))
			}
			i = end + 1
		case doSQLIdentifierRune(runes[i]):
			start := i
			for i < len(runes) && doSQLIdentifierRune(runes[i]) {
				i++
			}
			words = append(words, strings.ToUpper(string(runes[start:i])))
		default:
			i++
		}
	}
	if len(words) == 0 {
		return nil, errors.New("dosql_statement_invalid")
	}
	if doSQLTransactionKeywords[words[0]] || words[0] == "XA" ||
		words[0] == "SET" && (containsDoSQLString(words[1:], "AUTOCOMMIT") || containsDoSQLString(words[1:], "TRANSACTION")) {
		return words, errors.New("dosql_statement_transaction_control_forbidden: DoSql owns the transaction and autocommit settings")
	}
	return words, nil
}

func doSQLMySQLDryRunSafe(words []string) bool {
	switch words[0] {
	case "INSERT", "UPDATE", "DELETE", "REPLACE":
		return true
	case "SELECT", "WITH", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "VALUES", "TABLE":
		for i := 1; i < len(words); i++ {
			if words[i-1] == "INTO" && (words[i] == "OUTFILE" || words[i] == "DUMPFILE") {
				return false
			}
		}
		return true
	}
	return false
}

func doSQLEngineCommitsDDLImplicitly(engine string) bool {
	return lexer.EngineCommitsDDLImplicitly(engine)
}

func doSQLCommitsImplicitly(engine, statement string) bool {
	if !doSQLEngineCommitsDDLImplicitly(engine) {
		return false
	}
	words := doSQLLeadingKeywords(statement, 3)
	if len(words) == 0 {
		return false
	}
	if engine == "mysql" {
		words, _ = doSQLMySQLStatementWords(statement)
		if len(words) == 0 {
			return false
		}
		switch words[0] {
		case "CHECK", "CACHE", "LOAD", "IMPORT", "INSTALL", "UNINSTALL", "BEGIN", "START", "STOP", "CHANGE":
			return true
		case "RESET":
			return len(words) < 2 || words[1] != "PERSIST"
		case "SET":
			return len(words) > 1 && words[1] == "PASSWORD" || containsDoSQLString(words[1:], "AUTOCOMMIT")
		}
	}
	return doSQLImplicitCommitKeywords[words[0]]
}
