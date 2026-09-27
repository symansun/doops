package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCreateAndVerifyEdgeEnrollmentBindsClusterInstance(t *testing.T) {
	store := openEdgeTestStore(t)
	created, err := store.CreateEdgeEnrollment("jm", "gw-jm")
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	if created.Plaintext == "" || !strings.HasPrefix(created.Plaintext, "dge_een_") {
		t.Fatalf("plaintext = %q, want a dge_een_ token shown once", created.Plaintext)
	}
	got, err := store.VerifyEdgeEnrollment(created.Plaintext, "jm", "gw-jm")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("id = %s, want %s", got.ID, created.ID)
	}
	if _, err := store.VerifyEdgeEnrollment(created.Plaintext, "jm", "other"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong instance: %v, want unauthorized", err)
	}
	if _, err := store.VerifyEdgeEnrollment("dge_een_een_deadbeef_nope", "jm", "gw-jm"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown token: %v, want unauthorized", err)
	}
	if err := store.RevokeEdgeEnrollment(created.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := store.VerifyEdgeEnrollment(created.Plaintext, "jm", "gw-jm"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token: %v, want unauthorized", err)
	}
}

func TestEdgeConnectRequiresEnrollmentToken(t *testing.T) {
	store := openEdgeTestStore(t)
	created, err := store.CreateEdgeEnrollment("jm", "gw-jm")
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/edge/connect?cluster=jm&instance=gw-jm"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("connect without a token should fail")
	}
	if resp == nil {
		t.Fatal("connect without a token should return an HTTP error response")
	}
	var authError doopsErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&authError); err != nil {
		t.Fatalf("decode edge auth error: %v", err)
	}
	resp.Body.Close()
	if authError.Code != "edge_enrollment_auth_failed" || authError.AuthDomain != "edge_enrollment" {
		t.Fatalf("unexpected edge auth error: %#v", authError)
	}
	if _, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer nope"}}); err == nil {
		t.Fatal("connect with a garbage token should fail")
	}
	wrong := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/edge/connect?cluster=jm&instance=other"
	if _, _, err := websocket.DefaultDialer.Dial(wrong, http.Header{"Authorization": []string{"Bearer " + created.Plaintext}}); err == nil {
		t.Fatal("token bound to gw-jm must not open a session as other")
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer " + created.Plaintext}})
	if err != nil {
		t.Fatalf("authorized edge connect: %v", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for hub.edge.SessionCount("jm", "gw-jm") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if hub.edge.SessionCount("jm", "gw-jm") != 1 {
		t.Fatalf("session count = %d, want 1", hub.edge.SessionCount("jm", "gw-jm"))
	}
}

func TestParseEdgeBackendAllow(t *testing.T) {
	allow, err := parseEdgeBackendAllow("192.168.150.228:443, 10.0.0.0/8:443")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !allow.permits("192.168.150.228:443") {
		t.Fatal("exact backend should be allowed")
	}
	if !allow.permits("10.1.2.3:443") {
		t.Fatal("CIDR backend should be allowed")
	}
	if allow.permits("10.1.2.3:22") {
		t.Fatal("port mismatch must not be allowed")
	}
	if allow.permits("192.168.1.1:443") {
		t.Fatal("outside the exact host and CIDR must not be allowed")
	}
	if _, err := parseEdgeBackendAllow("  "); err == nil {
		t.Fatal("empty allow list must fail closed")
	}
}
