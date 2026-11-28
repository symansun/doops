package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Run the actual operator SQL against a file-backed SQLite database and, when
// configured, a private schema on the disposable local MySQL test server.
func TestAgentStatusCapabilitiesMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "gateway.db")
			t.Setenv("DOOPS_GATEWAY_STORE", dialect)
			var db *sql.DB
			if dialect == "mysql" {
				db = gatewayCopyTestDestination(t)
				var name string
				if err := db.QueryRow("SELECT DATABASE()").Scan(&name); err != nil {
					t.Fatal(err)
				}
				t.Setenv("DOOPS_GATEWAY_DATA_DIR", dir)
				t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", fmt.Sprintf("root@tcp(127.0.0.1:%s)/%s", os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"), name))
			} else {
				var err error
				db, err = sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			}
			filename := "agent_status_capabilities." + dialect + ".sql"
			preflight, err := os.ReadFile(filepath.Join("../../../docs/migrations", filename))
			if err != nil {
				t.Fatal(err)
			}
			check := func(t *testing.T, want string) string {
				t.Helper()
				var status string
				var ddl sql.NullString
				if err := db.QueryRow(string(preflight)).Scan(&status, &ddl); err != nil {
					t.Fatal(err)
				}
				if status != want || ddl.Valid != (want == "UPGRADE") {
					t.Fatalf("preflight = %q, %+v; want %q", status, ddl, want)
				}
				return ddl.String
			}
			check(t, "STOP: expected agent_status primary key (cluster, instance)")
			var adminToken string
			withCapabilityStore(t, path, func(s *GatewayStore) {
				check(t, "READY")
				_, adminToken = createCredentialHTTPUser(t, s, "migration-review", true)
			})

			for _, missing := range [][]string{{"mode", "kubernetes_available"}, {"mode"}, {"kubernetes_available"}} {
				if !t.Run("missing_"+strings.Join(missing, "_and_"), func(t *testing.T) {
					available := false
					agent := AgentStatus{Cluster: "res", Instance: "node", Generation: 7, ReleaseCommit: "original", Mode: "docker", KubernetesAvailable: &available}
					withCapabilityStore(t, path, func(s *GatewayStore) {
						if err := s.MarkAgentOnline(agent); err != nil {
							t.Fatal(err)
						}
					})
					wantMode, wantKube := agent.Mode, agent.KubernetesAvailable
					for _, column := range missing {
						if _, err := db.Exec("ALTER TABLE agent_status DROP COLUMN " + column); err != nil {
							t.Fatal(err)
						}
						if column == "mode" {
							wantMode = ""
						} else {
							wantKube = nil
						}
					}
					ddl := check(t, "UPGRADE")
					s, err := OpenGatewayStore(path)
					if err == nil {
						_, err = NewAgentRegistry(s)
						s.Close()
					}
					if err == nil || !strings.Contains(err.Error(), missing[0]) || (dialect == "sqlite" && !strings.Contains(err.Error(), filename)) {
						t.Fatalf("startup on legacy layout: %v", err)
					}
					if got := check(t, "UPGRADE"); got != ddl {
						t.Fatalf("startup changed the required upgrade: %q => %q", ddl, got)
					}
					if _, err := db.Exec(ddl); err != nil {
						t.Fatalf("execute operator DDL %q: %v", ddl, err)
					}
					check(t, "READY")
					withCapabilityStore(t, path, func(s *GatewayStore) {
						check(t, "READY")
						assertCapabilityStatus(t, s, adminToken, wantMode, wantKube)
						available = true
						agent.Mode = "kubernetes"
						if err := s.MarkAgentOnline(agent); err != nil {
							t.Fatal(err)
						}
					})
					withCapabilityStore(t, path, func(s *GatewayStore) { assertCapabilityStatus(t, s, adminToken, "kubernetes", &available) })
				}) {
					return
				}
			}
			if _, err := db.Exec("ALTER TABLE agent_status DROP COLUMN mode"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("ALTER TABLE agent_status ADD COLUMN mode TEXT"); err != nil {
				t.Fatal(err)
			}
			check(t, "STOP: unexpected agent_status.mode definition")
		})
	}
}

func withCapabilityStore(t *testing.T, path string, use func(*GatewayStore)) {
	t.Helper()
	s, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	use(s)
}

func assertCapabilityStatus(t *testing.T, s *GatewayStore, token, mode string, available *bool) {
	t.Helper()
	hub := NewGatewayHub(s, GatewayHubOptions{})
	defer hub.CloseTunnels()
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	response := credentialHTTPRequest(t, mux, token, http.MethodGet, "/v1/admin/instances?include=history", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("read instance history after restart: HTTP %d: %s", response.Code, response.Body)
	}
	var history struct{ Instances []adminInstanceInfo }
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil || len(history.Instances) != 1 {
		t.Fatalf("instance history: %+v, %v", history, err)
	}
	a := history.Instances[0]
	if a.Cluster != "res" || a.Instance != "node" || a.Status != "offline" || a.Mode != mode || !reflect.DeepEqual(a.KubernetesAvailable, available) {
		t.Fatalf("unexpected offline instance %+v (want mode %q, available %v)", a, mode, available)
	}
	var generation int
	var release string
	err := s.db.QueryRow("SELECT generation, release_commit FROM agent_status WHERE cluster=? AND instance=?", a.Cluster, a.Instance).Scan(&generation, &release)
	if err != nil || generation != 7 || release != "original" {
		t.Fatalf("legacy record after restart: generation=%d release=%q error=%v", generation, release, err)
	}
}
