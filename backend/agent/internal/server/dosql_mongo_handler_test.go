package server

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/symansun/doops/backend/agent/api"
)

// End to end through the handler: engine inference from the credential, the
// JSON statement planner, the version journal and the evidence file.
func TestDirectDoSQLHandlerMongoRecordsVersions(t *testing.T) {
	target := realMongoTarget(t)
	for _, name := range []string{"zz_mh_a"} {
		realMongoRun(t, target, true, false, "", `{"collection":"`+name+`","op":"drop"}`)
	}
	t.Cleanup(func() {
		realMongoRun(t, target, true, false, "", `{"collection":"zz_mh_a","op":"drop"}`)
	})

	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(root, "ws"))
	if err := os.MkdirAll(filepath.Join(root, "ws", "sess"), 0700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	secret := `{"metadata":{"resourceVersion":"1","labels":{"doops.sh/credential-id":"cred_m","doops.sh/credential-grant-id":"grant_m"},"annotations":{"doops.sh/credential-version":"ver_m"}},"data":{"connection_uri":"` +
		base64.StdEncoding.EncodeToString([]byte(target.DSN)) + `"}}`
	writeExecutable(t, filepath.Join(bin, "kubectl"), "#!/bin/sh\nprintf '%s' '"+secret+"'\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	binding := api.AgentCredentialBinding{
		Name: "mongo-db", Use: string(CredentialUseDatabase), Namespace: "apps", ResourceName: "sec",
		SecretType: "Opaque", Keys: []string{"connection_uri"}, ResourceVersion: "1",
		CredentialID: "cred_m", GrantID: "grant_m", VersionID: "ver_m",
	}
	run := func(operation, text string, dry bool) (string, error) {
		return handleDirectDoSQLPrompt(context.Background(), "sess", api.AgentPromptParams{
			Skill: "dosql", Operation: operation, SQL: text, SQLDryRun: dry,
			CredentialBindings: []api.AgentCredentialBinding{binding},
		})
	}
	path := doSQLVersionJournalPath("mongodb", target.Database)
	_ = os.Remove(path)

	insert := `{"collection":"zz_mh_a","op":"insertMany","documents":[{"k":1},{"k":2}]}`
	if _, err := run("ask", `{"collection":"zz_mh_a","op":"count","filter":{}}`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := run("apply", insert, true); err != nil {
		t.Fatal(err)
	}
	if versions, _ := listDoSQLVersions(path); len(versions) != 0 {
		t.Fatalf("queries and dry runs must not take versions: %#v", versions)
	}
	if got := realMongoCount(t, target, "zz_mh_a", `{}`); got != "0" {
		t.Fatalf("dry run left %s documents", got)
	}

	first, err := run("apply", insert, false)
	if err != nil || !strings.Contains(first, `"version": 1`) || !strings.Contains(first, `"engine": "mongodb"`) {
		t.Fatalf("first apply: %v\n%s", err, first)
	}
	if got := realMongoCount(t, target, "zz_mh_a", `{}`); got != "2" {
		t.Fatalf("apply persisted %s documents", got)
	}
	// An invalid statement is refused while planning: nothing runs, no version.
	if _, err := run("apply", `{"collection":"zz_mh_a","op":"deleteMany"}`, false); err == nil {
		t.Fatal("deleteMany without a filter was accepted")
	}
	// A runtime failure rolls back and still names its version.
	_, err = run("apply", `[{"collection":"zz_mh_a","op":"updateMany","filter":{},"update":{"$set":{"k":9}}},{"collection":"zz_mh_a","op":"updateOne","filter":{"k":1},"update":{"$badop":{"k":1}}}]`, false)
	if err == nil || !strings.Contains(err.Error(), "version=2") {
		t.Fatalf("a failed apply must name its version, got %v", err)
	}
	if got := realMongoCount(t, target, "zz_mh_a", `{"k":9}`); got != "0" {
		t.Fatalf("failed batch was not rolled back: %s documents changed", got)
	}
	versions, err := listDoSQLVersions(path)
	if err != nil || len(versions) != 2 || versions[0].Status != "verified" || versions[1].Status != "failed" {
		t.Fatalf("versions: %#v %v", versions, err)
	}
	if unfinished := doSQLVersionUnfinished(versions); len(unfinished) != 0 {
		t.Fatalf("unfinished versions: %v", unfinished)
	}
}
