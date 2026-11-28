package gatewaycmd

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// INT-103: the HTTP shutdown used to spend a hand-written 10s literal. The
// budget now comes from the timeout table (server.HTTPShutdownTimeout()), so
// this case pins the helper itself: a connection that refuses to drain must not
// hold the process past its budget.
func TestShutdownHTTPServerHonoursTheBudget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = server.Serve(listener) }()
	done := make(chan struct{})
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			resp.Body.Close()
		}
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never started")
	}
	defer close(release)

	begin := time.Now()
	shutdownHTTPServer(server, 20*time.Millisecond)
	elapsed := time.Since(begin)
	if elapsed > time.Second {
		t.Fatalf("shutdown took %s with a 20ms budget: the helper ignored its budget", elapsed)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		// The in-flight request is still blocked; that is the point of the budget.
	}
	t.Logf("shutdown with a 20ms budget returned after %s while one request was still in flight", elapsed)
}

// TestShutdownHTTPServerWithNoBudgetStillReturns pins the zero case: a zeroed
// budget must not block forever on a live connection.
func TestShutdownHTTPServerWithNoBudgetStillReturns(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
	})}
	go func() { _ = server.Serve(listener) }()
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never started")
	}
	defer close(release)
	begin := time.Now()
	shutdownHTTPServer(server, 0)
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("a zero budget blocked for %s", elapsed)
	}
}
