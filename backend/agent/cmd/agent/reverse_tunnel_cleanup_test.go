package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/internal/server"
)

func TestServeReverseTunnelConnLogsCleanupAfterInFlightWorkEnds(t *testing.T) {
	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)

	previousServe := reverseTunnelServeConn
	previousContinue := reverseTunnelContinue
	defer func() {
		reverseTunnelServeConn = previousServe
		reverseTunnelContinue = previousContinue
	}()

	// The stub tunnel behaves like a connection that disconnected with one
	// in-flight execution: it takes time to cancel and settle that work before
	// returning.
	var served bool
	reverseTunnelServeConn = func(_ *server.Gateway, _ *websocket.Conn, _ string, onReady func()) {
		served = true
		onReady()
		if !isReverseTunnelConnected() {
			t.Error("ready callback did not mark the tunnel connected")
		}
		time.Sleep(250 * time.Millisecond)
	}
	started := time.Now()
	serveReverseTunnelConn(server.NewGateway(), nil, "probe")
	if !served {
		t.Fatal("stub tunnel was not served")
	}
	if isReverseTunnelConnected() {
		t.Fatal("connected flag stayed set after cleanup")
	}
	got := logs.String()
	if !strings.Contains(got, "⚠️ reverse tunnel disconnected; in-flight cleanup completed in ") {
		t.Fatalf("missing cleanup log:\n%s", got)
	}
	if time.Since(started) < 250*time.Millisecond {
		t.Fatalf("cleanup log was not preceded by the cleanup wait")
	}
}
