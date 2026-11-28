package policy

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/user/doops/agent/internal/dosql/lexer"
)

type Severity string

const (
	SeverityOff   Severity = "off"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// Policy is the project-level Policy-as-Code specification (.dosql/policy.yaml).
// It governs schema standards, DML safety, and zero-downtime release rules.
type Policy struct {
	Schema  string        `yaml:"schema" json:"schema"`
	MySQL   MySQLRules    `yaml:"mysql" json:"mysql"`
	PG      PostgresRules `yaml:"postgres" json:"postgres"`
	DML     DMLRules      `yaml:"dml" json:"dml"`
	Release ReleaseRules  `yaml:"release" json:"release"`
}

type MySQLRules struct {
	RequirePrimaryKey     Severity `yaml:"require_primary_key" json:"require_primary_key"`
	RequireCharsetUTF8MB4 Severity `yaml:"require_charset_utf8mb4" json:"require_charset_utf8mb4"`
	ForbidFloatTypes      Severity `yaml:"forbid_float_types" json:"forbid_float_types"`
}

type PostgresRules struct {
	RequireConcurrentIndex Severity `yaml:"require_concurrent_index" json:"require_concurrent_index"`
}

type DMLRules struct {
	ForbidUnboundedUpdateDelete Severity `yaml:"forbid_unbounded_update_delete" json:"forbid_unbounded_update_delete"`
	ForbidTruncate              Severity `yaml:"forbid_truncate" json:"forbid_truncate"`
}

type ReleaseRules struct {
	ExpandContractGuard Severity `yaml:"expand_contract_guard" json:"expand_contract_guard"`
}

type Finding struct {
	RuleID         string   `json:"rule_id"`
	Severity       Severity `json:"severity"`
	StatementIndex int      `json:"statement_index"`
	Message        string   `json:"message"`
	Overridden     bool     `json:"overridden,omitempty"`
}

type Report struct {
	Passed    bool      `json:"passed"`
	Errors    int       `json:"errors"`
	Warnings  int       `json:"warnings"`
	Findings  []Finding `json:"findings"`
	Overrides []string  `json:"overrides,omitempty"`
}

// Default returns a pragmatic built-in governance policy that catches high-risk
// mistakes (unbounded UPDATE/DELETE) as errors while warning on structural and
// zero-downtime hygiene rules without blocking normal development.
func Default() Policy {
	return Policy{
		Schema: "dosql.policy.v1",
		MySQL: MySQLRules{
			RequirePrimaryKey:     SeverityWarn,
			RequireCharsetUTF8MB4: SeverityWarn,
			ForbidFloatTypes:      SeverityWarn,
		},
		PG: PostgresRules{
			RequireConcurrentIndex: SeverityWarn,
		},
		DML: DMLRules{
			ForbidUnboundedUpdateDelete: SeverityError,
			ForbidTruncate:              SeverityWarn,
		},
		Release: ReleaseRules{
			ExpandContractGuard: SeverityWarn,
		},
	}
}

// LoadFile loads a policy YAML file, merging unspecified rules with Default().
func LoadFile(path string) (Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("dosql_policy_unreadable: %w", err)
	}
	return ParseYAML(raw)
}

func ParseYAML(raw []byte) (Policy, error) {
	p := Default()
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return Policy{}, fmt.Errorf("dosql_policy_invalid: %w", err)
	}
	if p.Schema != "" && p.Schema != "dosql.policy.v1" {
		return Policy{}, fmt.Errorf("dosql_policy_invalid: unsupported schema %q", p.Schema)
	}
	return p, nil
}

var (
	reHasWhere      = regexp.MustCompile(`(?i)\bWHERE\b`)
	reHasPrimaryKey = regexp.MustCompile(`(?i)\bPRIMARY\s+KEY\b`)
	reCharset       = regexp.MustCompile(`(?i)(?:DEFAULT\s+)?(?:CHARACTER\s+SET|CHARSET)\s*=?\s*([A-Za-z0-9_]+)`)
	reFloatType     = regexp.MustCompile(`(?i)\b(?:FLOAT|DOUBLE(?:\s+PRECISION)?)\b`)
	reDropColumn    = regexp.MustCompile(`(?i)\bDROP\s+(?:COLUMN\s+)?`)
	reRenameColumn  = regexp.MustCompile(`(?i)\b(?:RENAME\s+COLUMN|CHANGE\s+(?:COLUMN\s+)?)\b`)
)

// Evaluate checks a parsed statement batch against the policy.
// Any rule listed in overrideRules is downgraded to overridden (recorded for audit).
func Evaluate(engine string, statements []lexer.Statement, p Policy, overrideRules []string) Report {
	engine = strings.ToLower(strings.TrimSpace(engine))
	overrideSet := map[string]bool{}
	for _, r := range overrideRules {
		if r = strings.TrimSpace(r); r != "" {
			overrideSet[r] = true
		}
	}
	report := Report{Passed: true}
	add := func(ruleID string, sev Severity, idx int, msg string) {
		if sev == "" || sev == SeverityOff {
			return
		}
		f := Finding{RuleID: ruleID, Severity: sev, StatementIndex: idx, Message: msg}
		if overrideSet[ruleID] {
			f.Overridden = true
			report.Overrides = appendUnique(report.Overrides, ruleID)
			report.Findings = append(report.Findings, f)
			return
		}
		report.Findings = append(report.Findings, f)
		if sev == SeverityError {
			report.Errors++
			report.Passed = false
		} else if sev == SeverityWarn {
			report.Warnings++
		}
	}

	for _, stmt := range statements {
		if len(stmt.LeadingKeywords) == 0 {
			continue
		}
		first := stmt.LeadingKeywords[0]
		// Strip quoted string literals and comments so tokens like 'where' or
		// 'drop column' inside a value cannot bypass or false-positive rules.
		sqlText := stripSQLLiteralsForPolicy(stmt.Executable)

		// 1. Unbounded UPDATE / DELETE
		if (first == "UPDATE" || first == "DELETE") && !reHasWhere.MatchString(sqlText) {
			add("forbid_unbounded_update_delete", p.DML.ForbidUnboundedUpdateDelete, stmt.Index,
				fmt.Sprintf("%s statement has no WHERE clause; add a predicate or pass --override-rule forbid_unbounded_update_delete --override-reason '...'", first))
		}

		// 2. TRUNCATE
		if first == "TRUNCATE" {
			add("forbid_truncate", p.DML.ForbidTruncate, stmt.Index,
				"TRUNCATE removes all rows and commits implicitly; verify target database and backup")
		}

		// 3. MySQL CREATE TABLE standards
		if engine == "mysql" && first == "CREATE" && containsKeyword(stmt.LeadingKeywords, "TABLE") {
			if !reHasPrimaryKey.MatchString(sqlText) {
				add("require_primary_key", p.MySQL.RequirePrimaryKey, stmt.Index,
					"CREATE TABLE has no PRIMARY KEY; InnoDB tables require an explicit primary key for replication and chunked backfill")
			}
			if m := reCharset.FindStringSubmatch(sqlText); len(m) == 2 && !strings.EqualFold(m[1], "utf8mb4") {
				add("require_charset_utf8mb4", p.MySQL.RequireCharsetUTF8MB4, stmt.Index,
					fmt.Sprintf("table uses charset %s instead of utf8mb4", m[1]))
			}
			if reFloatType.MatchString(sqlText) {
				add("forbid_float_types", p.MySQL.ForbidFloatTypes, stmt.Index,
					"CREATE TABLE uses FLOAT/DOUBLE; prefer DECIMAL for exact numeric/monetary values")
			}
		}

		// 4. PostgreSQL non-concurrent CREATE INDEX
		if engine == "postgres" && first == "CREATE" && containsKeyword(stmt.LeadingKeywords, "INDEX") &&
			!containsKeyword(stmt.LeadingKeywords, "CONCURRENTLY") {
			add("require_concurrent_index", p.PG.RequireConcurrentIndex, stmt.Index,
				"CREATE INDEX without CONCURRENTLY blocks table writes on PostgreSQL; prefer CREATE INDEX CONCURRENTLY")
		}

		// 5. Expand-Contract release guard
		if (first == "DROP" && (containsKeyword(stmt.LeadingKeywords, "TABLE") || containsKeyword(stmt.LeadingKeywords, "DATABASE"))) ||
			(first == "ALTER" && containsKeyword(stmt.LeadingKeywords, "TABLE") && (reDropColumn.MatchString(sqlText) || reRenameColumn.MatchString(sqlText))) {
			add("expand_contract_guard", p.Release.ExpandContractGuard, stmt.Index,
				"destructive DDL (DROP/RENAME) breaks running older app pods during rollout; prefer Expand-Contract (run cleanup in Contract phase)")
		}
	}
	return report
}

func containsKeyword(words []string, target string) bool {
	for _, w := range words {
		if w == target {
			return true
		}
	}
	return false
}

func appendUnique(list []string, item string) []string {
	for _, existing := range list {
		if existing == item {
			return list
		}
	}
	return append(list, item)
}

// stripSQLLiteralsForPolicy blanks out single-quoted string literals and SQL
// comments so keywords inside string values cannot bypass or trigger regex rules.
func stripSQLLiteralsForPolicy(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))
	n := len(sql)
	for i := 0; i < n; i++ {
		ch := sql[i]
		if ch == '\'' {
			b.WriteString("''")
			for i++; i < n; i++ {
				if sql[i] == '\\' && i+1 < n {
					i++
					continue
				}
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i++
						continue
					}
					break
				}
			}
			continue
		}
		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			b.WriteByte(' ')
			for i += 2; i < n && sql[i] != '\n'; i++ {
			}
			continue
		}
		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			b.WriteByte(' ')
			for i += 2; i+1 < n; i++ {
				if sql[i] == '*' && sql[i+1] == '/' {
					i++
					break
				}
			}
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}
