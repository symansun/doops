package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func TestEdgeTunnelHalfCloseLifecycle(t *testing.T) {
	for _, first := range []string{"client", "backend"} {
		t.Run(first, func(t *testing.T) {
			client, public := edgeTCPPair(t)
			backendListener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer backendListener.Close()
			route := EdgeRoute{ID: 1, Hostname: "local.test", Cluster: "test", Instance: "node", Backend: backendListener.Addr().String(), Enabled: true}
			snapshot, err := newEdgeRouteSnapshot(1, []EdgeRoute{route})
			if err != nil {
				t.Fatal(err)
			}
			gateway := &edgeGatewaySession{ws: &edgeWSSession{sched: newEdgeScheduler(edgeSchedulerOptions{}), closed: make(chan struct{})}, streams: make(map[uint32]net.Conn)}
			allow, err := parseEdgeBackendAllow(route.Backend)
			if err != nil {
				t.Fatal(err)
			}
			agent := &edgeAgentTunnel{ws: &edgeWSSession{sched: newEdgeScheduler(edgeSchedulerOptions{}), closed: make(chan struct{})}, allow: allow, maxConns: 1, dialTimeout: time.Second, backends: make(map[uint32]net.Conn)}
			agent.routes.Store(snapshot)
			registry := &EdgeRegistry{sessions: map[string][]*edgeGatewaySession{tunnelKey("test", "node"): {gateway}}}
			registry.routes.Store(snapshot)
			listener := &EdgeListener{hub: &GatewayHub{edge: registry}, perIP: make(map[string]int), perRoute: make(map[int64]int)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			t.Cleanup(func() {
				close(gateway.ws.closed)
				close(agent.ws.closed)
				gateway.closeAll()
				agent.closeAll()
			})
			bridgeErrors := make(chan error, 2)
			bridge := func(sched *edgeScheduler, receive func(edgeFrame) error) {
				for {
					frame, ok := sched.WaitFrame(ctx)
					if !ok {
						return
					}
					if err := receive(frame); err != nil {
						bridgeErrors <- err
						return
					}
				}
			}
			go bridge(gateway.ws.sched, agent.handleFrame)
			go bridge(agent.ws.sched, gateway.handleFrame)
			dispatched := make(chan struct{})
			go func() {
				defer close(dispatched)
				// Match handle's ownership: dispatch must not return on one-sided EOF.
				defer public.Close()
				listener.dispatch(public, []byte("request-prefix"), "local.test", netip.AddrPort{})
			}()
			backendListener.(*net.TCPListener).SetDeadline(time.Now().Add(3 * time.Second))
			backendConn, err := backendListener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer backendConn.Close()
			backend := backendConn.(*net.TCPConn)
			backend.SetDeadline(time.Now().Add(3 * time.Second))
			client.SetDeadline(time.Now().Add(3 * time.Second))
			request := func() {
				t.Helper()
				if _, err := client.Write([]byte("-tail")); err != nil {
					t.Fatal(err)
				}
				if err := client.CloseWrite(); err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(backend)
				if err != nil || string(got) != "request-prefix-tail" {
					t.Fatalf("request = %q, err = %v", got, err)
				}
			}
			response := func() {
				t.Helper()
				if _, err := backend.Write([]byte("delayed response")); err != nil {
					t.Fatal(err)
				}
				if err := backend.CloseWrite(); err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(client)
				if err != nil || string(got) != "delayed response" {
					t.Fatalf("response = %q, err = %v", got, err)
				}
			}
			if first == "client" {
				request()
			} else {
				response()
			}
			if got := listener.global.Load(); got != 1 {
				t.Fatalf("connections after one FIN = %d, want 1", got)
			}
			if first == "client" {
				response()
			} else {
				request()
			}
			select {
			case <-dispatched:
			case err := <-bridgeErrors:
				t.Fatal(err)
			case <-time.After(3 * time.Second):
				t.Fatal("dispatch retained a fully closed stream")
			}
			if got := listener.global.Load(); got != 0 {
				t.Fatalf("connections after both FINs = %d, want 0", got)
			}
			if gateway.ws.sched.LiveCount() != 0 || agent.ws.sched.LiveCount() != 0 {
				t.Fatal("scheduler retained a fully closed stream")
			}
		})
	}
}

func edgeTCPPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return client.(*net.TCPConn), server.(*net.TCPConn)
}

func TestEdgePipeTerminalCleanup(t *testing.T) {
	for _, halfClosed := range []bool{false, true} {
		for _, cause := range []string{"scheduler-close", "tunnel-close", "context-cancel"} {
			t.Run(fmt.Sprintf("halfClosed=%v/%s", halfClosed, cause), func(t *testing.T) {
				client, public := edgeTCPPair(t)
				sched := newEdgeScheduler(edgeSchedulerOptions{})
				stream, err := sched.Open()
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.onOpenAck(); err != nil {
					t.Fatal(err)
				}
				ws := &edgeWSSession{sched: sched, closed: make(chan struct{})}
				gateway := &edgeGatewaySession{ws: ws, streams: make(map[uint32]net.Conn)}
				gateway.attach(stream.ID(), public)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan struct{})
				go func() {
					defer close(done)
					gateway.pipePublic(ctx, stream.ID(), public)
				}()
				if halfClosed {
					if err := client.CloseWrite(); err != nil {
						t.Fatal(err)
					}
					wait, stop := context.WithTimeout(ctx, time.Second)
					frame, ok := sched.WaitFrame(wait)
					stop()
					if !ok || frame.Type != edgeFrameShutdown {
						t.Fatalf("first FIN = %+v, ok = %v", frame, ok)
					}
				}
				switch cause {
				case "scheduler-close":
					if err := sched.LocalClose(stream.ID(), edgeCloseBackendTimeout, "opening timed out"); err != nil {
						t.Fatal(err)
					}
				case "tunnel-close":
					sched.Stop()
					close(ws.closed)
				case "context-cancel":
					cancel()
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("terminal event did not release the TCP reader")
				}
				if gateway.lookup(stream.ID()) != nil || sched.LiveCount() != 0 {
					t.Fatal("terminal event retained stream ownership")
				}
				client.SetReadDeadline(time.Now().Add(time.Second))
				if _, err := client.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("client after close: %v, want EOF", err)
				}
			})
		}
	}
}

func TestEdgeListenerConcurrentGlobalLimitAndRelease(t *testing.T) {
	t.Setenv("DOOPS_EDGE_MAX_CONNS", "1")
	listener := &EdgeListener{perIP: make(map[string]int), perRoute: make(map[int64]int)}
	route := EdgeRoute{ID: 1}
	var started, finished sync.WaitGroup
	accepted := make(chan string, 32)
	listener.mu.Lock()
	for i := 0; i < cap(accepted); i++ {
		started.Add(1)
		finished.Add(1)
		go func(i int) {
			defer finished.Done()
			ip := fmt.Sprintf("192.0.2.%d", i)
			started.Done()
			if listener.admit(ip, route) {
				accepted <- ip
			}
		}(i)
	}
	started.Wait()
	// Hold the admission critical section while concurrent arrivals accumulate.
	time.Sleep(20 * time.Millisecond)
	listener.mu.Unlock()
	finished.Wait()
	close(accepted)
	if got := listener.global.Load(); got != 1 {
		t.Fatalf("global admissions = %d with limit 1", got)
	}
	for ip := range accepted {
		listener.release(ip, route)
	}
	if listener.global.Load() != 0 || len(listener.perIP) != 0 || len(listener.perRoute) != 0 {
		t.Fatal("released connection retained a reservation")
	}
	if !listener.admit("192.0.2.100", route) {
		t.Fatal("released capacity was not reusable")
	}
	listener.release("192.0.2.100", route)
}

func TestEdgeListenerRejectionDoesNotConsumeCapacity(t *testing.T) {
	t.Setenv("DOOPS_EDGE_MAX_CONNS", "3")
	t.Setenv("DOOPS_EDGE_MAX_CONNS_PER_IP", "1")
	listener := &EdgeListener{perIP: make(map[string]int), perRoute: make(map[int64]int)}
	route := EdgeRoute{ID: 1, MaxConns: 1}
	if !listener.admit("192.0.2.1", route) {
		t.Fatal("first connection rejected")
	}
	if listener.admit("192.0.2.1", EdgeRoute{ID: 2}) {
		t.Fatal("per-IP limit was ignored")
	}
	if listener.admit("192.0.2.2", route) {
		t.Fatal("per-route limit was ignored")
	}
	if listener.global.Load() != 1 || len(listener.perIP) != 1 || len(listener.perRoute) != 1 {
		t.Fatal("rejected connections consumed reservations")
	}
	listener.release("192.0.2.1", route)
}
