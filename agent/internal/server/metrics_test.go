package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMetricsRequest(t *testing.T, remoteAddr string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = remoteAddr
	return req
}

func TestMetricsLoopbackAllowed(t *testing.T) {
	t.Setenv("DOOPS_METRICS_TOKEN", "")
	hub := NewGatewayHub(nil, GatewayHubOptions{})
	hub.metrics.recordToolCall("exec", "success")
	hub.metrics.recordToolCall("exec", "success")
	hub.metrics.recordToolCall("read", "forbidden")

	req := newMetricsRequest(t, "127.0.0.1:5555")
	rec := httptest.NewRecorder()
	hub.HandleMetrics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("loopback request rejected: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"doops_gateway_agents_online 0\n",
		"doops_gateway_ws_clients 0\n",
		"doops_gateway_operations_active 0\n",
		`doops_gateway_tool_calls_total{action="exec",status="success"} 2`,
		`doops_gateway_tool_calls_total{action="read",status="forbidden"} 1`,
		"doops_gateway_guardrail_blocked_total 0\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("unexpected content type: %s", ct)
	}
}

func TestMetricsRemoteDeniedWithoutToken(t *testing.T) {
	t.Setenv("DOOPS_METRICS_TOKEN", "")
	hub := NewGatewayHub(nil, GatewayHubOptions{})
	req := newMetricsRequest(t, "203.0.113.10:5555")
	rec := httptest.NewRecorder()
	hub.HandleMetrics(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote request without token, got %d", rec.Code)
	}
}

func TestMetricsRemoteWithToken(t *testing.T) {
	t.Setenv("DOOPS_METRICS_TOKEN", "s3cret")
	hub := NewGatewayHub(nil, GatewayHubOptions{})

	rec := httptest.NewRecorder()
	hub.HandleMetrics(rec, newMetricsRequest(t, "203.0.113.10:5555"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without bearer token, got %d", rec.Code)
	}

	req := newMetricsRequest(t, "203.0.113.10:5555")
	req.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	hub.HandleMetrics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer token, got %d", rec.Code)
	}
}

func TestMetricsGuardrailBlockedCounter(t *testing.T) {
	t.Setenv("DOOPS_METRICS_TOKEN", "")
	hub := NewGatewayHub(nil, GatewayHubOptions{})
	// 模拟 handleGatewayToolCall finishAudit 的埋点路径
	hub.metrics.recordToolCall("exec", "error")
	hub.metrics.guardrailBlocked++

	req := newMetricsRequest(t, "[::1]:5555")
	rec := httptest.NewRecorder()
	hub.HandleMetrics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IPv6 loopback should be allowed, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if !strings.Contains(string(body), "doops_gateway_guardrail_blocked_total 1\n") {
		t.Fatalf("guardrail counter missing:\n%s", body)
	}
}
