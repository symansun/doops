package gatewaycmd

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/gorilla/websocket"
)

func TestEnvFlagEnabledSpellings(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "yes", "on", " on "} {
		t.Setenv("DOOPS_TEST_FLAG", value)
		if !envFlagEnabled("DOOPS_TEST_FLAG") {
			t.Fatalf("%q was not accepted as enabled", value)
		}
	}
	for _, value := range []string{"", "0", "false", "no", "off", "enabled"} {
		t.Setenv("DOOPS_TEST_FLAG", value)
		if envFlagEnabled("DOOPS_TEST_FLAG") {
			t.Fatalf("%q was accepted as enabled", value)
		}
	}
}

func TestGatewayServeOpenRegistrationFromEnvironment(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		t.Setenv("DOOPS_ALLOW_OPEN_AGENT_REGISTRATION", "1")
		port := freeGatewayLockTestPort(t)
		dbPath := filepath.Join(t.TempDir(), "gateway.db")
		child := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, port)...)
		waitGatewayLockTestHealth(t, child, port)

		conn, _, err := websocket.DefaultDialer.Dial(
			fmt.Sprintf("ws://127.0.0.1:%d/v1/agent/connect?cluster=dev&instance=local", port), nil)
		if err != nil {
			t.Fatalf("an unenrolled Agent was rejected although the environment switch is on: %v\n%s", err, child.output.String())
		}
		_ = conn.Close()
	})

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("DOOPS_ALLOW_OPEN_AGENT_REGISTRATION", "")
		port := freeGatewayLockTestPort(t)
		dbPath := filepath.Join(t.TempDir(), "gateway.db")
		child := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, port)...)
		waitGatewayLockTestHealth(t, child, port)

		conn, _, err := websocket.DefaultDialer.Dial(
			fmt.Sprintf("ws://127.0.0.1:%d/v1/agent/connect?cluster=dev&instance=local", port), nil)
		if err == nil {
			_ = conn.Close()
			t.Fatal("an unenrolled Agent was accepted while the environment switch is off")
		}
	})
}
