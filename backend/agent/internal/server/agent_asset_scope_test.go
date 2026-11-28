package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestWorkspaceAssetScope(t *testing.T) {
	f := newMachineFixture(t)
	for _, name := range []string{"cluster_inventory", "business_inventory", "agent_asset_scope"} {
		b, err := os.ReadFile("../../../docs/migrations/" + name + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, string(b))
	}
	for _, name := range []string{"left", "right"} {
		if err := f.store.MarkAgentOnline(AgentStatus{Cluster: "ops", Instance: name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.GrantUser(f.userID, ScopeGrant{Cluster: "ops", Instance: "left"}); err != nil {
		t.Fatal(err)
	}
	f.save(t, "create", machineInventoryInput{AssetID: "shared", Name: "Shared", Kind: "vm"}, 0, 200)
	f.save(t, "create", machineInventoryInput{AssetID: "private-canary", Name: "Private", Kind: "vm", Cluster: "ops", Instance: "right"}, 0, 200)
	call := func(token, query string, body any, want int) string {
		t.Helper()
		method := "GET"
		if body != nil {
			method = "POST"
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, f.server.URL+"/v1/assets/workspace"+query, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, query, res.StatusCode, want, out)
		}
		return string(out)
	}
	left := "?cluster=ops&instance=left"
	right := "?cluster=ops&instance=right"
	req := func(i string, assigned bool) map[string]any {
		return map[string]any{"cluster": "ops", "instance": i, "kind": "machine", "asset_id": "shared", "assigned": assigned}
	}
	if out := call(f.user, "?view=directory", nil, 200); !strings.Contains(out, "left") || strings.Contains(out, "right") {
		t.Fatal(out)
	}
	call(f.user, "?view=targets", nil, 403)
	call(f.user, left+"&view=manage", nil, 403)
	call(f.user, right, nil, 403)
	call(f.user, "", req("left", true), 403)
	call(f.admin, "", req("*", true), 400)
	call(f.admin, "", req("unknown", true), 404)
	if out := call(f.user, left, nil, 200); strings.Contains(out, "shared") || strings.Contains(out, "private-canary") {
		t.Fatal(out)
	}
	for _, i := range []string{"left", "right", "left"} {
		call(f.admin, "", req(i, true), 200)
	}
	if out := call(f.user, left, nil, 200); !strings.Contains(out, "shared") || strings.Contains(out, "private-canary") || strings.Contains(out, "password") {
		t.Fatal(out)
	}
	call(f.admin, "", req("left", false), 200)
	if strings.Contains(call(f.user, left, nil, 200), "shared") {
		t.Fatal("removed assignment still visible")
	}
	if !strings.Contains(call(f.admin, right, nil, 200), "shared") {
		t.Fatal("sibling assignment removed")
	}
	f.exec(t, "CREATE TRIGGER deny_scope_audit BEFORE UPDATE ON audit_events WHEN NEW.status='success' BEGIN SELECT RAISE(ABORT,'unavailable'); END")
	call(f.admin, "", req("left", true), 503)
	if strings.Contains(call(f.user, left, nil, 200), "shared") {
		t.Fatal("unaudited assignment persisted")
	}
	f.exec(t, "DELETE FROM grants WHERE user_id=?", f.userID)
	call(f.user, left, nil, 403)
}
