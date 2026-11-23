package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to the published port of a disposable
// local MySQL 8 container with empty root password. Each case owns a new DB.
func gatewayCopyTestDestination(t *testing.T) *sql.DB {
	t.Helper()
	port := os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT")
	if port == "" {
		t.Skip("requires a disposable local MySQL 8 (DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT)")
	}
	if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
		t.Fatal("invalid local MySQL port")
	}
	cfg := mysql.NewConfig()
	cfg.User, cfg.Net, cfg.Addr = "root", "tcp", net.JoinHostPort("127.0.0.1", port)
	cfg.Collation = "utf8mb4_0900_bin"
	cfg.Timeout = 3 * time.Second
	open := func() *sql.DB {
		connector, err := mysql.NewConnector(cfg)
		if err != nil {
			t.Fatal("invalid local test configuration")
		}
		db := sql.OpenDB(connector)
		t.Cleanup(func() { db.Close() })
		return db
	}
	admin := open()
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	name := "gateway_copy_test_" + hex.EncodeToString(id[:])
	gatewayCopyTestSQL(t, admin, "CREATE DATABASE "+name+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin")
	t.Cleanup(func() { gatewayCopyTestSQL(t, admin, "DROP DATABASE "+name) })
	cfg.DBName = name
	return open()
}

func gatewayCopyTestSQL(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("synthetic fixture SQL failed: %v", err)
		}
	}
}

func gatewayCopyTestSource(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	gatewayCopyTestSQL(t, db, `PRAGMA journal_mode=WAL`,
		`CREATE TABLE z_parent(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, ratio REAL NOT NULL)`,
		`CREATE TABLE a_child(k TEXT NOT NULL COLLATE NOCASE, part INTEGER NOT NULL, parent INTEGER NOT NULL REFERENCES z_parent(id), note TEXT, payload BLOB, PRIMARY KEY(k,part))`,
		`INSERT INTO z_parent VALUES (0,'zero',0.0), (8,'parent',1.25), (99,'deleted',-3.5)`,
		`DELETE FROM z_parent WHERE id=99`,
		`INSERT INTO a_child VALUES ('a',1,0,'汉字🙂' || char(0) || ' ',x'00ff8000'), ('Z',2,8,'',x''), ('Z',1,8,NULL,NULL)`)
	return db
}

func gatewayCopyTestSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	gatewayCopyTestSQL(t, db,
		`CREATE TABLE z_parent(id BIGINT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(64) NOT NULL UNIQUE, ratio DOUBLE NOT NULL) ENGINE=InnoDB`,
		`CREATE TABLE a_child(payload LONGBLOB, note LONGTEXT, parent BIGINT NOT NULL, part INT NOT NULL, k VARCHAR(64) COLLATE utf8mb4_0900_ai_ci NOT NULL, PRIMARY KEY(k,part), FOREIGN KEY(parent) REFERENCES z_parent(id)) ENGINE=InnoDB`)
}

func gatewayCopyTestCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != want {
		t.Fatalf("%s count = %d, want %d (read error: %v)", table, count, want, err)
	}
}

func TestGatewayMySQLCopySnapshot(t *testing.T) {
	ctx := t.Context()
	src := gatewayCopyTestSource(t)
	tx, err := src.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	table := gatewayCopyTable{name: "a_child", columns: []gatewayCopyColumn{{"k", 't'}, {"part", 'i'}, {"parent", 'i'}, {"note", 't'}, {"payload", 'b'}}, key: []string{"k", "part"}}
	n, digest, err := table.stream(ctx, tx, "sqlite", nil)
	if err != nil || n != 3 {
		t.Fatalf("source scan count = %d: %v", n, err)
	}
	gatewayCopyTestSQL(t, src, `UPDATE a_child SET payload=NULL WHERE payload=x''`)
	_, snapshot, err := table.stream(ctx, tx, "sqlite", nil)
	if err != nil || snapshot != digest {
		t.Fatal("a later WAL commit changed the open read snapshot")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	fresh, err := src.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Rollback()
	n, changed, err := table.stream(ctx, fresh, "sqlite", nil)
	if err != nil || n != 3 || changed == digest {
		t.Fatal("equal-count NULL and empty BLOB were indistinguishable")
	}
}

func TestGatewayMySQLCopyRehearsalAndFinal(t *testing.T) {
	ctx := t.Context()
	src := gatewayCopyTestSource(t)
	rehearsal := gatewayCopyTestDestination(t)
	gatewayCopyTestSchema(t, rehearsal)
	dry, err := copyGatewaySQLiteToMySQL(ctx, src, rehearsal, true)
	if err != nil {
		t.Fatal(err)
	}
	gatewayCopyTestCount(t, rehearsal, "z_parent", 0)
	gatewayCopyTestCount(t, rehearsal, "a_child", 0)
	for _, dryRun := range []bool{true, false} {
		if _, err := copyGatewaySQLiteToMySQL(ctx, src, rehearsal, dryRun); !errors.Is(err, errGatewayCopyStale) {
			t.Fatalf("reused rehearsal allocator accepted: %v", err)
		}
	}
	final := gatewayCopyTestDestination(t)
	gatewayCopyTestSchema(t, final)
	done, err := copyGatewaySQLiteToMySQL(ctx, src, final, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.SHA256 == "" || !reflect.DeepEqual(dry, done) || !reflect.DeepEqual(done.TableRows, map[string]int64{"a_child": 3, "z_parent": 2}) {
		t.Fatal("rehearsal and final report disagree on the same snapshot")
	}
	var matches int
	err = final.QueryRow(`SELECT COUNT(*) FROM a_child WHERE
		(k='a' AND parent=0 AND HEX(note)=? AND HEX(payload)='00FF8000') OR
		(k='Z' AND part=1 AND note IS NULL AND payload IS NULL) OR
		(k='Z' AND part=2 AND OCTET_LENGTH(note)=0 AND OCTET_LENGTH(payload)=0)`, strings.ToUpper(hex.EncodeToString([]byte("汉字🙂\x00 ")))).Scan(&matches)
	if err != nil || matches != 3 {
		t.Fatal("committed bytes, text, NULL, empty values or explicit zero ID differ")
	}
	res, err := final.Exec(`INSERT INTO z_parent(name,ratio) VALUES ('next',2.5)`)
	if err != nil {
		t.Fatal(err)
	}
	if next, err := res.LastInsertId(); err != nil || next != 100 {
		t.Fatalf("generated ID = %d, want 100", next)
	}
	if _, err := copyGatewaySQLiteToMySQL(ctx, src, final, false); !errors.Is(err, errGatewayCopyStale) {
		t.Fatalf("nonempty final target accepted: %v", err)
	}
}

func TestGatewayMySQLCopyFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name         string
		source, dest string
		want         string
	}{
		{"legacy_source_table", `CREATE TABLE archived_accounts(id INTEGER PRIMARY KEY)`, "", "mysql_copy_schema_mismatch"},
		// A table the MySQL schema has never seen, carrying rows: the copier must
		// fail closed on the unknown table instead of copying the rows it can map.
		{"unknown_source_table_with_rows", `CREATE TABLE unknown_metric_points(id INTEGER PRIMARY KEY, value REAL); INSERT INTO unknown_metric_points VALUES (1, 2.5)`, "", "mysql_copy_schema_mismatch"},
		{"extra_destination_table", "", `CREATE TABLE legacy(id BIGINT PRIMARY KEY)`, "mysql_copy_schema_mismatch"},
		{"legacy_column", `ALTER TABLE a_child ADD COLUMN old_state TEXT`, "", "mysql_copy_schema_mismatch"},
		{"blob_mapped_as_text", "", `ALTER TABLE a_child MODIFY payload LONGTEXT`, "mysql_copy_schema_mismatch"},
		{"nontransactional", `CREATE TABLE legacy(id INTEGER PRIMARY KEY)`, `CREATE TABLE legacy(id BIGINT PRIMARY KEY) ENGINE=MyISAM`, "mysql_copy_schema_mismatch"},
		{"orphan_source", `UPDATE a_child SET parent=404`, "", "mysql_copy_source_foreign_key_failed"},
		{"occupied_target", "", `INSERT INTO z_parent VALUES(1,'existing',0)`, "mysql_copy_destination_stale"},
		{"empty_stale_allocator", "", `ALTER TABLE z_parent AUTO_INCREMENT=345`, "mysql_copy_destination_stale"},
		{"storage_class", `UPDATE a_child SET payload='SYNTHETIC_CANARY' WHERE k='a'`, "", "mysql_copy_content_mismatch"},
		{"driver_error_redaction", "", `CREATE TRIGGER rejected BEFORE INSERT ON a_child FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_CANARY'`, "mysql_copy_insert_failed"},
		{"equal_count_corruption", "", `CREATE TRIGGER altered BEFORE INSERT ON a_child FOR EACH ROW SET NEW.note='SYNTHETIC_CANARY'`, "mysql_copy_content_mismatch"},
		{"earlier_table_corruption", "", `CREATE TRIGGER altered AFTER INSERT ON a_child FOR EACH ROW UPDATE z_parent SET name=CONCAT(name,'x') WHERE id=NEW.parent`, "mysql_copy_content_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, src := gatewayCopyTestDestination(t), gatewayCopyTestSource(t)
			gatewayCopyTestSchema(t, db)
			if tc.source != "" {
				gatewayCopyTestSQL(t, src, tc.source)
			}
			if tc.dest != "" {
				gatewayCopyTestSQL(t, db, tc.dest)
			}
			report, err := copyGatewaySQLiteToMySQL(t.Context(), src, db, false)
			if report != nil || err == nil || err.Error() != tc.want {
				t.Fatalf("expected sanitized failure %q, got %v", tc.want, err)
			}
			want := 0
			if tc.name == "occupied_target" {
				want = 1
			}
			gatewayCopyTestCount(t, db, "z_parent", want)
			gatewayCopyTestCount(t, db, "a_child", 0)
		})
	}
}
