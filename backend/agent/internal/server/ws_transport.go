package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: checkWebSocketOrigin,
}

// checkWebSocketOrigin decides whether a cross-origin WebSocket upgrade is
// allowed. Non-browser clients (CLI/SDK) typically send no Origin header and
// are always allowed. Browser clients are validated against the request host
// and an optional allowlist (DOOPS_ALLOWED_WS_ORIGINS, comma-separated). A
// fully permissive mode (the old behaviour) can be re-enabled for development
// via DOOPS_ALLOW_ALL_WS_ORIGINS=1.
func checkWebSocketOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// No Origin header: not a browser-enforced request (CLI/SDK/agent).
		return true
	}
	if strings.TrimSpace(os.Getenv("DOOPS_ALLOW_ALL_WS_ORIGINS")) == "1" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host == "" {
		return false
	}
	if originURL.Scheme != "http" && originURL.Scheme != "https" {
		return false
	}
	// Same origin means the same host AND port. Comparing only the hostname
	// would let any page on another port of the same host open the socket.
	if sameWebSocketOrigin(originURL, r.Host) {
		return true
	}
	for _, allowed := range strings.Split(os.Getenv("DOOPS_ALLOWED_WS_ORIGINS"), ",") {
		if allowedWebSocketOrigin(strings.TrimSpace(allowed), originURL) {
			return true
		}
	}
	return false
}

// sameWebSocketOrigin compares scheme-independent host and effective port. The
// scheme is deliberately not compared: TLS usually terminates in front of the
// Gateway, so the request scheme seen here is not the browser's. Cross-port
// origins are rejected; list them explicitly when a deployment needs them.
func sameWebSocketOrigin(origin *url.URL, requestHost string) bool {
	host, port, ok := originHostPort(origin)
	if !ok {
		return false
	}
	requestName, requestPort := splitHostPortDefault(requestHost, defaultPort(origin.Scheme))
	return strings.EqualFold(host, requestName) && port == requestPort
}

// allowedWebSocketOrigin accepts either a full origin ("https://console:8443")
// or the legacy bare host[:port] form, which means the https default port.
func allowedWebSocketOrigin(allowed string, origin *url.URL) bool {
	allowed = strings.TrimSpace(allowed)
	if allowed == "" {
		return false
	}
	if strings.Contains(allowed, "://") {
		parsed, err := url.Parse(allowed)
		if err != nil || parsed.Host == "" {
			return false
		}
		host, port, ok := originHostPort(parsed)
		if !ok || !strings.EqualFold(parsed.Scheme, origin.Scheme) {
			return false
		}
		originName, originPort, ok := originHostPort(origin)
		return ok && strings.EqualFold(host, originName) && port == originPort
	}
	host, port := splitHostPortDefault(allowed, defaultPort("https"))
	originName, originPort, ok := originHostPort(origin)
	return ok && strings.EqualFold(host, originName) && port == originPort
}

func originHostPort(origin *url.URL) (string, string, bool) {
	if origin == nil || origin.Hostname() == "" {
		return "", "", false
	}
	port := origin.Port()
	if port == "" {
		port = defaultPort(origin.Scheme)
	}
	return origin.Hostname(), port, true
}

func splitHostPortDefault(hostport, fallbackPort string) (string, string) {
	if host, port, err := net.SplitHostPort(hostport); err == nil {
		return host, port
	}
	return strings.Trim(hostport, "[]"), fallbackPort
}

func defaultPort(scheme string) string {
	if strings.EqualFold(scheme, "https") {
		return "443"
	}
	return "80"
}

const (
	agentWSPingInterval     = 5 * time.Second
	agentWSPingWriteTimeout = 3 * time.Second
	agentWSDataWriteTimeout = 60 * time.Second
)

// defaultAgentTunnelReadTimeout matches the Gateway's 90-second Agent lease.
// The Agent pongs (refreshing the lease) before this deadline; exceeding it
// ends the tunnel, which still terminates that connection's in-flight calls.
const defaultAgentTunnelReadTimeout = 90 * time.Second

// agentTunnelReadTimeout reads DOOPS_TUNNEL_READ_TIMEOUT on the Agent side.
// The Gateway's read deadline on the same connection is longer than any
// expectable interval between Agent pongs, so a healthy Agent is never cut.
func agentTunnelReadTimeout() time.Duration {
	return positiveEnvDuration("DOOPS_TUNNEL_READ_TIMEOUT", defaultAgentTunnelReadTimeout)
}

// edgeWSReadTimeout keeps the Edge transport's fixed 20-second idle bound.
const edgeWSReadTimeout = 20 * time.Second

type websocketControlWriter interface {
	WriteControl(messageType int, data []byte, deadline time.Time) error
	Close() error
}

type websocketDataWriter interface {
	SetWriteDeadline(deadline time.Time) error
	NextWriter(messageType int) (io.WriteCloser, error)
	Close() error
}

type websocketBinaryWriter = websocketDataWriter

type websocketDataWriteState struct {
	sequence atomic.Uint64
}

func (s *websocketDataWriteState) begin() {
	s.sequence.Add(1)
}

func (s *websocketDataWriteState) end() {
	s.sequence.Add(1)
}

func (s *websocketDataWriteState) overlaps(start uint64) bool {
	end := s.sequence.Load()
	return start%2 == 1 || end%2 == 1 || start != end
}

func writeHeartbeatControl(conn websocketControlWriter, dataWriteState *websocketDataWriteState, messageType int, payload []byte, timeout time.Duration) bool {
	writeSequence := dataWriteState.sequence.Load()
	if err := conn.WriteControl(messageType, payload, time.Now().Add(timeout)); err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) &&
			netErr.Timeout() &&
			netErr.Temporary() &&
			dataWriteState.overlaps(writeSequence) {
			return true
		}
		_ = conn.Close()
		return false
	}
	return true
}

func writeHeartbeatPing(conn websocketControlWriter, dataWriteState *websocketDataWriteState, payload []byte, timeout time.Duration) bool {
	return writeHeartbeatControl(conn, dataWriteState, websocket.PingMessage, payload, timeout)
}

// websocketWriteChunkBytes bounds how much of one message is written before
// the stall deadline is extended. A slow link keeps the tunnel as long as bytes
// keep moving; only a link that makes no progress for the timeout is closed.
const websocketWriteChunkBytes = 32 << 10

func writeWebSocketBinary(conn websocketBinaryWriter, writeMu *sync.Mutex, dataWriteState *websocketDataWriteState, payload []byte, timeout time.Duration) error {
	return writeWebSocketMessage(conn, writeMu, dataWriteState, websocket.BinaryMessage, payload, timeout)
}

func writeWebSocketJSON(conn websocketDataWriter, writeMu *sync.Mutex, dataWriteState *websocketDataWriteState, value interface{}, timeout time.Duration) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeWebSocketMessage(conn, writeMu, dataWriteState, websocket.TextMessage, payload, timeout)
}

// writeWebSocketMessage writes one message in chunks with a stall deadline:
// each chunk gets a fresh deadline, so total duration scales with link speed
// instead of failing every message that takes longer than timeout.
func writeWebSocketMessage(conn websocketDataWriter, writeMu *sync.Mutex, dataWriteState *websocketDataWriteState, messageType int, payload []byte, timeout time.Duration) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	dataWriteState.begin()
	defer dataWriteState.end()
	fail := func(err error) error {
		_ = conn.Close()
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return fail(err)
	}
	writer, err := conn.NextWriter(messageType)
	if err != nil {
		return fail(err)
	}
	for len(payload) > 0 {
		n := len(payload)
		if n > websocketWriteChunkBytes {
			n = websocketWriteChunkBytes
		}
		if _, err := writer.Write(payload[:n]); err != nil {
			return fail(err)
		}
		payload = payload[n:]
		if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return fail(err)
		}
	}
	if err := writer.Close(); err != nil {
		return fail(err)
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		return fail(err)
	}
	return nil
}

func readWebSocketMessage(conn *websocket.Conn, onProgress func()) (int, []byte, error) {
	messageType, reader, err := conn.NextReader()
	if err != nil {
		return 0, nil, err
	}
	var message bytes.Buffer
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			_, _ = message.Write(buffer[:n])
			if onProgress != nil {
				onProgress()
			}
		}
		if readErr == io.EOF {
			return messageType, message.Bytes(), nil
		}
		if readErr != nil {
			return 0, nil, readErr
		}
	}
}
