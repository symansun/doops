package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

type EdgeListener struct {
	Addr          net.Addr
	proxyProtocol bool
	hub           *GatewayHub
	ln            net.Listener

	global   atomic.Int64
	mu       sync.Mutex
	perIP    map[string]int
	perRoute map[int64]int
}

func (h *GatewayHub) StartEdgeListener(addr string, proxyProtocol bool) (*EdgeListener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	listener := &EdgeListener{
		Addr:          ln.Addr(),
		proxyProtocol: proxyProtocol,
		hub:           h,
		ln:            ln,
		perIP:         make(map[string]int),
		perRoute:      make(map[int64]int),
	}
	go listener.serve()
	return listener, nil
}

func (l *EdgeListener) Close() error {
	if l == nil || l.ln == nil {
		return nil
	}
	return l.ln.Close()
}

func (l *EdgeListener) serve() {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return
		}
		go l.handle(conn)
	}
}

func (l *EdgeListener) handle(conn net.Conn) {
	defer conn.Close()
	enableTCPKeepAlive(conn)
	_ = conn.SetDeadline(time.Now().Add(edgeHelloTimeout()))

	buf := make([]byte, 0, int(edgeHelloBytes()))
	scratch := make([]byte, 1024)
	var client netip.AddrPort
	proxyDone := !l.proxyProtocol
	for {
		n, err := conn.Read(scratch)
		if n > 0 {
			buf = append(buf, scratch[:n]...)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return
		}
		if !proxyDone {
			addr, consumed, parseErr := parseProxyProtocolHeader(buf)
			if errors.Is(parseErr, errEdgeProxyHeaderIncomplete) {
				if errors.Is(err, io.EOF) {
					return
				}
				continue
			}
			if parseErr != nil {
				return
			}
			client = addr
			buf = buf[consumed:]
			proxyDone = true
		}
		serverName, sniErr := extractTLSServerName(buf)
		if errors.Is(sniErr, errEdgeHelloIncomplete) {
			if errors.Is(err, io.EOF) || len(buf) >= int(edgeHelloBytes()) {
				return
			}
			continue
		}
		if sniErr != nil {
			return
		}
		_ = conn.SetDeadline(time.Time{})
		l.dispatch(conn, buf, serverName, client)
		return
	}
}

func (l *EdgeListener) dispatch(conn net.Conn, hello []byte, serverName string, client netip.AddrPort) {
	snapshot := l.hub.edge.Snapshot()
	if snapshot == nil {
		return
	}
	decision := snapshot.match(serverName)
	if decision.Status != edgeRouteAllowed {
		return
	}
	route := decision.Route
	session := l.hub.edge.Pick(route.Cluster, route.Instance)
	if session == nil {
		return
	}
	clientKey := clientIPKey(client, conn.RemoteAddr())
	if !l.admit(clientKey, route) {
		return
	}
	defer l.release(clientKey, route)

	stream, err := session.ws.sched.Open()
	if err != nil {
		return
	}
	defer session.ws.sched.LocalClose(stream.ID(), edgeCloseNormal, "")
	session.attach(stream.ID(), conn)
	defer session.detach(stream.ID())
	payload, err := encodeEdgeOpenPayload(route.ID, serverName)
	if err != nil {
		return
	}
	if err := session.ws.sched.EnqueueControl(edgeFrame{Type: edgeFrameOpen, ConnID: stream.ID(), Payload: payload}); err != nil {
		return
	}
	ok, err := session.ws.sched.EnqueueData(stream.ID(), hello)
	if err != nil || !ok {
		_ = session.ws.sched.LocalClose(stream.ID(), edgeCloseUpstreamFailure, "hello not queued")
		return
	}
	session.pipePublic(context.Background(), stream.ID(), conn)
}

func (l *EdgeListener) admit(ip string, route EdgeRoute) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global.Load() >= edgeMaxConns() {
		return false
	}
	if int64(l.perIP[ip]) >= edgeMaxConnsPerIP() {
		return false
	}
	if route.MaxConns > 0 && l.perRoute[route.ID] >= route.MaxConns {
		return false
	}
	l.global.Add(1)
	l.perIP[ip]++
	l.perRoute[route.ID]++
	return true
}

func (l *EdgeListener) release(ip string, route EdgeRoute) {
	l.mu.Lock()
	l.global.Add(-1)
	l.perIP[ip]--
	if l.perIP[ip] <= 0 {
		delete(l.perIP, ip)
	}
	l.perRoute[route.ID]--
	if l.perRoute[route.ID] <= 0 {
		delete(l.perRoute, route.ID)
	}
	l.mu.Unlock()
}

func clientIPKey(parsed netip.AddrPort, fallback net.Addr) string {
	if parsed.IsValid() {
		return parsed.Addr().String()
	}
	if tcp, ok := fallback.(*net.TCPAddr); ok {
		return tcp.IP.String()
	}
	host, _, err := net.SplitHostPort(fallback.String())
	if err != nil {
		return fallback.String()
	}
	return host
}

func enableTCPKeepAlive(conn net.Conn) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tcp.SetKeepAlive(true)
	_ = tcp.SetKeepAlivePeriod(30 * time.Second)
}
