package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseRunnerCreatesStructuredJobsWithoutSed(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		engine  string
		check   string
		request func() doSQLRunnerRequest
	}{
		{
			name:   "postgres",
			engine: "postgres",
			check:  "psql",
			request: func() doSQLRunnerRequest {
				return doSQLRunnerRequest{
					OperationID: "dbop_postgres_test", Phase: "operation", Engine: "postgres",
					Namespace: "apps", DatabaseName: "orders", SecretName: "database-secret",
					ClientImage: "postgres:16@sha256:" + strings.Repeat("a", 64), URIKey: "connection_uri",
					ReadOnly: true, Statement: "SELECT 1",
				}
			},
		},
		{
			name:   "mysql",
			engine: "mysql",
			check:  "mysql",
			request: func() doSQLRunnerRequest {
				return doSQLRunnerRequest{
					OperationID: "dbop_mysql_test", Phase: "operation", Engine: "mysql",
					Namespace: "apps", DatabaseName: "orders", SecretName: "database-secret",
					ClientImage: "mysql:8.4@sha256:" + strings.Repeat("b", 64), URLKey: "SPRING_DATASOURCE_URL",
					UserKey: "SPRING_DATASOURCE_USERNAME", PasswordKey: "SPRING_DATASOURCE_PASSWORD",
					ReadOnly: true, Statement: "SELECT 1",
				}
			},
		},
		{
			name:   "mongodb",
			engine: "mongodb",
			check:  "mongosh",
			request: func() doSQLRunnerRequest {
				return doSQLRunnerRequest{
					OperationID: "dbop_mongodb_test", Phase: "operation", Engine: "mongodb",
					Namespace: "apps", DatabaseName: "orders", SecretName: "database-secret",
					ClientImage: "mongo:8@sha256:" + strings.Repeat("c", 64), URIKey: "connection_uri",
					MongoScript: "printjson({ok: 1})", MongoOperation: json.RawMessage(`{"method":"getCollectionNames","args":[]}`),
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			kubectl := filepath.Join(root, "kubectl")
			writeExecutable(t, kubectl, `#!/bin/sh
set -eu
state="$DOSQL_RUNNER_TEST_STATE"
case "$1" in
  create)
    cat >"$state.last-create.json"
    if grep -q '"kind":"Job"' "$state.last-create.json"; then cp "$state.last-create.json" "$state.job.json"; else cp "$state.last-create.json" "$state.secret.json"; fi
    ;;
  get)
    printf '%s' '{"metadata":{"uid":"job-uid"},"status":{"succeeded":1}}'
    ;;
  patch)
    ;;
  logs)
    printf '%s\n' 'RUNNER_OUTPUT_CANARY'
    ;;
  delete)
    ;;
esac
`)
			t.Setenv("KUBECTL", kubectl)
			t.Setenv("DOSQL_RUNNER_TEST_STATE", filepath.Join(root, "state"))

			var output strings.Builder
			var errorOutput strings.Builder
			if err := RunDatabaseRunnerContext(context.Background(), strings.NewReader(mustRunnerJSON(t, testCase.request())), &output, &errorOutput); err != nil {
				t.Fatalf("run %s: %v; stderr=%s", testCase.engine, err, errorOutput.String())
			}
			if !strings.Contains(output.String(), "RUNNER_OUTPUT_CANARY") {
				t.Fatalf("runner output = %q", output.String())
			}
			job, err := os.ReadFile(filepath.Join(root, "state.job.json"))
			if err != nil {
				t.Fatal(err)
			}
			jobText := string(job)
			if !strings.Contains(jobText, testCase.check) || strings.Contains(jobText, "sed") {
				t.Fatalf("job did not contain native %s execution without sed: %s", testCase.check, jobText)
			}
			secret, err := os.ReadFile(filepath.Join(root, "state.secret.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(secret), "postgres://") || strings.Contains(string(secret), "password") {
				t.Fatalf("input Secret leaked credential-like material: %s", secret)
			}
		})
	}
}

func mustRunnerJSON(t *testing.T, value interface{}) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
