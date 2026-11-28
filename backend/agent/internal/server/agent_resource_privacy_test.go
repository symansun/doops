package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A real credential version is present before the first projection. Inspect
// the HTTP projection, the original apply bytes persisted by the Agent, the
// pinned file, the ACP request and the user's audit feed for the same marker.
func TestTargetManifestCredentialBoundaryThroughTunnel(t *testing.T) {
	stubKubernetesProbe(t, false)
	const name = "manifest opaque"
	const ref = "credential/manifest%20opaque"
	canary := "manifest-probe-" + randomHex(20)
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", randomHex(16))
	workspace := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", workspace)
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true, AgentLease: time.Hour})
	f.httpTimeout = 15 * time.Second
	owner := createCredentialTestUser(t, f.store, "manifest-owner")
	c, err := f.store.CreateCredential(CredentialCreateRequest{Name: name, Scope: CredentialScopePersonal, Type: CredentialTypeOpaque, OwnerID: owner.ID, CreatedBy: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"data": map[string]string{"SECRET_VALUE": canary}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutCredentialVersion(CredentialVersionPutRequest{CredentialID: c.ID, Payload: payload, CreatedBy: owner.ID, Activate: true}); err != nil {
		t.Fatal(err)
	}
	scope := CredentialGrantScope{Cluster: "ops", Instance: "one", Use: CredentialUseOpaqueSecret, Namespace: "apps", RequiredKeys: []string{"SECRET_VALUE"}}
	if _, err := f.store.CreateCredentialGrant(CredentialGrantCreateRequest{CredentialID: c.ID, Scope: scope, CreatedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}
	check := func(surface string, raw []byte) {
		t.Helper()
		if bytes.Contains(raw, []byte(canary)) || bytes.Contains(raw, []byte("SECRET_VALUE")) {
			t.Fatalf("%s contains credential payload", surface)
		}
		if !bytes.Contains(raw, []byte(ref)) {
			t.Fatalf("%s missing credential name reference", surface)
		}
	}
	dir := t.TempDir()
	connectManifestTestAgent(t, f, dir)
	user := f.user(t, "manifest-reader", ScopeGrant{Cluster: "ops", Instance: "one"})
	result := f.http(t, user, "POST", "/v1/assets/target-manifest", `{"action":"refresh","cluster":"ops","instance":"one"}`, 200)
	check("Gateway manifest", result)
	var status targetManifestStatus
	if err := json.Unmarshal(result, &status); err != nil || status.Manifest.Revision == 0 || status.AppliedRevision != status.Manifest.Revision {
		t.Fatal("Gateway did not confirm the projected credential")
	}
	// apply writes the received JSON-RPC params verbatim to current.json.
	readFile := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	check("received apply payload and current.json", readFile(filepath.Join(dir, "current.json")))
	client := dialGatewayRPCClient(t, f.server.URL, user.Plaintext, "ops", "one")
	defer client.Close()
	lifecycleSend(t, client, 2, "doops_shell", "manifest-exec", map[string]any{"command": "printf '%s' '" + ref + "'"})
	lifecycleResponse(t, client, 2, "")
	check("exec workspace", readFile(filepath.Join(workspace, "manifest-exec", "target.json")))

	// The ACP fixture queues the turn. Inspect the actual prompt delivered to
	// session/new or session/prompt and verify that the rejected turn is audited.
	acp, _ := newPromptAdmissionDoagent(t, func() map[string]any {
		return promptRPCResult(map[string]any{"status": "queued", "queuePosition": 1, "delivery": "sse"})
	})
	defer acp.Close()
	prompts := make(chan []byte, 4)
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rpc" {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(data))
			var call struct{ Method string }
			if json.Unmarshal(data, &call) == nil && (call.Method == "session/new" || call.Method == "session/prompt") {
				prompts <- data
			}
		}
		acp.Config.Handler.ServeHTTP(w, r)
	}))
	defer recorder.Close()
	t.Setenv("DO_AGENT_URL", recorder.URL)
	lifecycleSend(t, client, 3, "doops_agent_prompt", "manifest-ask", map[string]any{"instruction": "简述当前目标"})
	lifecycleResponse(t, client, 3, doagentPromptCleanupUnknown)
	var prompt bytes.Buffer
	for len(prompts) > 0 {
		prompt.Write(<-prompts)
	}
	check("ACP prompt", prompt.Bytes())
	check("ask workspace", readFile(filepath.Join(workspace, "manifest-ask", "target.json")))
	audit := f.http(t, user, "GET", "/v1/audit?cluster=ops&instance=one", "", 200)
	check("audit", audit)
	var history auditResponse
	if err := json.Unmarshal(audit, &history); err != nil {
		t.Fatal(err)
	}
	for _, item := range history.Events {
		if item.Session == "manifest-exec" && item.Status == "success" && item.ManifestRevision == status.AppliedRevision && slices.Contains(item.ResourceRefs, ref) {
			return
		}
	}
	t.Fatalf("missing successful exec audit at revision %d for %s", status.AppliedRevision, ref)
}
