package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/internal/server"
)

func TestReverseTunnelAuthBackoffDoublesToCap(t *testing.T) {
	got := time.Duration(0)
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
	for i, w := range want {
		got = nextReverseTunnelAuthBackoff(got, time.Second)
		if got != w {
			t.Fatalf("step %d = %s, want %s", i, got, w)
		}
	}
	if got := nextReverseTunnelAuthBackoff(0, 0); got != time.Second {
		t.Fatalf("zero base = %s", got)
	}
	if got := nextReverseTunnelAuthBackoff(0, 2*time.Minute); got != 2*time.Minute {
		t.Fatalf("base above cap = %s", got)
	}
}

func TestReverseTunnelReconnectDelayDoublesWithJitterToCap(t *testing.T) {
	expectedBases := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second, // capped at reverseTunnelMaxBackoff
		30 * time.Second,
	}
	for failures, wantBase := range expectedBases {
		for sample := 0; sample < 20; sample++ {
			got := nextReverseTunnelReconnectDelay(time.Second, failures)
			minWant := wantBase / 2
			maxWant := wantBase + wantBase/2
			if got < minWant || got >= maxWant {
				t.Fatalf("failures=%d got=%s, want in [%s, %s)", failures, got, minWant, maxWant)
			}
		}
	}
}

func TestReverseTunnelStableConnectionResetsBackoff(t *testing.T) {
	origDial := reverseTunnelDial
	origServe := reverseTunnelServeConn
	origContinue := reverseTunnelContinue
	origSleep := reverseTunnelSleep
	origNow := reverseTunnelNow
	t.Cleanup(func() {
		reverseTunnelDial = origDial
		reverseTunnelServeConn = origServe
		reverseTunnelContinue = origContinue
		reverseTunnelSleep = origSleep
		reverseTunnelNow = origNow
	})

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	reverseTunnelNow = func() time.Time { return now }

	attempt := 0
	var sleeps []time.Duration
	reverseTunnelContinue = func() bool { return attempt < 4 }
	reverseTunnelDial = func(urlStr string, header http.Header) (*websocket.Conn, *http.Response, error) {
		attempt++
		return nil, nil, nil
	}
	reverseTunnelServeConn = func(gw *server.Gateway, conn *websocket.Conn, name string, onReady func()) {
		if attempt == 4 {
			// Stay connected for longer than reverseTunnelStableThreshold (1m).
			now = now.Add(2 * time.Minute)
		} else {
			now = now.Add(5 * time.Second)
		}
	}
	reverseTunnelSleep = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}

	runReverseTunnel(nil, "ws://127.0.0.1:42222", "dev", "a1", "", time.Second)

	if len(sleeps) != 4 {
		t.Fatalf("sleeps = %v (len %d), want 4", sleeps, len(sleeps))
	}
	// Attempts 1..3 were short disconnects: failures = 1, 2, 3 -> base 2s, 4s, 8s.
	for i, base := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		if sleeps[i] < base/2 || sleeps[i] >= base+base/2 {
			t.Fatalf("sleep[%d] = %s, want in [%s, %s)", i, sleeps[i], base/2, base+base/2)
		}
	}
	// Attempt 4 stayed connected > 1m: failures reset to 0 -> base 1s -> [0.5s, 1.5s).
	if sleeps[3] < 500*time.Millisecond || sleeps[3] >= 1500*time.Millisecond {
		t.Fatalf("sleep[3] after stable connection = %s, want in [500ms, 1.5s)", sleeps[3])
	}
}
