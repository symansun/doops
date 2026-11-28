package lexer

import (
	"strings"
	"testing"
)

func TestSplitBusinessMigrationCases(t *testing.T) {
	t.Run("mysql mixed ddl and dml auto-segmented", func(t *testing.T) {
		stmts, err := Split("mysql", "ALTER TABLE orders ADD COLUMN status_v2 INT; UPDATE orders SET status_v2 = status WHERE status_v2 IS NULL;")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(stmts) != 2 {
			t.Fatalf("want 2 statements, got %d", len(stmts))
		}
		segs := PlanSegments(stmts)
		if len(segs) != 2 || segs[0].Kind != SegmentImplicitCommit || segs[0].Atomic || segs[1].Kind != SegmentTransaction || !segs[1].Atomic {
			t.Fatalf("unexpected segments: %+v", segs)
		}
	})

	t.Run("mysqldump headers and navicat export", func(t *testing.T) {
		sql := `/*!40101 SET NAMES utf8mb4 */;
SET FOREIGN_KEY_CHECKS = 0;
DROP TABLE IF EXISTS ` + "`t`" + `;
CREATE TABLE ` + "`t`" + ` (id INT PRIMARY KEY);
INSERT INTO ` + "`t`" + ` VALUES (1);
SET FOREIGN_KEY_CHECKS = 1;`
		stmts, err := Split("mysql", sql)
		if err != nil {
			t.Fatalf("mysqldump/navicat rejected: %v", err)
		}
		if len(stmts) != 6 {
			t.Fatalf("want 6 statements, got %d", len(stmts))
		}
		if stmts[0].Kind != KindSession || stmts[1].Kind != KindSession || stmts[5].Kind != KindSession {
			t.Fatalf("expected session classification, got %s %s %s", stmts[0].Kind, stmts[1].Kind, stmts[5].Kind)
		}
		if stmts[2].Kind != KindDDL || stmts[3].Kind != KindDDL || stmts[4].Kind != KindDML {
			t.Fatalf("unexpected ddl/dml kinds: %+v", stmts)
		}
	})

	t.Run("mysql trigger without delimiter and procedure with delimiter", func(t *testing.T) {
		trig := "CREATE TRIGGER trg BEFORE INSERT ON t FOR EACH ROW BEGIN SET NEW.a = 1; SET NEW.b = 2; END;"
		stmts, err := Split("mysql", trig)
		if err != nil || len(stmts) != 1 {
			t.Fatalf("mysql trigger failed: len=%d err=%v", len(stmts), err)
		}
		proc := "DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END$$\nDELIMITER ;\nSELECT 3;"
		stmts, err = Split("mysql", proc)
		if err != nil || len(stmts) != 2 {
			t.Fatalf("mysql delimiter failed: len=%d err=%v", len(stmts), err)
		}
	})

	t.Run("more than 64 statements allowed", func(t *testing.T) {
		sql := strings.Repeat("INSERT INTO t(a) VALUES (1);\n", 120)
		stmts, err := Split("mysql", sql)
		if err != nil || len(stmts) != 120 {
			t.Fatalf("120 inserts failed: len=%d err=%v", len(stmts), err)
		}
	})

	t.Run("postgres concurrently non-transactional and data-modifying CTE", func(t *testing.T) {
		sql := "CREATE INDEX CONCURRENTLY idx_a ON t(a); WITH moved AS (DELETE FROM a RETURNING *) INSERT INTO b SELECT * FROM moved;"
		stmts, err := Split("postgres", sql)
		if err != nil || len(stmts) != 2 {
			t.Fatalf("pg split failed: len=%d err=%v", len(stmts), err)
		}
		if !stmts[0].NonTransaction || stmts[0].Kind != KindNonTransaction {
			t.Fatalf("CONCURRENTLY must be non-transactional: %+v", stmts[0])
		}
		if stmts[1].Kind != KindDML {
			t.Fatalf("data-modifying CTE must be DML, got %s", stmts[1].Kind)
		}
		segs := PlanSegments(stmts)
		if len(segs) != 2 || segs[0].Kind != SegmentNonTransaction || segs[1].Kind != SegmentTransaction {
			t.Fatalf("unexpected pg segments: %+v", segs)
		}
	})

	t.Run("oracle plsql with slash and mssql GO and parenthesized select", func(t *testing.T) {
		stmts, err := Split("oracle", "CREATE OR REPLACE PROCEDURE p AS BEGIN NULL; END;\n/\nSELECT 1 FROM dual;")
		if err != nil || len(stmts) != 2 {
			t.Fatalf("oracle plsql failed: len=%d err=%v", len(stmts), err)
		}
		stmts, err = Split("mssql", "UPDATE t SET a = 1 WHERE id = 1\nGO\nUPDATE t SET b = 2 WHERE id = 2")
		if err != nil || len(stmts) != 2 {
			t.Fatalf("mssql GO failed: len=%d err=%v", len(stmts), err)
		}
		stmts, err = Split("postgres", "(SELECT 1) UNION (SELECT 2)")
		if err != nil || len(stmts) != 1 || stmts[0].Kind != KindRead {
			t.Fatalf("paren select failed: %+v err=%v", stmts, err)
		}
	})

	t.Run("malicious executable comments and transaction control still rejected", func(t *testing.T) {
		for _, bad := range []string{
			"/*!80000 SET autocommit=1 */",
			"/*M! SET autocommit=1 */",
			"CHECK /*!80000 TABLE */ t",
			"SELECT 1 /*!80000 INTO OUTFILE '/tmp/x' */",
			"SET autocommit=0",
			"BEGIN; UPDATE t SET a=1 WHERE id=1; COMMIT;",
		} {
			if _, err := Split("mysql", bad); err == nil {
				t.Fatalf("expected rejection for %q", bad)
			}
		}
	})
}
