package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/pkg/release"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newGatewayAuthorizedAgentServer(t *testing.T, gateway *Gateway) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		gateway.ServeGatewayWebSocketConnWithReady(conn, r.RemoteAddr, nil)
	}))
}

func interfaceStringValues(values []interface{}) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.(string))
	}
	return result
}

func dialAgentTestWS(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + serverURL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	return conn
}

func initializeAgentTestWS(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var resp map[string]interface{}
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read initialize: %v", err)
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("initialize failed: %#v", resp)
	}
	if strings.TrimSpace(fmt.Sprint(result["runtimeId"])) == "" {
		t.Fatalf("initialize result must identify the agent process: %#v", resp)
	}
	serverInfo, _ := result["serverInfo"].(map[string]interface{})
	releaseCommit, _ := serverInfo["releaseCommit"].(string)
	version, _ := serverInfo["version"].(string)
	if version != release.Version || releaseCommit != release.Commit {
		t.Fatalf("initialize result must expose release version=%q and commit=%q: %#v", release.Version, release.Commit, resp)
	}
	capabilities, _ := result["capabilities"].(map[string]interface{})
	protocols, _ := capabilities["protocols"].(map[string]interface{})
	if !credentialPlanCapabilityCompatible(protocols["credentialPlan"]) {
		t.Fatalf("initialize result must advertise credential plan v2: %#v", resp)
	}
	if !structuredSSHCapabilityCompatible(protocols[structuredSSHProtocol]) {
		t.Fatalf("initialize result must advertise structured SSH: %#v", resp)
	}
	if !toolDeadlineCapabilityCompatible(protocols[toolDeadlineProtocol]) {
		t.Fatalf("initialize result must advertise the tool deadline: %#v", resp)
	}
}

func callTool(t *testing.T, conn *websocket.Conn, tool string, args map[string]interface{}) string {
	t.Helper()
	msg := callToolResult(t, conn, tool, args)
	if errObj, ok := msg["error"]; ok {
		t.Fatalf("%s returned error: %#v", tool, errObj)
	}
	result, _ := msg["result"].(map[string]interface{})
	if isErr, ok := result["isError"]; ok && isErr == true {
		t.Fatalf("%s returned tool error: %#v", tool, result)
	}
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		return ""
	}
	item, _ := content[0].(map[string]interface{})
	text, _ := item["text"].(string)
	return text
}

func callToolResult(t *testing.T, conn *websocket.Conn, tool string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	rawArgs, _ := json.Marshal(args)
	id := float64(len(tool) + len(rawArgs))
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      tool,
			"arguments": json.RawMessage(rawArgs),
		},
	}); err != nil {
		t.Fatalf("call %s: %v", tool, err)
	}
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read %s response: %v", tool, err)
		}
		if _, ok := msg["method"]; ok {
			continue
		}
		return msg
	}
}

func readNotification(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read notification: %v", err)
		}
		if method, _ := msg["method"].(string); method == "notifications/message" {
			return msg
		}
	}
}

func tinyTarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	data := []byte(content)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data))}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write tar data: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}
