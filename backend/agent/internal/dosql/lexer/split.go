package lexer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"unicode"
)

const MaxBatchBytes = 4 << 20 // 4 MiB replaces the legacy 64-statement hard cap.

type StatementKind string

const (
	KindRead           StatementKind = "read"
	KindDML            StatementKind = "dml"
	KindDDL            StatementKind = "ddl"
	KindSession        StatementKind = "session"
	KindNonTransaction StatementKind = "non_transactional"
	KindOther          StatementKind = "other"
)

type SegmentKind string

const (
	SegmentSession        SegmentKind = "session"
	SegmentTransaction    SegmentKind = "transaction"
	SegmentImplicitCommit SegmentKind = "implicit_commit"
	SegmentNonTransaction SegmentKind = "non_transactional"
)

type Statement struct {
	Index           int           `json:"index"`
	Raw             string        `json:"raw"`
	Executable      string        `json:"executable"`
	Kind            StatementKind `json:"kind"`
	ImplicitCommit  bool          `json:"implicit_commit,omitempty"`
	NonTransaction  bool          `json:"non_transactional,omitempty"`
	LeadingKeywords []string      `json:"leading_keywords,omitempty"`
	Hash            string        `json:"hash"`
}

type Segment struct {
	Index      int         `json:"index"`
	Kind       SegmentKind `json:"kind"`
	Atomic     bool        `json:"atomic"`
	Statements []Statement `json:"statements"`
}

var (
	dollarQuoteTag = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)?\$`)
	identTokenRe   = regexp.MustCompile(`[A-Z_][A-Z0-9_]*`)

	readKeywords = map[string]bool{
		"SELECT": true, "WITH": true, "SHOW": true, "DESCRIBE": true, "DESC": true,
		"EXPLAIN": true, "VALUES": true, "TABLE": true,
	}
	dmlKeywords = map[string]bool{
		"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true, "MERGE": true, "UPSERT": true,
	}
	transactionKeywords = map[string]bool{
		"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "START": true, "END": true,
		"SAVEPOINT": true, "RELEASE": true, "ABORT": true,
	}
	implicitCommitKeywords = map[string]bool{
		"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true, "RENAME": true,
		"GRANT": true, "REVOKE": true, "ANALYZE": true, "OPTIMIZE": true, "REPAIR": true,
		"LOCK": true, "UNLOCK": true, "FLUSH": true, "COMMENT": true, "AUDIT": true,
	}
	compoundBlockOpeners = map[string]bool{
		"BEGIN": true, "CASE": true, "IF": true, "LOOP": true, "WHILE": true, "REPEAT": true,
	}
	safeMySQLSessionVars = map[string]bool{
		"NAMES": true, "CHARACTER": true, "FOREIGN_KEY_CHECKS": true, "UNIQUE_CHECKS": true,
		"SQL_MODE": true, "TIME_ZONE": true, "SQL_NOTES": true, "SQL_SAFE_UPDATES": true,
		"CHARACTER_SET_CLIENT": true, "CHARACTER_SET_RESULTS": true, "COLLATION_CONNECTION": true,
		"LOCK_WAIT_TIMEOUT": true, "INNODB_LOCK_WAIT_TIMEOUT": true, "GROUP_CONCAT_MAX_LEN": true,
		"NET_READ_TIMEOUT": true, "NET_WRITE_TIMEOUT": true, "MAX_EXECUTION_TIME": true,
	}
)

// Split splits and classifies a SQL batch for the given engine.
// Supports MySQL DELIMITER, BEGIN..END compound blocks, standalone mysqldump
// versioned SET comments, PostgreSQL dollar-quoting & CONCURRENTLY, Oracle PL/SQL
// with '/' terminators, and SQL Server GO batch separators.
func Split(engine, text string) ([]Statement, error) {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if strings.ContainsAny(text, "\u0085\uFEFF") {
		return nil, errors.New("dosql_statement_whitespace_unsupported")
	}
	if len(text) > MaxBatchBytes {
		return nil, errors.New("dosql_statement_batch_too_large")
	}
	rawStatements, err := scanRawStatements(engine, text)
	if err != nil {
		return nil, err
	}
	if len(rawStatements) == 0 {
		return nil, errors.New("dosql_statement_required")
	}
	out := make([]Statement, 0, len(rawStatements))
	for i, raw := range rawStatements {
		stmt, err := classifyStatement(engine, i+1, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, stmt)
	}
	return out, nil
}

// PlanSegments groups classified statements into sequential execution segments
// so mixed DDL + DML + session SET + PG non-transactional batches execute cleanly
// without rejecting valid business migrations.
func PlanSegments(statements []Statement) []Segment {
	var segments []Segment
	var txBuf []Statement
	flushTx := func() {
		if len(txBuf) == 0 {
			return
		}
		segments = append(segments, Segment{
			Index:      len(segments) + 1,
			Kind:       SegmentTransaction,
			Atomic:     true,
			Statements: append([]Statement(nil), txBuf...),
		})
		txBuf = txBuf[:0]
	}
	for _, stmt := range statements {
		switch {
		case stmt.Kind == KindSession:
			flushTx()
			if len(segments) > 0 && segments[len(segments)-1].Kind == SegmentSession {
				segments[len(segments)-1].Statements = append(segments[len(segments)-1].Statements, stmt)
			} else {
				segments = append(segments, Segment{
					Index:      len(segments) + 1,
					Kind:       SegmentSession,
					Atomic:     false,
					Statements: []Statement{stmt},
				})
			}
		case stmt.NonTransaction:
			flushTx()
			segments = append(segments, Segment{
				Index:      len(segments) + 1,
				Kind:       SegmentNonTransaction,
				Atomic:     false,
				Statements: []Statement{stmt},
			})
		case stmt.ImplicitCommit:
			flushTx()
			segments = append(segments, Segment{
				Index:      len(segments) + 1,
				Kind:       SegmentImplicitCommit,
				Atomic:     false,
				Statements: []Statement{stmt},
			})
		default:
			txBuf = append(txBuf, stmt)
		}
	}
	flushTx()
	return segments
}

// StatementHash computes a whitespace-normalized SHA-256 hex digest.
func StatementHash(statement string) string {
	return BatchFingerprint([]string{statement})
}

// BatchFingerprint matches the legacy whitespace-normalized fingerprint format.
func BatchFingerprint(statements []string) string {
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

func scanRawStatements(engine, text string) ([]string, error) {
	runes := []rune(text)
	delimiter := []rune(";")
	var statements []string
	var current strings.Builder
	hasCode := false
	compoundDepth := 0
	inCompoundRoutine := false
	oraclePLSQL := false

	flush := func() {
		stmt := strings.TrimSpace(current.String())
		if hasCode && stmt != "" {
			statements = append(statements, stmt)
		}
		current.Reset()
		hasCode = false
		compoundDepth = 0
		inCompoundRoutine = false
		oraclePLSQL = false
	}

	atLineStart := true
	for index := 0; index < len(runes); index++ {
		char := runes[index]

		// Handle line-oriented directives: MySQL DELIMITER, MSSQL GO, Oracle /
		if atLineStart {
			lineEnd := index
			for lineEnd < len(runes) && runes[lineEnd] != '\n' {
				lineEnd++
			}
			line := strings.TrimSpace(string(runes[index:lineEnd]))
			upperLine := strings.ToUpper(line)
			if engine == "mysql" && strings.HasPrefix(upperLine, "DELIMITER ") {
				flush()
				nextDelim := strings.TrimSpace(line[len("DELIMITER "):])
				if nextDelim == "" {
					return nil, errors.New("dosql_statement_invalid: DELIMITER requires a token")
				}
				delimiter = []rune(nextDelim)
				index = lineEnd
				atLineStart = true
				continue
			}
			if engine == "mssql" && (upperLine == "GO" || strings.HasPrefix(upperLine, "GO ") || strings.HasPrefix(upperLine, "GO\t")) {
				flush()
				index = lineEnd
				atLineStart = true
				continue
			}
			if engine == "oracle" && line == "/" && hasCode {
				flush()
				index = lineEnd
				atLineStart = true
				continue
			}
		}

		// Custom or default delimiter match (outside compound blocks)
		if len(delimiter) > 0 && runesMatchAt(runes, index, delimiter) {
			isDefaultSemi := len(delimiter) == 1 && delimiter[0] == ';'
			if !isDefaultSemi || (compoundDepth == 0 && !oraclePLSQL) {
				flush()
				index += len(delimiter) - 1
				atLineStart = false
				continue
			}
		}

		switch {
		case char == '-' && index+1 < len(runes) && runes[index+1] == '-' &&
			(engine != "mysql" || index+2 == len(runes) || unicode.IsSpace(runes[index+2])),
			char == '#' && engine == "mysql":
			end := index
			for end < len(runes) && runes[end] != '\n' {
				end++
			}
			current.WriteString(string(runes[index:end]))
			index = end - 1
			atLineStart = false
			continue

		case char == '/' && index+1 < len(runes) && runes[index+1] == '*':
			end := strings.Index(string(runes[index+2:]), "*/")
			if end < 0 {
				return nil, errors.New("dosql_statement_unterminated_comment")
			}
			length := len([]rune(string(runes[index+2:])[:end])) + 4
			commentText := string(runes[index : index+length])
			isExecComment := engine == "mysql" && (index+2 < len(runes) && runes[index+2] == '!' ||
				index+3 < len(runes) && runes[index+2] == 'M' && runes[index+3] == '!')
			if isExecComment {
				// Standalone top-level mysqldump versioned comment (e.g. /*!40101 SET NAMES utf8mb4 */)
				// is allowed through to statement classification; inline executable comments embedded
				// inside another statement (e.g. SELECT 1 /*!80000 INTO OUTFILE ... */) are rejected.
				if hasCode {
					return nil, errors.New("dosql_statement_executable_comment_unsupported")
				}
				hasCode = true
			}
			current.WriteString(commentText)
			index += length - 1
			atLineStart = false
			continue

		case char == '\'' || char == '"' || (char == '`' && engine == "mysql"):
			backslash := engine == "mysql" && char != '`' ||
				engine == "postgres" && char == '\'' && index > 0 && (runes[index-1] == 'E' || runes[index-1] == 'e')
			end := closingQuote(runes, index, char, backslash)
			if end < 0 {
				return nil, errors.New("dosql_statement_unterminated_quote")
			}
			current.WriteString(string(runes[index : end+1]))
			index = end
			hasCode = true
			atLineStart = false
			continue

		case char == '$' && engine == "postgres" && (index == 0 || !isIdentRune(runes[index-1])):
			if tag := dollarQuoteTag.FindString(string(runes[index:])); tag != "" {
				body := string(runes[index+len([]rune(tag)):])
				end := strings.Index(body, tag)
				if end < 0 {
					return nil, errors.New("dosql_statement_unterminated_dollar_quote")
				}
				length := len([]rune(tag)) + len([]rune(body[:end])) + len([]rune(tag))
				current.WriteString(string(runes[index : index+length]))
				index += length - 1
				hasCode = true
				atLineStart = false
				continue
			}

		case isIdentRune(char) && (index == 0 || !isIdentRune(runes[index-1])):
			end := index
			for end < len(runes) && isIdentRune(runes[end]) {
				end++
			}
			word := strings.ToUpper(string(runes[index:end]))
			current.WriteString(string(runes[index:end]))
			index = end - 1
			hasCode = true
			atLineStart = false

			if engine == "mysql" && len(delimiter) == 1 && delimiter[0] == ';' {
				if !inCompoundRoutine && isMySQLRoutinePrefix(current.String()) {
					inCompoundRoutine = true
				}
				if inCompoundRoutine {
					if compoundBlockOpeners[word] {
						compoundDepth++
					} else if word == "END" && compoundDepth > 0 {
						compoundDepth--
					}
				}
			}
			if engine == "oracle" && !oraclePLSQL && isOraclePLSQLPrefix(current.String()) {
				oraclePLSQL = true
			}
			continue
		}

		if char == '\n' {
			atLineStart = true
		} else if !unicode.IsSpace(char) {
			atLineStart = false
			hasCode = true
		}
		current.WriteRune(char)
	}
	flush()
	return statements, nil
}

func classifyStatement(engine string, index int, raw string) (Statement, error) {
	executable := raw
	if engine == "mysql" {
		if unwrapped, ok, err := unwrapStandaloneMySQLExecComment(raw); err != nil {
			return Statement{}, err
		} else if ok {
			executable = unwrapped
		}
	}

	var words []string
	var err error
	if engine == "mysql" {
		words, err = mysqlStatementWords(executable)
		if err != nil {
			return Statement{}, err
		}
	} else {
		words = leadingKeywords(executable, 8)
	}
	if len(words) == 0 {
		return Statement{}, errors.New("dosql_statement_invalid")
	}

	// Reject explicit transaction control everywhere except Oracle PL/SQL BEGIN blocks.
	if transactionKeywords[words[0]] && !(engine == "oracle" && words[0] == "BEGIN") {
		return Statement{}, errors.New("dosql_statement_transaction_control_forbidden: remove BEGIN/COMMIT, DoSql owns the transaction boundary")
	}

	stmt := Statement{
		Index:           index,
		Raw:             raw,
		Executable:      executable,
		LeadingKeywords: words,
		Hash:            StatementHash(executable),
	}

	switch {
	case isSessionStatement(engine, words):
		stmt.Kind = KindSession
	case isNonTransactionalStatement(engine, words):
		stmt.Kind = KindNonTransaction
		stmt.NonTransaction = true
	case commitsImplicitly(engine, words):
		stmt.Kind = KindDDL
		stmt.ImplicitCommit = true
	case isReadOnlyStatement(engine, executable, words):
		stmt.Kind = KindRead
	case dmlKeywords[words[0]] || (words[0] == "WITH" && hasDataModifyingCTE(engine, executable)):
		stmt.Kind = KindDML
	case implicitCommitKeywords[words[0]]:
		stmt.Kind = KindDDL
	default:
		stmt.Kind = KindOther
	}
	return stmt, nil
}

func unwrapStandaloneMySQLExecComment(raw string) (string, bool, error) {
	trimmed := strings.TrimSpace(raw)
	// Strip leading ordinary comments first.
	for {
		switch {
		case strings.HasPrefix(trimmed, "--"), strings.HasPrefix(trimmed, "#"):
			nl := strings.IndexByte(trimmed, '\n')
			if nl < 0 {
				return "", false, nil
			}
			trimmed = strings.TrimSpace(trimmed[nl+1:])
		case strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "/*!") && !strings.HasPrefix(trimmed, "/*M!"):
			end := strings.Index(trimmed, "*/")
			if end < 0 {
				return "", false, errors.New("dosql_statement_unterminated_comment")
			}
			trimmed = strings.TrimSpace(trimmed[end+2:])
		default:
			goto doneStrip
		}
	}
doneStrip:
	if !strings.HasPrefix(trimmed, "/*!") && !strings.HasPrefix(trimmed, "/*M!") {
		return "", false, nil
	}
	end := strings.Index(trimmed, "*/")
	if end < 0 {
		return "", false, errors.New("dosql_statement_unterminated_comment")
	}
	if strings.TrimSpace(trimmed[end+2:]) != "" {
		// Trailing SQL after an executable comment: reject to prevent hidden clauses.
		return "", false, errors.New("dosql_statement_executable_comment_unsupported")
	}
	inner := trimmed[3:end]
	if strings.HasPrefix(trimmed, "/*M!") {
		inner = trimmed[4:end]
	}
	// Strip optional 5-digit MySQL version prefix (e.g. 40101).
	inner = strings.TrimSpace(inner)
	digits := 0
	for digits < len(inner) && inner[digits] >= '0' && inner[digits] <= '9' {
		digits++
	}
	if digits >= 5 {
		inner = strings.TrimSpace(inner[digits:])
	}
	if inner == "" {
		return "", false, errors.New("dosql_statement_executable_comment_unsupported")
	}
	// Only safe standalone session SET statements (mysqldump/Navicat headers) are
	// allowed inside versioned comments; transaction/autocommit or arbitrary SQL
	// inside /*! ... */ remains rejected.
	words, err := mysqlStatementWords(inner)
	if err != nil {
		return "", false, errors.New("dosql_statement_executable_comment_unsupported")
	}
	if !isSessionStatement("mysql", words) {
		return "", false, errors.New("dosql_statement_executable_comment_unsupported")
	}
	return inner, true, nil
}

func isSessionStatement(engine string, words []string) bool {
	if len(words) < 2 {
		return false
	}
	if words[0] == "USE" && (engine == "mysql" || engine == "mssql") {
		return true
	}
	if words[0] != "SET" {
		return false
	}
	if containsString(words[1:], "AUTOCOMMIT") || containsString(words[1:], "TRANSACTION") || words[1] == "PASSWORD" {
		return false
	}
	if engine == "mysql" {
		for _, w := range words[1:] {
			if safeMySQLSessionVars[w] || strings.HasPrefix(w, "OLD_") {
				return true
			}
		}
		return false
	}
	if engine == "postgres" {
		switch words[1] {
		case "SEARCH_PATH", "TIME", "TIMEZONE", "CLIENT_ENCODING", "STANDARD_CONFORMING_STRINGS", "APPLICATION_NAME":
			return true
		}
	}
	return false
}

func isNonTransactionalStatement(engine string, words []string) bool {
	if engine != "postgres" || len(words) == 0 {
		return false
	}
	switch words[0] {
	case "VACUUM", "CLUSTER", "DISCARD":
		return true
	case "CREATE", "DROP":
		if len(words) >= 2 && (words[1] == "DATABASE" || words[1] == "TABLESPACE" || words[1] == "SUBSCRIPTION") {
			return true
		}
		if containsString(words[1:], "CONCURRENTLY") {
			return true
		}
	case "REINDEX":
		if containsString(words[1:], "CONCURRENTLY") || (len(words) >= 2 && (words[1] == "DATABASE" || words[1] == "SYSTEM")) {
			return true
		}
	case "ALTER":
		if len(words) >= 2 && (words[1] == "SYSTEM" || words[1] == "DATABASE") {
			return true
		}
		if len(words) >= 4 && words[1] == "TYPE" && containsString(words[2:], "ADD") && containsString(words[2:], "VALUE") {
			return true
		}
	}
	return false
}

func EngineCommitsDDLImplicitly(engine string) bool {
	switch engine {
	case "postgres", "mssql", "mongodb":
		return false
	}
	return true
}

func commitsImplicitly(engine string, words []string) bool {
	if !EngineCommitsDDLImplicitly(engine) || len(words) == 0 {
		return false
	}
	if engine == "mysql" {
		switch words[0] {
		case "CHECK", "CACHE", "LOAD", "IMPORT", "INSTALL", "UNINSTALL", "BEGIN", "START", "STOP", "CHANGE":
			return true
		case "RESET":
			return len(words) < 2 || words[1] != "PERSIST"
		case "SET":
			return (len(words) > 1 && words[1] == "PASSWORD") || containsString(words[1:], "AUTOCOMMIT")
		}
	}
	return implicitCommitKeywords[words[0]]
}

func isReadOnlyStatement(engine, statement string, words []string) bool {
	if len(words) == 0 || !readKeywords[words[0]] {
		return false
	}
	if engine == "mysql" {
		for i := 1; i < len(words); i++ {
			if words[i-1] == "INTO" && (words[i] == "OUTFILE" || words[i] == "DUMPFILE") {
				return false
			}
		}
	}
	if hasDataModifyingCTE(engine, statement) {
		return false
	}
	return true
}

func hasDataModifyingCTE(engine, statement string) bool {
	unquoted := stripQuotesAndComments(engine, statement)
	tokens := identTokenRe.FindAllString(strings.ToUpper(unquoted), -1)
	for _, tok := range tokens {
		switch tok {
		case "INSERT", "UPDATE", "DELETE", "MERGE", "REPLACE", "UPSERT", "INTO", "NEXTVAL", "SETVAL":
			return true
		}
	}
	return false
}

func mysqlStatementWords(statement string) ([]string, error) {
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
			end := closingQuote(runes, i, runes[i], runes[i] != '`')
			if end < 0 {
				return nil, errors.New("dosql_statement_unterminated_quote")
			}
			if runes[i] == '`' || runes[i] == '"' {
				words = append(words, strings.ToUpper(string(runes[i+1:end])))
			}
			i = end + 1
		case isIdentRune(runes[i]):
			start := i
			for i < len(runes) && isIdentRune(runes[i]) {
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
	if transactionKeywords[words[0]] || words[0] == "XA" ||
		(words[0] == "SET" && (containsString(words[1:], "AUTOCOMMIT") || containsString(words[1:], "TRANSACTION"))) {
		return words, errors.New("dosql_statement_transaction_control_forbidden: DoSql owns the transaction and autocommit settings")
	}
	return words, nil
}

func leadingKeywords(statement string, limit int) []string {
	remaining := strings.TrimSpace(statement)
	for {
		switch {
		case strings.HasPrefix(remaining, "--"), strings.HasPrefix(remaining, "#"):
			newline := strings.IndexByte(remaining, '\n')
			if newline < 0 {
				return nil
			}
			remaining = strings.TrimSpace(remaining[newline+1:])
		case strings.HasPrefix(remaining, "/*"):
			end := strings.Index(remaining, "*/")
			if end < 0 {
				return nil
			}
			remaining = strings.TrimSpace(remaining[end+2:])
		case strings.HasPrefix(remaining, "("):
			// Support parenthesized queries such as (SELECT 1) UNION (SELECT 2).
			remaining = strings.TrimSpace(remaining[1:])
		default:
			goto extracted
		}
	}
extracted:
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

func stripQuotesAndComments(engine, text string) string {
	runes := []rune(text)
	var out strings.Builder
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			out.WriteByte(' ')
		case c == '#' && engine == "mysql":
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			out.WriteByte(' ')
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			end := strings.Index(string(runes[i+2:]), "*/")
			if end < 0 {
				return out.String()
			}
			i += len([]rune(string(runes[i+2:])[:end])) + 3
			out.WriteByte(' ')
		case c == '\'' || c == '"' || (c == '`' && engine == "mysql"):
			backslash := engine == "mysql" && c != '`' ||
				engine == "postgres" && c == '\'' && i > 0 && (runes[i-1] == 'E' || runes[i-1] == 'e')
			end := closingQuote(runes, i, c, backslash)
			if end < 0 {
				return out.String()
			}
			i = end
			out.WriteByte(' ')
		case c == '$' && engine == "postgres" && (i == 0 || !isIdentRune(runes[i-1])):
			if tag := dollarQuoteTag.FindString(string(runes[i:])); tag != "" {
				body := string(runes[i+len([]rune(tag)):])
				if end := strings.Index(body, tag); end >= 0 {
					i += len([]rune(tag)) + len([]rune(body[:end])) + len([]rune(tag)) - 1
					out.WriteByte(' ')
					continue
				}
			}
			out.WriteRune(c)
		default:
			out.WriteRune(c)
		}
	}
	return out.String()
}

func isMySQLRoutinePrefix(text string) bool {
	words := leadingKeywords(text, 6)
	if len(words) < 2 || (words[0] != "CREATE" && words[0] != "ALTER") {
		return false
	}
	for _, w := range words[1:] {
		switch w {
		case "PROCEDURE", "FUNCTION", "TRIGGER", "EVENT":
			return true
		}
	}
	return false
}

func isOraclePLSQLPrefix(text string) bool {
	words := leadingKeywords(text, 5)
	if len(words) == 0 {
		return false
	}
	if words[0] == "BEGIN" || words[0] == "DECLARE" {
		return true
	}
	if words[0] == "CREATE" {
		for _, w := range words[1:] {
			switch w {
			case "PROCEDURE", "FUNCTION", "PACKAGE", "TRIGGER", "TYPE":
				return true
			}
		}
	}
	return false
}

func closingQuote(runes []rune, start int, quote rune, backslash bool) int {
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

func runesMatchAt(runes []rune, pos int, pattern []rune) bool {
	if pos+len(pattern) > len(runes) {
		return false
	}
	for i, r := range pattern {
		if runes[pos+i] != r {
			return false
		}
	}
	return true
}

func isIdentRune(char rune) bool {
	return char == '_' || unicode.IsLetter(char) || unicode.IsDigit(char)
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
