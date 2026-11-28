package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const applyReceiptPrivateCanary = "synthetic-private-rpc-data-canary"

func applyReceiptRPCError(outcome string, missingRaw bool) map[string]interface{} {
	first := map[string]interface{}{
		"tool": "doops_helm", "tool_call_id": "first", "status": "failed", "verified": true,
		"result": map[string]interface{}{
			"isError": true,
			"content": []interface{}{map[string]interface{}{"type": "text", "text": "helm execution interrupted"}},
			"structuredContent": map[string]interface{}{
				"operation": "rollback", "release": "app", "namespace": "apps",
				"phase": "execution", "executionStarted": true, "executionOutcome": "unknown",
			},
		},
	}
	if missingRaw {
		first["verified"], first["reason"] = false, "raw_output_missing"
		delete(first, "result")
	}
	report := map[string]interface{}{
		"kind": "doops.verified-mutation", "verified": !missingRaw, "outcome": outcome,
		"summary": "A later tool call completed; the earlier execution remains unresolved.",
		"mutations": []interface{}{
			first,
			map[string]interface{}{
				"tool": "doops_helm", "tool_call_id": "second", "status": "completed", "verified": true,
				"result": map[string]interface{}{
					"isError": false,
					"content": []interface{}{map[string]interface{}{"type": "text", "text": "rollback completed"}},
				},
			},
		},
	}
	code, message := -32603, "execution_outcome_unknown: unverified_mutation"
	if outcome == "cancelled" {
		code, message = -32007, "operation canceled"
	}
	return map[string]interface{}{
		"code": code, "message": message,
		"data": map[string]interface{}{
			"structuredContent": report,
			"credential":        applyReceiptPrivateCanary,
			"rawOutput":         applyReceiptPrivateCanary,
			"result":            map[string]interface{}{"content": applyReceiptPrivateCanary},
		},
	}
}

func newApplyReceiptGateway(t *testing.T, rpcError map[string]interface{}) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	upgrader := websocket.Upgrader{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			var request map[string]interface{}
			if err := conn.ReadJSON(&request); err != nil {
				return
			}
			switch request["method"] {
			case "initialize":
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0", "id": request["id"],
					"result": map[string]interface{}{"capabilities": map[string]interface{}{}},
				})
			case "tools/call":
				calls.Add(1)
				params, _ := request["params"].(map[string]interface{})
				arguments, _ := params["arguments"].(map[string]interface{})
				if params["name"] != "doops_agent_prompt" || arguments["operation"] != "apply" {
					t.Errorf("unexpected apply request: %#v", request)
				}
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0", "method": "notifications/message",
					"params": map[string]interface{}{
						"request_id": request["id"], "kind": "tool", "data": "[tool:mcp_doops_helm]",
					},
				})
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0", "id": request["id"], "error": rpcError,
				})
			default:
				t.Errorf("unexpected RPC method: %v", request["method"])
			}
		}
	}))
	return gateway, &calls
}

func TestApplyRPCErrorPreservesReceiptForCallers(t *testing.T) {
	responseError := applyReceiptRPCError("unknown", false)
	gateway, calls := newApplyReceiptGateway(t, responseError)
	defer gateway.Close()
	wantData := responseError["data"].(map[string]interface{})
	reportJSON, err := json.Marshal(wantData["structuredContent"])
	if err != nil {
		t.Fatal(err)
	}

	for _, method := range []string{"Call", "CallAndCapture", "CallStructured"} {
		t.Run(method, func(t *testing.T) {
			client := newGatewayTestClient(t, gateway.URL, "apply-receipt")
			defer client.Close()
			arguments := map[string]interface{}{"instruction": "rollback app", "operation": "apply"}
			var callErr error
			output := captureStdout(t, func() {
				switch method {
				case "Call":
					callErr = client.Call("doops_agent_prompt", arguments)
				case "CallAndCapture":
					_, callErr = client.CallAndCapture("doops_agent_prompt", arguments)
				case "CallStructured":
					var result map[string]interface{}
					callErr = client.CallStructured("doops_agent_prompt", arguments, &result)
					if result != nil {
						t.Errorf("RPC error was decoded as a successful result: %#v", result)
					}
				}
			})
			var rpcErr *gatewayRPCError
			if !errors.As(callErr, &rpcErr) || rpcErr.Code != -32603 ||
				rpcErr.Message != responseError["message"] {
				t.Fatalf("RPC error identity was lost: %v", callErr)
			}
			if !jsonEquivalent(rpcErr.Data, wantData) {
				t.Fatalf("RPC error data was lost: %#v", rpcErr.Data)
			}
			if strings.Count(callErr.Error(), string(reportJSON)) != 1 {
				t.Fatalf("public receipt missing or duplicated in error: %v", callErr)
			}
			if strings.Contains(output+callErr.Error(), applyReceiptPrivateCanary) {
				t.Fatal("private RPC data was printed")
			}
		})
	}
	if calls.Load() != 3 {
		t.Fatalf("error calls were replayed: %d", calls.Load())
	}
}

func TestApplyRPCErrorCLI(t *testing.T) {
	binary := buildDoopsCLIBinary(t)
	for _, scenario := range []struct {
		name, outcome string
		missingRaw    bool
		reportKind    string
	}{
		{"unknown survives later success", "unknown", false, "doops.verified-mutation"},
		{"missing raw output stays unverified", "unknown", true, "doops.verified-mutation"},
		{"cancellation stays cancelled", "cancelled", false, "doops.verified-mutation"},
		{"other structured data stays private", "unknown", false, "private.report"},
		{"unstructured RPC data stays private", "unknown", false, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			responseError := applyReceiptRPCError(scenario.outcome, scenario.missingRaw)
			data := responseError["data"].(map[string]interface{})
			report := data["structuredContent"].(map[string]interface{})
			report["kind"] = scenario.reportKind
			if scenario.reportKind != "doops.verified-mutation" {
				report["payload"] = applyReceiptPrivateCanary
			}
			if scenario.reportKind == "" {
				delete(data, "structuredContent")
			}
			gateway, calls := newApplyReceiptGateway(t, responseError)
			defer gateway.Close()
			t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "test", Instance: "node"}}); err != nil {
				t.Fatal(err)
			}
			auth := &AuthStore{}
			auth.SetGateway(gateway.URL, "synthetic-apply-auth-canary")
			if err := auth.Save(); err != nil {
				t.Fatal(err)
			}

			for _, verbose := range []bool{false, true} {
				args := []string{"-session", "apply-receipt"}
				if verbose {
					args = append(args, "-verbose")
				}
				args = append(args, "ask", "--target", "test", "--apply", "--msg", "rollback app")
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				command := exec.CommandContext(ctx, binary, args...)
				command.Env = os.Environ()
				output, err := command.CombinedOutput()
				cancel()
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
					t.Fatalf("apply must exit 1: err=%v output=%s", err, output)
				}
				text := string(output)
				if !strings.Contains(text, "Error: remote error: "+responseError["message"].(string)) {
					t.Fatalf("original RPC error was lost: %s", output)
				}
				if strings.Contains(text, applyReceiptPrivateCanary) || strings.Contains(text, "synthetic-apply-auth-canary") {
					t.Fatal("CLI printed private data")
				}
				if scenario.reportKind == "doops.verified-mutation" {
					encoded, err := json.Marshal(report)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Count(text, string(encoded)) != 1 {
						t.Fatalf("complete public receipt missing or changed: %s", output)
					}
				} else if strings.Contains(text, `"mutations"`) || strings.Contains(text, scenario.reportKind) && scenario.reportKind != "" {
					t.Fatalf("CLI printed non-public structured data: %s", output)
				}
			}
			if calls.Load() != 2 {
				t.Fatalf("failed apply was replayed: %d calls", calls.Load())
			}
		})
	}
}
