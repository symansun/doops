package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectNamesGatewayRPCURLOnHandshakeFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer ts.Close()

	client := newGatewayTestClient(t, ts.URL, "dial-error")
	defer client.Close()
	err := client.connect()
	if err == nil {
		t.Fatal("expected gateway handshake failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "agent WS") {
		t.Fatalf("error still pretends to dial agent WS: %v", err)
	}
	if !strings.Contains(msg, "gateway") || !strings.Contains(msg, "/v1/rpc") {
		t.Fatalf("error must name gateway /v1/rpc, got %v", err)
	}
	if !strings.Contains(msg, ts.URL) && !strings.Contains(msg, strings.TrimPrefix(ts.URL, "http://")) {
		t.Fatalf("error must include the dialed URL, got %v", err)
	}
}

func TestConnectSurfacesRetiredWSGoneBody(t *testing.T) {
	const retired = "inbound /ws has been removed; CLI must use /v1/rpc?cluster=&instance=; agents must use /v1/agent/connect"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(retired))
	}))
	defer ts.Close()

	client := newGatewayTestClient(t, ts.URL, "retired-ws")
	defer client.Close()
	err := client.connect()
	if err == nil {
		t.Fatal("expected 410 handshake failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "agent WS") {
		t.Fatalf("error still pretends to dial agent WS: %v", err)
	}
	if !strings.Contains(msg, "410") {
		t.Fatalf("error must include HTTP 410, got %v", err)
	}
	if !strings.Contains(msg, retired) {
		t.Fatalf("error must include retired /ws body, got %v", err)
	}
}
