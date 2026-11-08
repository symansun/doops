package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/symansun/doops/backend/agent/internal/server"
)

func runEdgeTunnels(rawURL, cluster, instance, token, allowList string, conns, maxConns int, dialTimeout, reconnectDelay time.Duration) {
	if conns <= 0 {
		conns = 1
	}
	for i := 0; i < conns; i++ {
		go maintainEdgeTunnel(rawURL, cluster, instance, token, allowList, maxConns, dialTimeout, reconnectDelay)
	}
}

func maintainEdgeTunnel(rawURL, cluster, instance, token, allowList string, maxConns int, dialTimeout, reconnectDelay time.Duration) {
	if reconnectDelay <= 0 {
		reconnectDelay = time.Second
	}
	backoff := reconnectDelay
	const maxBackoff = 30 * time.Second
	for {
		tunnelURL, err := buildGatewayEdgeURL(rawURL, cluster, instance)
		if err != nil {
			log.Printf("❌ invalid edge gateway URL %q: %v", rawURL, err)
			time.Sleep(backoff)
			continue
		}
		header := http.Header{}
		header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		log.Printf("🌉 connecting edge tunnel to %s (cluster=%s instance=%s)", tunnelURL, cluster, instance)
		conn, resp, err := reverseTunnelDial(tunnelURL, header)
		if err != nil {
			if resp != nil {
				log.Printf("❌ edge tunnel dial failed: %v (HTTP %s)", err, resp.Status)
			} else {
				log.Printf("❌ edge tunnel dial failed: %v", err)
			}
			time.Sleep(backoff)
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = reconnectDelay
		log.Printf("✅ edge tunnel connected: cluster=%s instance=%s", cluster, instance)
		server.ServeEdgeWebSocket(conn, server.EdgeAgentOptions{
			AllowList:   allowList,
			MaxConns:    maxConns,
			DialTimeout: dialTimeout,
		})
		log.Printf("⚠️ edge tunnel disconnected; reconnecting in %s", backoff)
		time.Sleep(backoff)
	}
}

func buildGatewayEdgeURL(rawURL, cluster, instance string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.Contains(rawURL, "://") {
		rawURL = "wss://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if err := enforceSecureGatewayURL(rawURL, u); err != nil {
		return "", err
	}
	path := strings.TrimRight(u.Path, "/")
	switch path {
	case "", "/v1/agent/connect":
		u.Path = "/v1/edge/connect"
	case "/v1/edge/connect":
		u.Path = "/v1/edge/connect"
	default:
		return "", fmt.Errorf("invalid gateway URL path %q: use the doops-gateway base URL or /v1/edge/connect", u.Path)
	}
	q := u.Query()
	q.Set("cluster", cluster)
	q.Set("instance", instance)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
