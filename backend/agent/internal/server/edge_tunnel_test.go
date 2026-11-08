package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestEdgeTunnelRelaysHTTPSWebSocketAndLargePayload(t *testing.T) {
	store := openEdgeTestStore(t)
	enroll, err := store.CreateEdgeEnrollment("jm", "gw-jm")
	if err != nil {
		t.Fatalf("enrollment: %v", err)
	}

	tlsCfg, pool := mustLocalTestCertificate(t)
	backend := startTLSBackend(t, tlsCfg)
	if _, err := store.CreateEdgeRoute(CreateEdgeRouteRequest{
		Hostname: "local.test",
		Cluster:  "jm",
		Instance: "gw-jm",
		Backend:  backend,
	}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute})
	if err := hub.edge.Reload(); err != nil {
		t.Fatalf("reload routes: %v", err)
	}
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	gw := httptest.NewServer(mux)
	t.Cleanup(gw.Close)

	listener, err := hub.StartEdgeListener("127.0.0.1:0", false)
	if err != nil {
		t.Fatalf("start edge listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	agentWS := "ws" + strings.TrimPrefix(gw.URL, "http") + "/v1/edge/connect?cluster=jm&instance=gw-jm"
	agentConn, _, err := websocket.DefaultDialer.Dial(agentWS, http.Header{
		"Authorization": []string{"Bearer " + enroll.Plaintext},
	})
	if err != nil {
		t.Fatalf("agent edge connect: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ServeEdgeWebSocket(agentConn, EdgeAgentOptions{AllowList: backend, DialTimeout: 2 * time.Second})
	}()
	t.Cleanup(func() {
		_ = agentConn.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	for hub.edge.SessionCount("jm", "gw-jm") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if hub.edge.SessionCount("jm", "gw-jm") == 0 {
		t.Fatal("edge session never came online")
	}
	time.Sleep(50 * time.Millisecond)

	client := edgeHTTPClient(t, listener.Addr.String(), pool)
	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = client.Get("https://local.test/health")
		if err == nil {
			break
		}
		if attempt == 2 {
			t.Fatalf("https health: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("health = %d %q, want 200 ok", resp.StatusCode, body)
	}

	payload := bytes.Repeat([]byte("Z"), 4<<20)
	echo, err := client.Post("https://local.test/echo", "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("https echo: %v", err)
	}
	got, err := io.ReadAll(echo.Body)
	_ = echo.Body.Close()
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo length %d, want %d", len(got), len(payload))
	}

	wsDialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			ServerName: "local.test",
			RootCAs:    pool,
		},
		NetDial: func(network, addr string) (net.Conn, error) {
			return net.DialTimeout("tcp", listener.Addr.String(), 2*time.Second)
		},
		HandshakeTimeout: 3 * time.Second,
	}
	wsConn, _, err := wsDialer.Dial("wss://local.test/ws", nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer wsConn.Close()
	if err := wsConn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("websocket write: %v", err)
	}
	_, message, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("websocket read: %v", err)
	}
	if string(message) != "ping" {
		t.Fatalf("websocket message = %q, want ping", message)
	}
}

func edgeHTTPClient(t *testing.T, edgeAddr string, pool *x509.CertPool) *http.Client {
	t.Helper()
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", edgeAddr)
			},
			TLSClientConfig:   &tls.Config{ServerName: "local.test", RootCAs: pool},
			ForceAttemptHTTP2: false,
		},
	}
}

func startTLSBackend(t *testing.T, tlsCfg *tls.Config) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, r.Body)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(messageType, payload); err != nil {
				return
			}
		}
	})
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("listen backend: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = http.Serve(ln, mux) }()
	return ln.Addr().String()
}

func mustLocalTestCertificate(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "local.test"},
		DNSNames:     []string{"local.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{"http/1.1"},
	}, pool
}
