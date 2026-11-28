package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The MySQL openers, the hub zero-value fallback block and the audit purge
// backoff used to spell their budget as a literal. These tests pin the table as
// the only source: a non-default table value must change what the process does,
// and the literals must not come back.

// silentMySQLListener accepts connections and never sends the server greeting,
// so a driver ping can only end on its own deadline.
func silentMySQLListener(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address %v", listener.Addr())
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				<-done
				conn.Close()
			}()
		}
	}()
	return uint16(addr.Port)
}

func TestMySQLPingBudgetComesFromTheTable(t *testing.T) {
	port := silentMySQLListener(t)
	// 50ms stands in for an operator who raised DOOPS_MYSQL_CONNECT_TIMEOUT: the
	// old 10s literal would still cut the ping at 10s, this value must win.
	withTimeoutField(t, func(d *timeoutDefaults) { d.MySQLConnectTimeout = 50 * time.Millisecond })
	cfg := gatewayMySQLConfig{Host: "127.0.0.1", Port: port, User: "probe", Password: "probe", Database: "probe"}
	start := time.Now()
	db, err := openGatewayMySQL(context.Background(), cfg)
	elapsed := time.Since(start)
	if db != nil {
		db.Close()
		t.Fatal("a silent MySQL listener must not yield a usable connection")
	}
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the ping ran %s: the opener ignored the 50ms table budget and still used the old 10s literal", elapsed)
	}
	t.Logf("ping with a 50ms table budget failed after %s: %v", elapsed, err)
}

func TestMySQLPingBudgetFallsBackWhenTheTableIsZero(t *testing.T) {
	withTimeoutField(t, func(d *timeoutDefaults) { d.MySQLConnectTimeout = 0 })
	if got, want := mysqlPingTimeout(), defaultTimeouts().MySQLConnectTimeout; got != want {
		t.Fatalf("a zeroed table value must fall back to the table default %s, got %s", want, got)
	}
}

func TestHubZeroOptionsTakeBudgetsFromTheTable(t *testing.T) {
	// The environment is the path an operator actually uses; loadTimeouts is the
	// same parser the process start-up uses.
	t.Setenv("DOOPS_OPERATION_TIMEOUT", "7m")
	t.Setenv("DOOPS_DETACHED_TASK_TIMEOUT", "3h")
	t.Setenv("DOOPS_TARGET_RECONNECT_GRACE", "17s")
	loaded, err := loadTimeouts(os.Getenv)
	if err != nil {
		t.Fatalf("load timeouts: %v", err)
	}
	withTimeoutField(t, func(d *timeoutDefaults) { *d = loaded })
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	hub := NewGatewayHub(store, GatewayHubOptions{})
	if got := hub.opts.OperationTimeout; got != 7*time.Minute {
		t.Fatalf("hub operation timeout = %s, want the 7m table value", got)
	}
	if got := hub.opts.DetachedTaskTimeout; got != 3*time.Hour {
		t.Fatalf("hub detached task timeout = %s, want the 3h table value", got)
	}
	if got := hub.opts.TargetReconnectGrace; got != 17*time.Second {
		t.Fatalf("hub reconnect grace = %s, want the 17s table value", got)
	}
}

// TestWiredTimeoutSitesKeepNoHandWrittenLiteral is the structural half: even a
// passing behaviour test only covers the site it drives, so every site that was
// moved onto the table is scanned for the literal it used to carry.
func TestWiredTimeoutSitesKeepNoHandWrittenLiteral(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	dir := filepath.Dir(file)
	// Needles are whitespace-free: gofmt writes "10 * time.Second" but hand
	// edits write "10*time.Second", and only the expression matters.
	forbidden := map[string][]string{
		"gateway_store_mysql.go":         {"10*time.Second"},
		"gateway_store_mysql_runtime.go": {"10*time.Second"},
		"gateway_mysql_copy_command.go":  {"10*time.Second"},
		"audit_scrub.go":                 {"10*time.Second"},
		"tunnel_hub.go":                  {"10*time.Second", "60*time.Minute", "24*time.Hour"},
		"audit_purge.go":                 {"50*time.Millisecond"},
	}
	if len(forbidden) != 6 {
		t.Fatalf("scan set shrank to %d files; it must cover all six wired sites", len(forbidden))
	}
	for name, needles := range forbidden {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := strings.Split(string(src), string(rune(10)))
		for i, line := range lines {
			compact := strings.NewReplacer(" ", "", string(rune(9)), "").Replace(line)
			for _, needle := range needles {
				if strings.Contains(compact, needle) {
					t.Errorf("%s:%d still spells the budget as %q; it must read the timeouts table", name, i+1, needle)
				}
			}
		}
	}
}
