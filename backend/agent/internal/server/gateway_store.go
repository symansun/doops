package server

import (
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
	"time"
)

type GatewayStore struct {
	db    *sql.DB
	rdb   *sql.DB
	dbDir string

	// The sensitive filter is file-first so it stays outside the frozen
	// SQLite/MySQL schema contract. The mutex guards the cache and its file.
	sensitiveMu     sync.Mutex
	sensitiveFilter *sensitiveFilterState
}

// readDB returns the read-only SQLite connection pool when open, falling back
// to the write handle if a test constructed GatewayStore directly.
func (s *GatewayStore) readDB() *sql.DB {
	if s.rdb != nil {
		return s.rdb
	}
	return s.db
}

func OpenGatewayStore(path string) (*GatewayStore, error) {
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
	store := &GatewayStore{db: db, dbDir: storeDir}
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

func (s *GatewayStore) ensureColumn(table, column, definition string) error {
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
