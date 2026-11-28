package server

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestGatewayMySQLBinaryBoots is the INT-33 acceptance test: it builds the real
// gateway binary and starts it against a real MySQL 8.4 server, then requires the
// process to survive ten seconds, answer an HTTP request, and log no panic. Before
// the fix the process died on the first read of edge_route_revision.
func TestGatewayMySQLBinaryBoots(t *testing.T) {
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to run the MySQL gateway boot test")
	}
	if testing.Short() {
		t.Skip("the MySQL gateway boot test builds and runs the gateway binary")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("the go toolchain is not on PATH")
	}

	db := gatewayCopyTestDestination(t)
	var database string
	if err := db.QueryRow("SELECT DATABASE()").Scan(&database); err != nil {
		t.Fatalf("read fixture database name: %v", err)
	}
	dsn := fmt.Sprintf("root@tcp(%s)/%s?parseTime=true", net.JoinHostPort("127.0.0.1", port), database)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate the test source")
	}
	gatewayDir := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "gateway"))
	if _, err := os.Stat(filepath.Join(gatewayDir, "main.go")); err != nil {
		t.Fatalf("gateway source not found at %s: %v", gatewayDir, err)
	}
	tmp := t.TempDir()
	binary := filepath.Join(tmp, "doops-gateway")
	build := exec.Command(goBin, "build", "-o", binary, ".")
	build.Dir = gatewayDir
	build.Env = gatewayTestEnvWithoutGOROOT()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build gateway: %v\n%s", err, output)
	}

	publicPort := gatewayTestFreePort(t)
	dataDir := filepath.Join(tmp, "data")
	gateway := exec.Command(binary,
		"-port", publicPort,
		"-db", filepath.Join(tmp, "gateway.db"),
		"-monitor-tick", "0",
	)
	gateway.Env = append(gatewayTestEnvWithoutGOROOT(),
		"DOOPS_GATEWAY_STORE=mysql",
		"DOOPS_GATEWAY_MYSQL_DSN="+dsn,
		"DOOPS_GATEWAY_DATA_DIR="+dataDir,
	)
	var logs syncLogBuffer
	gateway.Stdout = &logs
	gateway.Stderr = &logs
	startedAt := time.Now()
	if err := gateway.Start(); err != nil {
		t.Fatalf("start the gateway binary: %v", err)
	}
	var stopped bool
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if gateway.Process == nil {
			return
		}
		_ = gateway.Process.Signal(syscall.SIGTERM)
		waited := make(chan error, 1)
		go func() { waited <- gateway.Wait() }()
		select {
		case <-waited:
		case <-time.After(20 * time.Second):
			_ = gateway.Process.Kill()
			<-waited
		}
	}
	t.Cleanup(stop)

	status := gatewayTestWaitForLogin(t, gateway, &logs, publicPort, 45*time.Second)
	if status != http.StatusBadRequest && status != http.StatusUnauthorized && status != http.StatusUnprocessableEntity {
		t.Errorf("POST /v1/auth/login returned %d, want a validation or auth refusal", status)
	}
	if remaining := 10*time.Second - time.Since(startedAt); remaining > 0 {
		time.Sleep(remaining)
	}
	if err := gateway.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the gateway process died within ten seconds: %v\nlogs:\n%s", err, logs.String())
	}
	logText := logs.String()
	if strings.Contains(logText, "panic:") {
		t.Fatalf("the gateway panicked on boot:\n%s", logText)
	}
	for _, want := range []string{
		"backend=mysql address=" + net.JoinHostPort("127.0.0.1", port) + " database=" + database,
		"data_dir=" + dataDir,
		"database: mysql " + net.JoinHostPort("127.0.0.1", port) + "/" + database,
	} {
		if !strings.Contains(logText, want) {
			t.Errorf("boot log does not contain %q:\n%s", want, logText)
		}
	}
}

func gatewayTestEnvWithoutGOROOT() []string {
	// The shell that runs the tests exports a GOROOT for an older toolchain, which
	// makes the go driver refuse to build; the module has to build with the go on
	// PATH. See the repository AGENTS.md environment note.
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOROOT=") {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func gatewayTestFreePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return fmt.Sprintf("%d", port)
}

func gatewayTestWaitForLogin(t *testing.T, gateway *exec.Cmd, logs *syncLogBuffer, port string, timeout time.Duration) int {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	url := "http://" + net.JoinHostPort("127.0.0.1", port) + "/v1/auth/login"
	var lastErr error
	for time.Now().Before(deadline) {
		if gateway.ProcessState != nil {
			break
		}
		response, err := client.Post(url, "application/json", bytes.NewReader([]byte("{}")))
		if err == nil {
			response.Body.Close()
			return response.StatusCode
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("the gateway never answered %s: %v\nlogs:\n%s", url, lastErr, logs.String())
	return 0
}
