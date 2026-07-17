package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func newGatewayAuthorizedAgentServer(t *testing.T, gw *Gateway) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		gw.ServeGatewayWebSocketConnWithReady(conn, r.RemoteAddr, nil)
	}))
}

func TestDirectAgentWebSocketRejectsCredentialControlPlaneTools(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	for _, tool := range []string{"doops_credential_materialize", "doops_credential_cleanup"} {
		result := callToolResult(t, conn, tool, map[string]interface{}{
			"session_id": "direct-credential-control-plane",
		})
		toolResult, _ := result["result"].(map[string]interface{})
		if toolResult["isError"] != true ||
			!strings.Contains(fmt.Sprint(toolResult["content"]), "gateway-authorized reverse tunnel") {
			t.Fatalf("%s direct call was not rejected: %#v", tool, result)
		}
	}
}

func TestGatewayAuthorizedWebSocketMaterializesAndCleansCredential(t *testing.T) {
	bin, _ := installCredentialTestCommands(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("DOOPS_TEST_SECRET_TYPE", "Opaque")
	t.Setenv("DOOPS_TEST_SECRET_KEYS", "API_KEY")

	gw := NewGateway("0")
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	materialized := callTool(t, conn, "doops_credential_materialize", map[string]interface{}{
		"session_id":      "credential-ws",
		"credential_id":   "cred_ws",
		"grant_id":        "grant_ws",
		"version_id":      "ver_ws",
		"credential_type": CredentialTypeOpaque,
		"use":             CredentialUseOpaqueSecret,
		"namespace":       "app",
		"required_keys":   []string{"API_KEY"},
		"payload":         map[string]interface{}{"data": map[string]string{"API_KEY": "ws-canary-secret"}},
	})
	if strings.Contains(materialized, "ws-canary-secret") {
		t.Fatalf("materialize result exposed credential payload: %s", materialized)
	}

	removed := callTool(t, conn, "doops_credential_cleanup", map[string]interface{}{
		"session_id":    "credential-ws",
		"credential_id": "cred_ws",
		"grant_id":      "grant_ws",
		"version_id":    "ver_ws",
		"namespace":     "app",
	})
	if removed != `{"status":"removed"}` {
		t.Fatalf("cleanup result = %s", removed)
	}
}
