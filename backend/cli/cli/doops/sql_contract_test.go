package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestSQLCLIArgumentCombinationsReachTheExpectedRPC(t *testing.T) {
	calls := make(chan map[string]interface{}, 1)
	upgrader := websocket.Upgrader{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rpc" || r.URL.Query().Get("cluster") != "test" ||
			r.URL.Query().Get("instance") != "node" || r.Header.Get("Authorization") != "Bearer sql-contract-login" {
			t.Errorf("unexpected Gateway route or login: %s", r.URL)
			http.Error(w, "wrong route or login", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]interface{}
			if err := conn.ReadJSON(&request); err != nil {
				return
			}
			result := map[string]interface{}{"protocolVersion": "2024-11-05"}
			if request["method"] != "initialize" {
				params := request["params"].(map[string]interface{})
				if params["name"] != "doops_agent_prompt" {
					t.Errorf("unexpected tool: %v", params["name"])
				}
				calls <- params["arguments"].(map[string]interface{})
				result = map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "contract accepted"}}}
			}
			_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": request["id"], "result": result})
		}
	}))
	defer gateway.Close()
	binary := buildDoopsCLIBinary(t)
	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := saveServers([]Server{{Name: "sql-contract", Gateway: gateway.URL, Cluster: "test", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "sql-contract-login")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}
	query := "SELECT status FROM orders WHERE id = 1"
	change := "UPDATE orders SET status = 'paid' WHERE id = 1"
	file := writeSQLFixture(t, "change.sql", change)
	queryFile := writeSQLFixture(t, "query.sql", query)
	migDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(migDir, "V001__init.sql"), []byte(change+";\n"), 0644); err != nil {
		t.Fatal(err)
	}
	migManifest, err := loadCLIMigrationDirJSON(migDir, true)
	if err != nil {
		t.Fatal(err)
	}
	migManifestNoContent, err := loadCLIMigrationDirJSON(migDir, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want map[string]interface{}
		fail string
	}{
		{"natural query", []string{"--mode", "query", "--request", "count orders"}, map[string]interface{}{"operation": "ask", "credential_mode": "auto"}, ""},
		{"natural plan", []string{"--mode", "plan", "--request", "add external_id"}, map[string]interface{}{"operation": "ask"}, ""},
		{"natural discover", []string{"--mode", "discover", "--request", "list databases"}, map[string]interface{}{"operation": "ask"}, ""},
		{"direct query", []string{"--mode", "query", "--sql", query, "--credential", "orders-app"}, map[string]interface{}{"operation": "ask", "credential_mode": "auto", "sql": query, "sql_credential": "orders-app"}, ""},
		{"query file", []string{"--mode", "query", "--file", queryFile}, map[string]interface{}{"operation": "ask", "credential_mode": "auto", "sql": query}, ""},
		{"direct apply", []string{"--mode", "apply", "--sql", change, "--database", "orders", "--credential", "orders-migrate", "--verify", query}, map[string]interface{}{"operation": "apply", "credential_mode": "auto", "sql": change, "sql_database": "orders", "sql_credential": "orders-migrate", "sql_verification": query}, ""},
		{"apply file dry run", []string{"--mode", "apply", "--file", file, "--verify-file", queryFile, "--dry-run"}, map[string]interface{}{"operation": "apply", "credential_mode": "auto", "sql": change, "sql_verification": query, "sql_dry_run": true}, ""},
		{"inspect", []string{"--mode", "inspect", "--database", "orders"}, map[string]interface{}{"operation": "ask", "credential_mode": "auto", "sql_action": "inspect", "sql_database": "orders"}, ""},
		{"diff", []string{"--mode", "diff", "--database", "orders", "--from", "v1", "--to", "live"}, map[string]interface{}{"operation": "ask", "credential_mode": "auto", "sql_action": "diff", "sql_database": "orders", "sql_diff_from": "v1", "sql_diff_to": "live"}, ""},
		{"datadiff", []string{"--mode", "datadiff", "--source", "staging", "--database", "orders", "--table", "sys_dict", "--generate-fix"}, map[string]interface{}{"operation": "ask", "credential_mode": "auto", "sql_action": "datadiff", "sql_database": "orders", "sql_datadiff_source": "staging", "sql_datadiff_table": "sys_dict", "sql_datadiff_key": "id", "sql_datadiff_chunk": float64(10000), "sql_datadiff_fix": true}, ""},
		{"lint", []string{"--mode", "lint", "--database", "orders", "--sql", change}, map[string]interface{}{"operation": "ask", "credential_mode": "auto", "sql_action": "lint", "sql_database": "orders", "sql": change}, ""},
		{"status", []string{"--mode", "status", "--database", "orders", "--dir", migDir}, map[string]interface{}{"operation": "ask", "credential_mode": "auto", "sql_action": "status", "sql_database": "orders", "sql_migrations": migManifestNoContent}, ""},
		{"migrate", []string{"--mode", "migrate", "--database", "orders", "--dir", migDir, "--release-tag", "v1.0"}, map[string]interface{}{"operation": "apply", "credential_mode": "auto", "sql_action": "migrate", "sql_database": "orders", "sql_migrations": migManifest, "sql_release_tag": "v1.0"}, ""},
		{"backfill", []string{"--mode", "backfill", "--database", "orders", "--table", "orders", "--set", "status='ok'", "--batch-size", "500", "--dry-run"}, map[string]interface{}{"operation": "apply", "credential_mode": "auto", "sql_action": "backfill", "sql_database": "orders", "sql_backfill_table": "orders", "sql_backfill_key": "id", "sql_backfill_set": "status='ok'", "sql_backfill_batch": float64(500), "sql_dry_run": true}, ""},
		{"history all sessions", []string{"--mode", "history", "--database", "orders", "--all-sessions"}, map[string]interface{}{"operation": "ask", "credential_mode": "auto", "sql_action": "history", "sql_database": "orders", "sql_all_sessions": true}, ""},
		{"natural apply rejected", []string{"--mode", "apply", "--request", "add a column"}, nil, "it does not translate --request"},
		{"natural credential rejected", []string{"--mode", "query", "--request", "count orders", "--credential", "orders-app"}, nil, "--credential only applies to --sql/--file"},
		{"plan SQL rejected", []string{"--mode", "plan", "--sql", query}, nil, "--sql/--file"},
		{"discover SQL rejected", []string{"--mode", "discover", "--sql", query}, nil, "--sql/--file"},
		{"query dry run rejected", []string{"--mode", "query", "--sql", query, "--dry-run"}, nil, "--mode apply"},
		{"query verify rejected", []string{"--mode", "query", "--sql", query, "--verify", query}, nil, "--mode apply"},
		{"SQL plus request rejected", []string{"--mode", "apply", "--sql", change, "--request", "change orders"}, nil, "--sql/--file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"-session", "sql-contract", "sql", "--target", "sql-contract"}, tc.args...)
			command := exec.Command(binary, args...)
			command.Env = os.Environ()
			output, err := command.CombinedOutput()
			if tc.fail != "" {
				if err == nil || !strings.Contains(string(output), tc.fail) {
					t.Fatalf("expected %q: %v\n%s", tc.fail, err, output)
				}
				select {
				case call := <-calls:
					t.Fatalf("invalid input reached Gateway: %#v", call)
				default:
				}
				return
			}
			if err != nil {
				t.Fatalf("valid SQL command failed: %v\n%s", err, output)
			}
			select {
			case got := <-calls:
				if got["skill"] != "dosql" || got["session_id"] != "sql-contract" || got["instruction"] == "" {
					t.Fatalf("missing domain/session/audit instruction: %#v", got)
				}
				delete(got, "skill")
				delete(got, "session_id")
				delete(got, "instruction")
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("RPC arguments = %#v, want %#v", got, tc.want)
				}
			default:
				t.Fatal("successful command sent no RPC")
			}
		})
	}
}
