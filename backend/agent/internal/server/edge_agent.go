package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type EdgeAgentOptions struct {
	AllowList   string
	MaxConns    int
	DialTimeout time.Duration
}

func ServeEdgeWebSocket(conn *websocket.Conn, opts EdgeAgentOptions) {
	allow, err := parseEdgeBackendAllow(opts.AllowList)
	if err != nil {
		_ = conn.Close()
		return
	}
	if opts.MaxConns <= 0 {
		opts.MaxConns = defaultEdgeMaxAgentConns
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = defaultEdgeDialTimeout
	}
	agent := &edgeAgentTunnel{
		ws:          newEdgeWSSession(conn),
		allow:       allow,
		maxConns:    opts.MaxConns,
		dialTimeout: opts.DialTimeout,
		backends:    make(map[uint32]net.Conn),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.ws.writer(ctx)
	go agent.ws.pingLoop(ctx)
	go agent.expireOpening(ctx)
	agent.ws.readLoop(agent.handleFrame)
	cancel()
	agent.closeAll()
	agent.ws.close()
}

type edgeAgentTunnel struct {
	ws          *edgeWSSession
	allow       *edgeBackendAllow
	maxConns    int
	dialTimeout time.Duration
	routes      atomic.Pointer[edgeRouteSnapshot]
	mu          sync.Mutex
	backends    map[uint32]net.Conn
}

func (a *edgeAgentTunnel) expireOpening(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.ws.closed:
			return
		case now := <-ticker.C:
			a.ws.sched.ExpireOpening(now)
		}
	}
}

func (a *edgeAgentTunnel) handleFrame(frame edgeFrame) error {
	switch frame.Type {
	case edgeFrameRoutes:
		return a.applyRoutes(frame.Payload)
	case edgeFrameOpen:
		return a.openBackend(frame)
	case edgeFrameData:
		if _, err := a.ws.sched.HandleRemote(frame); err != nil {
			return err
		}
		if conn := a.lookup(frame.ConnID); conn != nil {
			if _, err := conn.Write(frame.Payload); err != nil {
				_ = a.ws.sched.LocalClose(frame.ConnID, edgeCloseUpstreamFailure, err.Error())
				a.drop(frame.ConnID)
			}
		}
		return nil
	case edgeFrameShutdown:
		action, err := a.ws.sched.HandleRemote(frame)
		if err != nil {
			return err
		}
		if conn := a.lookup(frame.ConnID); conn != nil && action == edgeActionCloseWrite {
			closeWrite(conn)
		}
		if action == edgeActionClose {
			a.drop(frame.ConnID)
		}
		return nil
	case edgeFrameClose:
		action, err := a.ws.sched.HandleRemote(frame)
		if err != nil {
			return err
		}
		if action == edgeActionClose {
			a.drop(frame.ConnID)
		}
		return nil
	default:
		_, err := a.ws.sched.HandleRemote(frame)
		return err
	}
}

func (a *edgeAgentTunnel) applyRoutes(payload []byte) error {
	var wire struct {
		Version int64       `json:"version"`
		Routes  []EdgeRoute `json:"routes"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		return err
	}
	snapshot, err := newEdgeRouteSnapshot(wire.Version, wire.Routes)
	if err != nil {
		return err
	}
	a.routes.Store(snapshot)
	return nil
}

func (a *edgeAgentTunnel) openBackend(frame edgeFrame) error {
	routeID, _, err := decodeEdgeOpenPayload(frame.Payload)
	if err != nil {
		return err
	}
	if _, err := a.ws.sched.Accept(frame.ConnID); err != nil {
		return err
	}
	snapshot := a.routes.Load()
	if snapshot == nil {
		return a.reject(frame.ConnID, edgeCloseRouteForbidden, "no routes")
	}
	route, found := snapshot.byID(routeID)
	if !found || !route.Enabled || route.Backend == "" {
		return a.reject(frame.ConnID, edgeCloseRouteForbidden, "route not authorized")
	}
	if !a.allow.permits(route.Backend) {
		return a.reject(frame.ConnID, edgeCloseRouteForbidden, "backend not allowed")
	}
	if a.liveBackends() >= a.maxConns {
		return a.reject(frame.ConnID, edgeCloseQuotaExceeded, "agent conn quota")
	}
	dialer := net.Dialer{Timeout: a.dialTimeout, KeepAlive: 30 * time.Second}
	backend, err := dialer.Dial("tcp", route.Backend)
	if err != nil {
		reason := edgeCloseBackendRefused
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			reason = edgeCloseBackendTimeout
		}
		return a.reject(frame.ConnID, reason, err.Error())
	}
	enableTCPKeepAlive(backend)
	if err := a.ws.sched.EnqueueControl(edgeFrame{Type: edgeFrameOpenAck, ConnID: frame.ConnID}); err != nil {
		_ = backend.Close()
		return err
	}
	if conn, status := a.ws.sched.Lookup(frame.ConnID); status == edgeIDLive {
		_ = conn.onOpenAck()
	}
	a.attach(frame.ConnID, backend)
	go a.pipeBackend(frame.ConnID, backend)
	return nil
}

func (a *edgeAgentTunnel) reject(id uint32, reason edgeCloseReason, detail string) error {
	_ = a.ws.sched.LocalClose(id, reason, detail)
	return nil
}

func (a *edgeAgentTunnel) pipeBackend(id uint32, conn net.Conn) {
	defer a.drop(id)
	buf := make([]byte, int(edgeFramePayloadBytes()))
	ctx := context.Background()
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if waitErr := a.ws.sched.WaitEnqueue(ctx, id, buf[:n]); waitErr != nil {
				_ = a.ws.sched.LocalClose(id, edgeCloseUpstreamFailure, waitErr.Error())
				return
			}
		}
		if errors.Is(err, io.EOF) {
			_ = a.ws.sched.LocalShutdown(id)
			return
		}
		if err != nil {
			_ = a.ws.sched.LocalClose(id, edgeCloseUpstreamFailure, err.Error())
			return
		}
	}
}

func (a *edgeAgentTunnel) attach(id uint32, conn net.Conn) {
	a.mu.Lock()
	a.backends[id] = conn
	a.mu.Unlock()
}

func (a *edgeAgentTunnel) lookup(id uint32) net.Conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.backends[id]
}

func (a *edgeAgentTunnel) drop(id uint32) {
	a.mu.Lock()
	conn := a.backends[id]
	delete(a.backends, id)
	a.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (a *edgeAgentTunnel) closeAll() {
	a.mu.Lock()
	conns := make([]net.Conn, 0, len(a.backends))
	for id, conn := range a.backends {
		conns = append(conns, conn)
		delete(a.backends, id)
	}
	a.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (a *edgeAgentTunnel) liveBackends() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.backends)
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
