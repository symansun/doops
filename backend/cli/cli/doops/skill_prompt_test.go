package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
)

func TestDomainCLIsShareGatewayLoginAndPropagateFailure(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rpc" || r.URL.Query().Get("cluster") != "test" || r.URL.Query().Get("instance") != "node" {
			t.Errorf("domain CLI changed Gateway route: %s", r.URL)
			http.Error(w, "wrong route", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer shared-synthetic-login" {
			http.Error(w, "missing shared login", http.StatusUnauthorized)
			return
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			var request map[string]interface{}
			if err := connection.ReadJSON(&request); err != nil {
				return
			}
			if request["method"] == "initialize" {
				_ = connection.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": request["id"], "result": map[string]interface{}{"protocolVersion": "2024-11-05"}})
				continue
			}
			params, _ := request["params"].(map[string]interface{})
			arguments, _ := params["arguments"].(map[string]interface{})
			if params["name"] != "doops_agent_prompt" || arguments["session_id"] != "domain-functional" {
				t.Errorf("wrong domain RPC: %#v", params)
			}
			skill, _ := arguments["skill"].(string)
			if skill != "doweb" && skill != "dosql" {
				t.Errorf("wrong domain skill: %q", skill)
			}
			if skill == "dosql" && arguments["credential_mode"] != "auto" {
				t.Errorf("sql must use automatic credential mode: %#v", arguments)
			}
			calls.Add(1)
			instruction, _ := arguments["instruction"].(string)
			failed := strings.Contains(instruction, "synthetic-failure")
			text := "domain operation completed"
			if failed {
				text = "synthetic target validation failed"
			}
			_ = connection.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": request["id"], "result": map[string]interface{}{"isError": failed, "content": []map[string]interface{}{{"type": "text", "text": text}}}})
		}
	}))
	defer gateway.Close()
	binary := buildDoopsCLIBinary(t)
	root := t.TempDir()
	t.Setenv("DOOPS_CONFIG", filepath.Join(root, "config.json"))
	if err := saveServers([]Server{{Name: "domain", Gateway: gateway.URL, Cluster: "test", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "shared-synthetic-login")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"web", "sql"} {
		for _, failed := range []bool{false, true} {
			request := "check the requested domain"
			if failed {
				request = "synthetic-failure"
			}
			args := []string{"-session", "domain-functional", domain, "--target", "domain", "--request", request}
			command := exec.Command(binary, args...)
			command.Env = os.Environ()
			output, err := command.CombinedOutput()
			if (err != nil) != failed {
				t.Fatalf("%s failure propagation: %v: %s", domain, err, output)
			}
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("expected one RPC per operation, got %d", calls.Load())
	}
}
