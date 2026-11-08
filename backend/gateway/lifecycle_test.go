package main

import (
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestGatewayBinaryInstallUpgradeHealthAndEdgeDefaultOff(t *testing.T) {
	bin := buildGatewayBinary(t)
	help, err := exec.Command(bin, "serve", "-h").CombinedOutput()
	if err != nil {
		t.Fatalf("gateway serve -h: %v\n%s", err, help)
	}
	helpText := string(help)
	if !strings.Contains(helpText, "-edge-listen") {
		t.Fatalf("gateway help must document -edge-listen\n%s", helpText)
	}
	if !strings.Contains(helpText, "empty disables") {
		t.Fatalf("gateway help must say empty -edge-listen disables Edge\n%s", helpText)
	}

	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	port := freeTCPPort(t)

	first := startGatewayServe(t, bin, dbPath, port)
	waitGatewayHealth(t, port)
	assertUnauthenticatedTargets(t, port)
	assertEdgeTablesExist(t, dbPath)
	if err := first.Process.Kill(); err != nil {
		t.Fatalf("stop installed gateway: %v", err)
	}
	_, _ = first.Process.Wait()

	_ = startGatewayServe(t, bin, dbPath, port)
	waitGatewayHealth(t, port)
	assertUnauthenticatedTargets(t, port)
	assertEdgeTablesExist(t, dbPath)
}

func buildGatewayBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "doops-gateway")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build doops-gateway: %v\n%s", err, out)
	}
	return bin
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func startGatewayServe(t *testing.T, bin, dbPath string, port int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin, "serve", "-db", dbPath, "-port", strconv.Itoa(port))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func waitGatewayHealth(t *testing.T, port int) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = fmt.Errorf("health status %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("gateway health did not become ready: %v", last)
}

func assertUnauthenticatedTargets(t *testing.T, port int) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/targets", port))
	if err != nil {
		t.Fatalf("GET /v1/targets: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/v1/targets status = %d, want 401", resp.StatusCode)
	}
}

func assertEdgeTablesExist(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	for _, table := range []string{"users", "edge_routes", "edge_enrollments"} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&count); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("missing table %s after gateway start", table)
		}
	}
}
