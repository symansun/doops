package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installExternalDriver(t *testing.T, scheme, script string, mode os.FileMode) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(doSQLExternalDirEnv, dir)
	path := filepath.Join(dir, "dosql-driver-"+scheme)
	if err := os.WriteFile(path, []byte(script), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestExternalDriverReceivesDSNOnStdinOnly(t *testing.T) {
	out := filepath.Join(t.TempDir(), "seen")
	installExternalDriver(t, "extsql", "#!/bin/sh\ncat > '"+out+"'\nprintf '%s' \"$#:$(env | wc -l)\" > '"+out+".env'\n"+
		`printf '{"result":{"status":"verified","committed":true,"statements":[{"statement":"x","affected_rows":1}]}}'`+"\n", 0o755)
	target, err := doSQLConnectionTarget("extsql", "app", map[string]string{"connection_uri": "extsql://u:s3cret@db:1/app"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{Target: target, Statements: []string{"UPDATE t SET a=1"}})
	if err != nil || !result.Committed || result.Statements[0].AffectedRows != 1 {
		t.Fatalf("result %#v err %v", result, err)
	}
	var seen doSQLExternalRequest
	raw, _ := os.ReadFile(out)
	if json.Unmarshal(raw, &seen) != nil || !strings.Contains(seen.DSN, "s3cret") || seen.Statements[0] != "UPDATE t SET a=1" {
		t.Fatalf("driver stdin: %s", raw)
	}
	if env, _ := os.ReadFile(out + ".env"); !strings.HasPrefix(string(env), "0:") {
		t.Fatalf("driver got argv or an unexpected environment: %s", env)
	}
}

func TestExternalDriverFailureAndMissingDriver(t *testing.T) {
	installExternalDriver(t, "extsql", "#!/bin/sh\nprintf '{\"result\":{\"status\":\"failed\"},\"error\":\"dosql_statement_failed: boom\"}'\n", 0o755)
	target, _ := doSQLConnectionTarget("extsql", "app", map[string]string{"connection_uri": "extsql://u:p@db/app"})
	if _, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{Target: target, Statements: []string{"DELETE FROM t"}}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("driver error not surfaced: %v", err)
	}
	installExternalDriver(t, "extsql", "#!/bin/sh\necho nope\n", 0o755)
	if _, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{Target: target, Statements: []string{"DELETE FROM t"}}); err == nil || !strings.Contains(err.Error(), "dosql_driver_failed") {
		t.Fatalf("garbage output accepted: %v", err)
	}
	for name, mode := range map[string]os.FileMode{"not executable": 0o644, "world writable": 0o757} {
		installExternalDriver(t, "extsql", "#!/bin/sh\n", mode)
		if _, err := doSQLExternalDriverPath("extsql"); err == nil {
			t.Errorf("%s driver accepted", name)
		}
	}
	for _, scheme := range []string{"../x", "Ext", "a", "ext sql"} {
		if _, err := doSQLExternalDriverPath(scheme); err == nil {
			t.Errorf("scheme %q accepted", scheme)
		}
	}
	t.Setenv(doSQLExternalDirEnv, t.TempDir())
	if _, err := doSQLConnectionTarget("extsql", "app", map[string]string{"connection_uri": "extsql://u:p@db/app"}); err == nil {
		t.Fatal("engine without an installed driver accepted")
	}
}

func TestRunDoSQLDriverKeepsRollbackSemantics(t *testing.T) {
	target := newDoSQLDirectTestTarget(t)
	run := func(req doSQLExternalRequest) doSQLExternalResponse {
		in, _ := json.Marshal(req)
		var out bytes.Buffer
		if err := RunDoSQLDriver("sqlite", "sqlite", bytes.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		var response doSQLExternalResponse
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	bad := run(doSQLExternalRequest{DSN: target.DSN, Statements: []string{"UPDATE orders SET status='x'", "INSERT INTO orders (id,status) VALUES (1,'dup')"}})
	if bad.Error == "" || countDoSQLRows(t, target, "status = 'x'") != "0" {
		t.Fatalf("failed batch was not rolled back: %#v", bad)
	}
	dry := run(doSQLExternalRequest{DSN: target.DSN, Statements: []string{"UPDATE orders SET status='x'"}, DryRun: true})
	if dry.Error != "" || dry.Result.Status != "dry_run_rolled_back" || countDoSQLRows(t, target, "status = 'x'") != "0" {
		t.Fatalf("dry run: %#v", dry)
	}
	ok := run(doSQLExternalRequest{DSN: target.DSN, Statements: []string{"UPDATE orders SET status='x'"}})
	if ok.Error != "" || !ok.Result.Committed || countDoSQLRows(t, target, "status = 'x'") != "2" {
		t.Fatalf("apply: %#v", ok)
	}
}
