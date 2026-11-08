package server

import (
	"strings"
	"testing"

	"github.com/symansun/doops/backend/agent/api"
)

func TestDoSQLMySQLImplicitCommitCategories(t *testing.T) {
	// MySQL 8.4, 15.3.3: cover each statement family, not only the two
	// originally reproduced examples. These are classification tests.
	categories := map[string][]string{
		"ddl": {
			"ALTER EVENT e", "ALTER FUNCTION f", "ALTER PROCEDURE p", "ALTER SERVER s",
			"ALTER TABLE t", "ALTER TABLESPACE s", "ALTER VIEW v",
			"CREATE DATABASE d", "CREATE EVENT e", "CREATE FUNCTION f", "CREATE INDEX i",
			"CREATE PROCEDURE p", "CREATE ROLE r", "CREATE SERVER s",
			"CREATE SPATIAL REFERENCE SYSTEM 1", "CREATE TABLE t", "CREATE TABLESPACE s",
			"CREATE TRIGGER t", "CREATE VIEW v",
			"DROP DATABASE d", "DROP EVENT e", "DROP FUNCTION f", "DROP INDEX i",
			"DROP PROCEDURE p", "DROP ROLE r", "DROP SERVER s",
			"DROP SPATIAL REFERENCE SYSTEM 1", "DROP TABLE t", "DROP TABLESPACE s",
			"DROP TRIGGER t", "DROP VIEW v", "INSTALL PLUGIN p", "UNINSTALL PLUGIN p",
			"RENAME TABLE t TO u", "TRUNCATE TABLE t",
		},
		"accounts": {
			"ALTER USER u", "CREATE USER u", "DROP USER u", "GRANT SELECT ON t TO u",
			"RENAME USER u TO v", "REVOKE SELECT ON t FROM u", "SET PASSWORD = 'synthetic'",
		},
		"transaction and locking": {
			"BEGIN", "START TRANSACTION", "LOCK TABLES t WRITE", "UNLOCK TABLES",
			"SET autocommit=1", "SET @@SESSION.autocommit=ON",
		},
		"loading": {"LOAD DATA INFILE 'synthetic' INTO TABLE t"},
		"administration": {
			"ANALYZE TABLE t", "CACHE INDEX t IN DEFAULT", "CHECK TABLE t", "FLUSH TABLES",
			"LOAD INDEX INTO CACHE t", "OPTIMIZE TABLE t", "REPAIR TABLE t",
			"RESET BINARY LOGS AND GTIDS",
		},
		"replication": {
			"START REPLICA", "STOP REPLICA", "RESET REPLICA",
			"CHANGE REPLICATION SOURCE TO SOURCE_HOST='db.test'",
		},
	}
	for category, statements := range categories {
		t.Run(category, func(t *testing.T) {
			for _, statement := range statements {
				if !doSQLCommitsImplicitly("mysql", statement) {
					t.Errorf("missed implicit-commit family: %s", statement)
				}
			}
		})
	}
	for _, statement := range []string{
		"RESET /* ordinary comment */ PERSIST max_connections",
		"SELECT 'CHECK TABLE t'", "UPDATE t SET v=1", "INSERT INTO t VALUES (1)",
		"DELETE FROM t", "REPLACE INTO t VALUES (1)",
	} {
		if doSQLCommitsImplicitly("mysql", statement) {
			t.Errorf("transactional/non-committing statement misclassified: %s", statement)
		}
	}
}

func TestDoSQLMySQLRejectsUnsafeBatchesBeforeOpeningDriver(t *testing.T) {
	for _, statement := range []string{
		"CHECK TABLE t", "CACHE INDEX t IN DEFAULT", "LOAD INDEX INTO CACHE t",
		"ANALYZE TABLE t", "OPTIMIZE TABLE t", "REPAIR TABLE t", "FLUSH TABLES",
		"RESET REPLICA", "STOP REPLICA", "CHANGE REPLICATION SOURCE TO SOURCE_HOST='db.test'",
		"CREATE TEMPORARY TABLE t (id int)", "DROP TEMPORARY TABLE t",
		"LOAD DATA INFILE 'synthetic' INTO TABLE t", "SET PASSWORD = 'synthetic'",
	} {
		t.Run(statement, func(t *testing.T) {
			for _, dry := range []bool{false, true} {
				for _, batch := range [][]string{
					{"UPDATE t SET v=2", statement}, {statement, "UPDATE t SET v=2"},
				} {
					request, err := planDirectDoSQL("mysql", api.AgentPromptParams{
						Operation: "apply", SQLDryRun: dry, SQL: strings.Join(batch, ";"),
					})
					if err != nil {
						t.Fatal(err)
					}
					// An unregistered driver proves rejection happens before
					// sql.Open, without modelling MySQL transaction semantics.
					request.Target = doSQLTarget{Engine: "mysql", Driver: "unregistered_mysql_guard"}
					result, err := executeDoSQLDirect(t.Context(), request)
					category := "dosql_mixed_ddl_dml_unsupported"
					if dry {
						category = "dosql_dry_run_ddl_unsupported"
					}
					if err == nil || !strings.Contains(err.Error(), category) || result.Status != "rejected" ||
						result.Committed || len(result.Statements) != 0 {
						t.Fatalf("dry=%t batch=%q result=%+v error=%v", dry, batch, result, err)
					}
				}
			}
		})
	}
}

func TestDoSQLMySQLRejectsTransactionSettingsAndExecutableComments(t *testing.T) {
	for _, tc := range []struct{ statement, category string }{
		{"SET autocommit=0", "transaction_control_forbidden"},
		{"SET autocommit=1", "transaction_control_forbidden"},
		{"SET SESSION autocommit=ON", "transaction_control_forbidden"},
		{"SET @@local.`autocommit`=1", "transaction_control_forbidden"},
		{"SET sql_mode='', @@SESSION.autocommit=1", "transaction_control_forbidden"},
		{"SET /* comment */ TRANSACTION READ WRITE", "transaction_control_forbidden"},
		{"XA START 'synthetic'", "transaction_control_forbidden"},
		{"/*!80000 SET autocommit=1 */", "executable_comment_unsupported"},
		{"/*M! SET autocommit=1 */", "executable_comment_unsupported"},
		{"CHECK /*!80000 TABLE */ t", "executable_comment_unsupported"},
		{"SELECT 1 /*!80000 INTO OUTFILE '/tmp/synthetic' */", "executable_comment_unsupported"},
	} {
		t.Run(tc.statement, func(t *testing.T) {
			for _, sql := range []string{tc.statement, "UPDATE t SET v=2; " + tc.statement} {
				if _, err := planDirectDoSQL("mysql", api.AgentPromptParams{Operation: "apply", SQL: sql}); err == nil ||
					!strings.Contains(err.Error(), tc.category) {
					t.Fatalf("planner accepted %q: %v", sql, err)
				}
			}
			result, err := executeDoSQLDirect(t.Context(), doSQLDirectRequest{
				Target:     doSQLTarget{Engine: "mysql", Driver: "unregistered_mysql_guard"},
				Statements: []string{"UPDATE t SET v=2", tc.statement},
			})
			if err == nil || !strings.Contains(err.Error(), tc.category) || result.Status != "rejected" {
				t.Fatalf("executor accepted transaction escape: %+v %v", result, err)
			}
		})
	}
}

func TestDoSQLMySQLDryRunSyntaxBoundary(t *testing.T) {
	for _, statement := range []string{
		"SET sql_mode=''", "CALL p()", "PREPARE stmt FROM 'SELECT 1'", "EXECUTE stmt",
		"SELECT 1 INTO OUTFILE '/tmp/synthetic'", "RESET PERSIST max_connections",
	} {
		result, err := executeDoSQLDirect(t.Context(), doSQLDirectRequest{
			Target:     doSQLTarget{Engine: "mysql", Driver: "unregistered_mysql_guard"},
			Statements: []string{"UPDATE t SET v=2", statement}, DryRun: true,
		})
		if err == nil || !strings.Contains(err.Error(), "dosql_dry_run_statement_unsupported") || result.Status != "rejected" {
			t.Fatalf("unsafe dry run accepted: %q %+v %v", statement, result, err)
		}
	}
	for _, statement := range []string{
		"INSERT INTO t VALUES (1)", "UPDATE t SET v=2", "DELETE FROM t", "REPLACE INTO t VALUES (1)",
		"SELECT '/*!80000 SET autocommit=1 */'", "SELECT /* ordinary comment */ 1",
	} {
		words, err := doSQLMySQLStatementWords(statement)
		if err != nil || !doSQLMySQLDryRunSafe(words) {
			t.Fatalf("ordinary DML/read rejected: %s %v", statement, err)
		}
	}
	// Verification is also user SQL and must not escape the dry-run boundary.
	result, err := executeDoSQLDirect(t.Context(), doSQLDirectRequest{
		Target:     doSQLTarget{Engine: "mysql", Driver: "unregistered_mysql_guard"},
		Statements: []string{"UPDATE t SET v=2"}, Verification: "SELECT 1 INTO OUTFILE '/tmp/synthetic'", DryRun: true,
	})
	if err == nil || result.Status != "rejected" {
		t.Fatalf("unsafe verification accepted: %+v %v", result, err)
	}
}
