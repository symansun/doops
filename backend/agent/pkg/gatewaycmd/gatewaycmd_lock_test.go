package gatewaycmd

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/symansun/doops/backend/agent/internal/server"
)

const gatewayLockHelperEnv = "DOOPS_GATEWAY_LOCK_HELPER"

func TestGatewayServeLocksDatabaseBeforeRecovery(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "gateway.db")
	port := freeGatewayLockTestPort(t)
	first := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, port)...)
	waitGatewayLockTestHealth(t, first, port)
	seedGatewayLockDatabase(t, dbPath)
	before := readGatewayLockState(t, dbPath)
	if before != "online|executing|" {
		t.Fatalf("invalid live database fixture: %s", before)
	}

	dbAlias := filepath.Join(root, "alias.db")
	if err := os.Symlink(dbPath, dbAlias); err != nil {
		t.Fatal(err)
	}
	dirAlias := filepath.Join(root, "alias-dir")
	if err := os.Symlink(filepath.Dir(dbPath), dirAlias); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path string
		port int
	}{
		{"same-port", dbPath, port},
		{"different-port", dbPath, freeGatewayLockTestPort(t)},
		{"relative-path", relative, freeGatewayLockTestPort(t)},
		{"database-symlink", dbAlias, freeGatewayLockTestPort(t)},
		{"directory-symlink", filepath.Join(dirAlias, "gateway.db"), freeGatewayLockTestPort(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := gatewayLockDatabaseSnapshot(t, dbPath)
			second := startGatewayLockHelper(t, gatewayServeTestArgs(tc.path, tc.port)...)
			assertGatewayLockRejected(t, second)
			if after := gatewayLockDatabaseSnapshot(t, dbPath); !reflect.DeepEqual(snapshot, after) {
				t.Fatal("rejected gateway modified database or WAL bytes")
			}
			if after := readGatewayLockState(t, dbPath); before != after {
				t.Fatalf("second gateway changed the database: before=%s after=%s", before, after)
			}
			waitGatewayLockTestHealth(t, first, port)
		})
	}

	// Management commands must not contend for the serve-only ownership lock.
	admin := startGatewayLockHelper(t, "audit", "list", "-db", dbPath)
	if err := admin.wait(t); err != nil {
		t.Fatalf("audit list while serving: %v\n%s", err, admin.output.String())
	}
	if !strings.Contains(admin.output.String(), "status=executing") {
		t.Fatalf("management command lost live audit state: %s", admin.output.String())
	}
	if after := readGatewayLockState(t, dbPath); before != after {
		t.Fatalf("management command changed runtime state: %s", after)
	}

	lockPath, err := canonicalGatewayDBPath(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.Stat(lockPath + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	first.stop(t)
	restarted := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, port)...)
	waitGatewayLockTestHealth(t, restarted, port)
	if after := readGatewayLockState(t, dbPath); !strings.HasPrefix(after, "offline|outcome_unknown|gateway restarted") {
		t.Fatalf("legitimate restart did not recover interrupted state: %s", after)
	}
	lockAfter, err := os.Stat(lockPath + ".lock")
	if err != nil || !os.SameFile(lockBefore, lockAfter) {
		t.Fatalf("serve lock inode was removed or replaced: %v", err)
	}
}

func TestGatewayServeLockPrecedesDatabaseCreation(t *testing.T) {
	dbPath, err := canonicalGatewayDBPath(filepath.Join(t.TempDir(), "nested", "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := acquireGatewayDBLock(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	child := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, freeGatewayLockTestPort(t))...)
	assertGatewayLockRejected(t, child)
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("contender touched database before owning lock: %s, %v", path, err)
		}
	}
}

func TestGatewayServeLockFailureDoesNotOpenDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	if err := os.Mkdir(dbPath+".lock", 0700); err != nil {
		t.Fatal(err)
	}
	child := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, freeGatewayLockTestPort(t))...)
	if err := child.wait(t); err == nil || !strings.Contains(child.output.String(), "open gateway database lock") {
		t.Fatalf("lock failure not reported: %v\n%s", err, child.output.String())
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database created despite lock failure: %v", err)
	}
}

func TestGatewayServeLockReleasedAfterListenFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	child := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, port)...)
	if err := child.wait(t); err == nil || !strings.Contains(child.output.String(), "address already in use") {
		t.Fatalf("expected listen failure: %v\n%s", err, child.output.String())
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, port)...)
	waitGatewayLockTestHealth(t, restarted, port)
}

func TestGatewayServeDifferentDatabasesDoNotConflict(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"first.db", "second.db"} {
		port := freeGatewayLockTestPort(t)
		child := startGatewayLockHelper(t, gatewayServeTestArgs(filepath.Join(root, name), port)...)
		waitGatewayLockTestHealth(t, child, port)
	}
}

func TestGatewayServeLockHelperProcess(t *testing.T) {
	if os.Getenv(gatewayLockHelperEnv) != "1" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			Run(os.Args[index+1:])
			return
		}
	}
	t.Fatal("missing helper command")
}

func seedGatewayLockDatabase(t *testing.T, path string) {
	t.Helper()
	store, err := server.OpenGatewayStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.MarkAgentOnline(server.AgentStatus{
		Cluster: "cluster", Instance: "instance", Generation: 1,
		ConnectedAt: time.Now().UTC(), LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	id, err := store.StartAudit(server.AuditEvent{
		Cluster: "cluster", Instance: "instance",
		Action: server.ActionExec, Session: "seed", Status: "running",
		StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkExecutionDispatched(id); err != nil {
		t.Fatal(err)
	}
}

type gatewayLockTestProcess struct {
	cmd    *exec.Cmd
	output bytes.Buffer
	done   chan struct{}
	err    error
}

func startGatewayLockHelper(t *testing.T, args ...string) *gatewayLockTestProcess {
	t.Helper()
	child := &gatewayLockTestProcess{done: make(chan struct{})}
	child.cmd = exec.Command(os.Args[0], append([]string{"-test.run=^TestGatewayServeLockHelperProcess$", "--"}, args...)...)
	child.cmd.Env = append(os.Environ(), gatewayLockHelperEnv+"=1")
	child.cmd.Stdout, child.cmd.Stderr = &child.output, &child.output
	if err := child.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		child.err = child.cmd.Wait()
		close(child.done)
	}()
	t.Cleanup(func() { child.stop(t) })
	return child
}

func (child *gatewayLockTestProcess) stop(t *testing.T) {
	t.Helper()
	select {
	case <-child.done:
		return
	default:
	}
	_ = child.cmd.Process.Kill()
	select {
	case <-child.done:
	case <-time.After(5 * time.Second):
		t.Error("gateway test process did not stop")
	}
}

func (child *gatewayLockTestProcess) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-child.done:
		return child.err
	case <-time.After(3 * time.Second):
		child.stop(t)
		t.Fatal("gateway command did not exit promptly")
		return nil
	}
}

func assertGatewayLockRejected(t *testing.T, child *gatewayLockTestProcess) {
	t.Helper()
	err := child.wait(t)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 ||
		!strings.Contains(child.output.String(), "gateway database is already in use") {
		t.Fatalf("expected immediate database ownership rejection: %v\n%s", err, child.output.String())
	}
}

func gatewayServeTestArgs(path string, port int) []string {
	return []string{"serve", "-db", path, "-port", strconv.Itoa(port)}
}

func freeGatewayLockTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitGatewayLockTestHealth(t *testing.T, child *gatewayLockTestProcess, port int) {
	t.Helper()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-child.done:
			t.Fatalf("gateway exited before health: %v\n%s", child.err, child.output.String())
		default:
		}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("gateway health did not become ready")
}

func readGatewayLockState(t *testing.T, path string) string {
	t.Helper()
	dsn := &url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var agentStatus, auditStatus, auditError string
	if err := db.QueryRow(`
		SELECT status FROM agent_status
		WHERE cluster = 'cluster' AND instance = 'instance'
	`).Scan(&agentStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`
		SELECT status, error FROM audit_events
		WHERE session = 'seed'
		ORDER BY id DESC LIMIT 1
	`).Scan(&auditStatus, &auditError); err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{agentStatus, auditStatus, auditError}, "|")
}

func gatewayLockDatabaseSnapshot(t *testing.T, path string) map[string][32]byte {
	t.Helper()
	snapshot := make(map[string][32]byte)
	for _, file := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		snapshot[file] = sha256.Sum256(data)
	}
	return snapshot
}
