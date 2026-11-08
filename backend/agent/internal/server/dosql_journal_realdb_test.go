package server

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/user/doops/agent/api"
)

func TestRealDoSQLMySQLJournalEndpointIsolation(t *testing.T) {
	secondDSN := os.Getenv("DOOPS_TEST_MYSQL_SECOND_DSN")
	if secondDSN == "" {
		t.Skip("DOOPS_TEST_MYSQL_SECOND_DSN not set; requires two disposable local MySQL instances")
	}
	first := realDoSQLTarget(t, "mysql")
	second := first
	second.DSN = secondDSN
	toURI := func(target doSQLTarget) string {
		t.Helper()
		config, err := mysql.ParseDSN(target.DSN)
		if err != nil {
			t.Fatal("invalid test DSN")
		}
		host, _, err := net.SplitHostPort(config.Addr)
		if err != nil || config.Net != "tcp" || (host != "127.0.0.1" && host != "::1") || config.DBName != "testdb" {
			t.Fatal("journal acceptance requires disposable loopback testdb instances")
		}
		uri := url.URL{Scheme: "mysql", User: url.UserPassword(config.User, config.Passwd), Host: config.Addr, Path: "/testdb"}
		return uri.String()
	}
	firstURI, secondURI := toURI(first), toURI(second)
	targetA, err := doSQLConnectionTarget("mysql", "testdb", map[string]string{"connection_uri": firstURI})
	if err != nil {
		t.Fatal(err)
	}
	targetB, err := doSQLConnectionTarget("mysql", "testdb", map[string]string{"connection_uri": secondURI})
	if err != nil || targetA.Address == targetB.Address {
		t.Fatal("two distinct local endpoints are required")
	}
	realDoSQLCleanup(t, first, "zz_ver_endpoint")
	realDoSQLCleanup(t, second, "zz_ver_endpoint")
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(root, "ws"))
	if err := os.MkdirAll(filepath.Join(root, "ws", "journal-test"), 0700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	binding := api.AgentCredentialBinding{
		Name: "test-db", Use: string(CredentialUseDatabase), Namespace: "tests", ResourceName: "synthetic",
		SecretType: "Opaque", Keys: []string{"connection_uri"}, ResourceVersion: "1",
		CredentialID: "cred-test", GrantID: "grant-test", VersionID: "version-1",
	}
	run := func(uri, sqlText string) (doSQLDirectReport, error) {
		t.Helper()
		// Only Secret delivery is a local fixture. The production handler and
		// database/sql driver execute against two actual MySQL servers.
		secret := `{"metadata":{"resourceVersion":"1","labels":{"doops.sh/credential-id":"cred-test","doops.sh/credential-grant-id":"grant-test"},"annotations":{"doops.sh/credential-version":"` + binding.VersionID +
			`"}},"data":{"connection_uri":"` + base64.StdEncoding.EncodeToString([]byte(uri)) + `"}}`
		writeExecutable(t, filepath.Join(bin, "kubectl"), "#!/bin/sh\nprintf '%s' '"+secret+"'\n")
		text, err := handleDirectDoSQLPrompt(t.Context(), "journal-test", api.AgentPromptParams{
			Skill: "dosql", Operation: "apply", SQL: sqlText,
			CredentialBindings: []api.AgentCredentialBinding{binding},
		})
		var report doSQLDirectReport
		if err == nil {
			if err := json.Unmarshal([]byte(text), &report); err != nil {
				t.Fatal(err)
			}
		}
		return report, err
	}
	for _, uri := range []string{firstURI, secondURI} {
		report, err := run(uri, "CREATE TABLE zz_ver_endpoint (id int PRIMARY KEY) ENGINE=InnoDB")
		if err != nil || report.Version != 1 || !report.Result.Committed {
			t.Fatalf("endpoint did not begin at v1: version=%d error=%v", report.Version, err)
		}
	}
	if report, err := run(firstURI, "INSERT INTO zz_ver_endpoint VALUES (1)"); err != nil || report.Version != 2 {
		t.Fatalf("first endpoint v2: %d %v", report.Version, err)
	}
	if _, err := realDoSQLRun(t, first, true, false, "",
		"CREATE USER 'doops_journal_rotation'@'%' IDENTIFIED BY 'synthetic-rotation-only'",
		"GRANT ALL ON testdb.* TO 'doops_journal_rotation'@'%'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = realDoSQLRun(t, first, true, false, "", "DROP USER IF EXISTS 'doops_journal_rotation'@'%'")
	})
	rotatedURI, _ := url.Parse(firstURI)
	rotatedURI.User = url.UserPassword("doops_journal_rotation", "synthetic-rotation-only")
	binding.VersionID = "version-2"
	if report, err := run(rotatedURI.String(), "INSERT INTO zz_ver_endpoint VALUES (2)"); err != nil || report.Version != 3 {
		t.Fatalf("real credential rotation did not retain v3: %d %v", report.Version, err)
	}
	if got := realDoSQLScalar(t, first, "SELECT count(*) FROM zz_ver_endpoint"); got != "2" {
		t.Fatalf("first endpoint stored %s rows", got)
	}
	if got := realDoSQLScalar(t, second, "SELECT count(*) FROM zz_ver_endpoint"); got != "0" {
		t.Fatalf("second endpoint received first endpoint's writes: %s rows", got)
	}
	pathA, err := doSQLVersionJournalPath(targetA)
	if err != nil {
		t.Fatal(err)
	}
	pathB, err := doSQLVersionJournalPath(targetB)
	if err != nil || pathA == pathB {
		t.Fatalf("endpoints share a journal: %v", err)
	}
	for path, count := range map[string]int{pathA: 3, pathB: 1} {
		items, err := listDoSQLVersions(path)
		if err != nil || len(items) != count || len(doSQLVersionUnfinished(items)) != 0 {
			t.Fatalf("journal entries=%d want=%d error=%v", len(items), count, err)
		}
	}
	legacy := filepath.Join(root, ".doops-dosql-versions", "mysql__testdb.jsonl")
	if _, err := beginDoSQLVersion(legacy, "unknown-legacy-owner", "old-binding", "fp", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := run(firstURI, "INSERT INTO zz_ver_endpoint VALUES (9)"); err == nil ||
		!strings.Contains(err.Error(), "dosql_version_journal_migration_required") {
		t.Fatalf("ambiguous legacy journal did not block the handler: %v", err)
	}
	if got := realDoSQLScalar(t, first, "SELECT count(*) FROM zz_ver_endpoint"); got != "2" {
		t.Fatalf("legacy rejection still executed a write: %s rows", got)
	}
}
