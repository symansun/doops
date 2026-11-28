package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type GatewayStore struct {
	db      *sql.DB
	rdb     *sql.DB
	dbDir   string
	dialect gatewaySQLDialect
	backend string
	// dbPath is the SQLite database file, kept for operator-facing logs. It is
	// empty for the MySQL backend, which has no database file.
	dbPath string
	// mysqlAddr and mysqlDatabase are the MySQL endpoint the store opened, kept
	// for startup logs and diagnostics. They never contain a user or
	// password.
	mysqlAddr     string
	mysqlDatabase string
	// previousDialect is the process dialect that was current when this store
	// opened. Close restores it, because the ambient value is a property of the
	// live store: a process that hosts more than one store (tests, offline
	// tooling) would otherwise keep rendering MySQL SQL after the MySQL store is
	// gone, and every later renderer that reads gatewayDialect() would inherit a
	// backend it no longer has.
	previousDialect gatewaySQLDialect
	// mysql57 records whether this MySQL store targets MySQL 5.7 (e.g. Aliyun
	// RDS 5.7.44), so dialect helpers choose 5.7-compatible spellings while
	// keeping MySQL 8.0/8.4 byte-identical.
	mysql57         bool
	previousMySQL57 bool
	// dialectRestored guards the restore so a second Close is a no-op.
	dialectRestored bool

	// webhookWake is set by the Gateway hub after the outbox worker exists.
	// It is called after an event is committed so the worker does not sleep
	// until the next timer when there is already work.
	webhookWake func()

	// The sensitive filter is file-first so it stays outside the frozen
	// SQLite/MySQL schema contract. The mutex guards the cache and its file.
	sensitiveMu     sync.Mutex
	sensitiveFilter *sensitiveFilterState

	// resourceSyncReady caches the positive readiness check for the 11 resource
	// sync tables once verified, avoiding repeated information_schema.tables
	// metadata queries on every 30-second agent heartbeat and manifest read.
	// Only true is cached so dynamic SQLite migrations in tests still flip to
	// ready as soon as the tables appear.
	resourceSyncReady atomic.Bool

	// File-First audit document storage (DOOPS_AUDIT_STORAGE=file).
	// Keeps full audit record JSONL documents and externalized tail text files
	// under <dbDir>/audit/ so the relational database stays a light hot index.
	auditDocMode  string
	auditDocMu    sync.Mutex
	auditInFlight map[int64]AuditEvent
}

// StoreBackend names the backend the store actually opened. Startup logging
// reads it so the MySQL backend never prints the unused SQLite path.
func (s *GatewayStore) StoreBackend() string { return s.backend }

// StoreLocation renders the address the store is using, for operator-facing
// startup logs. The MySQL backend reports host:port/database because the SQLite
// path printed before it pointed at a file the process never opened.
func (s *GatewayStore) StoreLocation() string {
	if s.backend == "mysql" {
		return "mysql " + s.mysqlAddr + "/" + s.mysqlDatabase
	}
	if s.dbPath != "" {
		return s.dbPath
	}
	return s.dbDir
}

// StoreDataDir reports the filesystem root for Skill bundles and File-First
// artifacts. A SQLite store derives it from the database file; a MySQL store
// takes it from DOOPS_GATEWAY_DATA_DIR (D18). Empty means no file root, which
// only a directly constructed test store can produce.
func (s *GatewayStore) StoreDataDir() string { return s.dbDir }

// readDB returns the read-only SQLite connection pool when open, falling back
// to the write handle if a test constructed GatewayStore directly.
func (s *GatewayStore) readDB() *sql.DB {
	if s.rdb != nil {
		return s.rdb
	}
	return s.db
}

// gatewayStoreBackend reads DOOPS_GATEWAY_STORE. An empty value keeps the
// historical SQLite store; anything other than the two supported names is a
// startup error rather than a silent fallback, because a typo must never move
// production data to a backend the operator did not choose.
func gatewayStoreBackend() (string, error) {
	switch backend := strings.TrimSpace(os.Getenv("DOOPS_GATEWAY_STORE")); backend {
	case "", "sqlite":
		return "sqlite", nil
	case "mysql":
		return "mysql", nil
	default:
		return "", fmt.Errorf("gateway_store_invalid: DOOPS_GATEWAY_STORE=%q is not a supported backend (allowed: sqlite, mysql)", backend)
	}
}

// OpenGatewayStore opens the Gateway's one store. The backend is selected by
// DOOPS_GATEWAY_STORE; the default path, file permissions, migration and read
// pool below are the historic SQLite behaviour and stay byte-identical.
func OpenGatewayStore(path string) (*GatewayStore, error) {
	backend, err := gatewayStoreBackend()
	if err != nil {
		return nil, err
	}
	if backend == "mysql" {
		store, err := openGatewayMySQLStore(context.Background())
		if err != nil {
			return nil, err
		}
		if err := store.initAuditStorage(); err != nil {
			_ = store.Close()
			return nil, err
		}
		return store, nil
	}
	// D16: the dialect is process-global, so every open path states it instead of
	// relying on the init-time default. Without this line, a process that opened a
	// MySQL store earlier would keep rendering MySQL SQL here, and the first
	// statement below (_pragma, PRAGMA, CREATE TABLE ... AUTOINCREMENT) would fail
	// on a database that is in fact SQLite.
	gatewaySetDialect(gatewayDialectSQLite)
	gatewaySetMySQL57(false)
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/var/lib/doops-gateway/gateway.db"
	}
	storeDir := filepath.Dir(path)
	_, statErr := os.Stat(storeDir)
	storeDirCreated := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !storeDirCreated {
		return nil, statErr
	}
	if err := os.MkdirAll(storeDir, 0700); err != nil {
		return nil, err
	}
	if storeDirCreated {
		if err := os.Chmod(storeDir, 0700); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	// busy_timeout must be set on the connection before the first statement:
	// switching to WAL takes a lock that a just-closed store may still hold.
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &GatewayStore{db: db, dbDir: storeDir, dbPath: path, dialect: gatewayDialectSQLite, backend: "sqlite"}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	for _, databaseFile := range []string{path, path + "-wal", path + "-shm"} {
		if err := chmodIfExists(databaseFile, 0600); err != nil {
			db.Close()
			return nil, err
		}
	}
	// Separate read-only pool in WAL mode so hot reads (auth, grants, audit
	// listing, enrollment checks) do not queue behind a slow write statement on
	// the single writer connection.
	readDSN := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)",
	}).String()
	rdb, err := sql.Open("sqlite", readDSN)
	if err != nil {
		db.Close()
		return nil, err
	}
	rdb.SetMaxOpenConns(8)
	store.rdb = rdb
	if err := store.initAuditStorage(); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func chmodIfExists(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *GatewayStore) Close() error {
	if s == nil {
		return nil
	}
	s.restoreAmbientDialect()
	var firstErr error
	if s.rdb != nil {
		if err := s.rdb.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// restoreAmbientDialect puts the process dialect back to the value that was
// current when this store opened. The ambient value describes the live store, so
// a closed MySQL store must stop claiming it: otherwise the next renderer that
// reads gatewayDialect() - in a test process that hosts several stores, or in
// offline tooling - renders MySQL SQL against a backend that is no longer there.
// An SQLite store does not restore, because opening it already published the
// SQLite spelling and a process without a MySQL store must keep rendering it.
func (s *GatewayStore) restoreAmbientDialect() {
	if s.backend != "mysql" || s.dialectRestored {
		return
	}
	s.dialectRestored = true
	previous := s.previousDialect
	if previous != gatewayDialectMySQL && previous != gatewayDialectSQLite {
		previous = gatewayDialectSQLite
	}
	gatewaySetDialect(previous)
	gatewaySetMySQL57(s.previousMySQL57)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func trimTail(s string, max int) string {
	if max <= 0 {
		return s
	}
	return utf8Tail(s, max)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

// errGatewaySQLiteOnlyPath marks a call into the SQLite migration path while the store
// is running against MySQL. The signatures already return errors, so misuse fails closed
// with a named contract instead of a 1064 syntax error from PRAGMA or EXPLAIN.
var errGatewaySQLiteOnlyPath = errors.New("gateway_sqlite_only_path")

func gatewaySQLiteOnlyPath(fn string) error {
	return fmt.Errorf("%w: %s runs only in the SQLite migration path", errGatewaySQLiteOnlyPath, fn)
}

func (s *GatewayStore) ensureColumn(table, column, definition string) error {
	if gatewayDialect() == gatewayDialectMySQL {
		return gatewaySQLiteOnlyPath("ensureColumn")
	}
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}
