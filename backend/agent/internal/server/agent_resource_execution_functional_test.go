package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The model boundary is deterministic. Gateway, both WS peers, the command
// executor, package sync, ACP requests, SSE reader and audit store are real.
type manifestACPTurn struct {
	Session, CWD, System, Prompt string
	Tools                        []string
}

func manifestTestACP(t *testing.T) <-chan manifestACPTurn {
	t.Helper()
	turns := make(chan manifestACPTurn, 16)
	type acceptedTurn struct {
		id           string
		omitTerminal bool
	}
	accepted := make(chan acceptedTurn, 1)
	var mu sync.Mutex
	var session manifestACPTurn
	var turn int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			select {
			case turn := <-accepted:
				for _, update := range []map[string]any{
					{"sessionUpdate": "agent_message", "turnId": turn.id, "content": map[string]string{"type": "text", "text": `{"status":"pass"}`}},
					{"sessionUpdate": "turn_finished", "turnId": turn.id, "status": "completed", "stopReason": "end_turn"},
				} {
					if turn.omitTerminal && update["sessionUpdate"] == "turn_finished" {
						return
					}
					frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": update}})
					if err != nil {
						t.Error(err)
						return
					}
					fmt.Fprintf(w, "data: %s\n\n", frame)
				}
			case <-r.Context().Done():
			}
			return
		}
		var rpc struct {
			ID     any
			Method string
			Params struct {
				SessionID, CWD, SystemPrompt, Prompt string
				AllowedTools                         []string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Errorf("ACP request: %v", err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		result := map[string]any{}
		switch rpc.Method {
		case "initialize":
			result = doagentAdmissionCapabilities()
		case "session/new":
			session = manifestACPTurn{Session: rpc.Params.SessionID, CWD: rpc.Params.CWD, System: rpc.Params.SystemPrompt}
			result["sessionId"] = rpc.Params.SessionID
		case "session/setMode", "session/setModel":
		case "session/cancel":
			result["status"] = "cancelling"
		case "session/prompt":
			session.Prompt, session.Tools = rpc.Params.Prompt, rpc.Params.AllowedTools
			turns <- session
			turn++
			id := fmt.Sprint(turn)
			result = map[string]any{"status": "accepted", "delivery": "sse", "turnId": id}
			accepted <- acceptedTurn{id, strings.Contains(rpc.Params.Prompt, "omit terminal event")}
		default:
			t.Errorf("unexpected ACP method %q", rpc.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	t.Setenv("DO_AGENT_URL", server.URL)
	return turns
}

func manifestAuditEvents(t *testing.T, f *executionLifecycleFixture, user CreatedToken) []auditRecord {
	t.Helper()
	var feed auditResponse
	if err := json.Unmarshal(f.http(t, user, "GET", "/v1/audit?cluster=ops&instance=one&limit=500", "", 200), &feed); err != nil {
		t.Fatal(err)
	}
	return feed.Events
}

func checkManifestAudit(t *testing.T, f *executionLifecycleFixture, user CreatedToken, session string, action GatewayAction, status string, rev uint64, refs ...string) auditRecord {
	t.Helper()
	for _, e := range manifestAuditEvents(t, f, user) {
		if e.Session != session || e.Action != string(action) {
			continue
		}
		if e.Status != status || e.ManifestRevision != rev || !slices.Equal(e.ResourceRefs, refs) || (rev != 0 && e.ResourceRefs == nil) {
			t.Fatalf("audit (%s, %s) got status=%s rev=%d refs=%#v err=%s, want %s %d %v", session, action, e.Status, e.ManifestRevision, e.ResourceRefs, e.Error, status, rev, refs)
		}
		return e
	}
	t.Fatalf("no %s audit for %s", action, session)
	return auditRecord{}
}

func refreshExecutionTestManifest(t *testing.T, f *executionLifecycleFixture, user CreatedToken) targetManifest {
	t.Helper()
	var status targetManifestStatus
	raw := f.http(t, user, "POST", "/v1/assets/target-manifest", `{"action":"refresh","cluster":"ops","instance":"one"}`, 200)
	if err := json.Unmarshal(raw, &status); err != nil || status.Manifest.Revision == 0 || status.AppliedRevision != status.Manifest.Revision {
		t.Fatalf("refresh: %v %s", err, raw)
	}
	return status.Manifest
}

func executionTestSite(t *testing.T, f *executionLifecycleFixture, name string) {
	t.Helper()
	_, err := f.store.db.Exec(`INSERT INTO inventory_resource_catalog(id,kind,cluster,instance,created_by,name,environment,details,identity_key,revision) VALUES(?,'website','ops','one','tester',?,'test','{}',?,1)`, name, name, name)
	if err != nil {
		t.Fatal(err)
	}
}

func readPinnedManifest(t *testing.T, session string) targetManifest {
	t.Helper()
	var manifest targetManifest
	raw, err := os.ReadFile(filepath.Join(workspaceRoot(), session, "target.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Digest != manifest.contentDigest() {
		t.Fatalf("%s snapshot: %v %s", session, err, raw)
	}
	return manifest
}

func finishManifestTestCall(t *testing.T, conn *websocket.Conn, id int) (warnings, output string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer conn.SetReadDeadline(time.Time{})
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		if p, ok := msg["params"].(map[string]any); ok && msg["method"] == "notifications/message" {
			data, _ := p["data"].(string)
			if p["kind"] == "warning" {
				warnings += data
			} else {
				output += data
			}
		}
		if msg["id"] == float64(id) {
			result, ok := msg["result"].(map[string]any)
			if !ok || result["isError"] == true {
				t.Fatalf("tool call failed: %+v", msg)
			}
			warnings, _ = result["manifest_warning"].(string)
			return warnings, output + resultContentText(result)
		}
	}
}

func TestManifestExecutionTurnsAndAudit(t *testing.T) {
	stubKubernetesProbe(t, true)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte("#!/bin/sh\ncase \"$*\" in *'get namespace kube-system'*) printf uid-ops;; *) exit 1;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	turns := manifestTestACP(t)
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true, AgentLease: time.Hour})
	user := f.user(t, "manifest-execution", ScopeGrant{Cluster: "ops", Instance: "one"})
	executionTestSite(t, f, "orders")
	const session = "same-conversation"
	dir := filepath.Join(t.TempDir(), "manifest")
	if err := os.WriteFile(dir, []byte("force first apply to fail"), 0600); err != nil {
		t.Fatal(err)
	}
	connectManifestTestAgent(t, f, dir)
	client := dialGatewayRPCClient(t, f.server.URL, user.Plaintext, "ops", "one")
	defer client.Close()
	if err := os.Mkdir(filepath.Join(workspaceRoot(), session), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot(), session, "target.json"), []byte(`{"revision":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	f.http(t, user, "POST", "/v1/assets/target-manifest", `{"action":"refresh","cluster":"ops","instance":"one"}`, 503)
	lifecycleSend(t, client, 11, "doops_agent_prompt", session, map[string]any{"instruction": "检查 site/orders"})
	warn, _ := finishManifestTestCall(t, client, 11)
	first := <-turns
	if !strings.Contains(warn, missingExecutionManifest) || !strings.Contains(first.System, missingExecutionManifest) || first.CWD != filepath.Join(workspaceRoot(), session) {
		t.Fatalf("missing manifest: %q %+v", warn, first)
	}
	if _, err := os.Stat(filepath.Join(first.CWD, "target.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous target.json survived: %v", err)
	}
	checkManifestAudit(t, f, user, session, ActionAsk, "success", 0)
	lifecycleSend(t, client, 12, "doops_shell", "missing-shell", map[string]any{"command": "printf 'still ran'"})
	warn, out := finishManifestTestCall(t, client, 12)
	if !strings.Contains(warn, missingExecutionManifest) || !strings.Contains(out, "still ran") {
		t.Fatalf("shell warning %q output %q", warn, out)
	}
	checkManifestAudit(t, f, user, "missing-shell", ActionExec, "success", 0)
	if err := os.Rename(dir, dir+"-blocked"); err != nil {
		t.Fatal(err)
	}
	old := refreshExecutionTestManifest(t, f, user)
	lifecycleSend(t, client, 13, "doops_agent_prompt", session, map[string]any{"instruction": "检查 site/orders 与 site/orders-typo"})
	finishManifestTestCall(t, client, 13)
	second := <-turns
	version := fmt.Sprintf("目标清单版本 %d", old.Revision)
	if second.Session != first.Session || !strings.Contains(second.Prompt, version) || !strings.Contains(second.System, version) || !slices.Contains(second.Tools, "mcp_doops_k8s_read") || strings.Contains(second.System, "可能过期") {
		t.Fatalf("confirmed turn: %+v", second)
	}
	checkManifestAudit(t, f, user, session, ActionAsk, "success", old.Revision, "site/orders")
	if got := readPinnedManifest(t, session); got.Digest != old.Digest {
		t.Fatal("ask used a different snapshot")
	}

	// Hold a real command open across a refresh; check both the file in the
	// running command and the old audit after the new revision is confirmed.
	marker := filepath.Join(t.TempDir(), "running")
	file := filepath.Join(workspaceRoot(), "pinned-shell", "target.json")
	cmd := fmt.Sprintf("printf ready > %s; while [ ! -f %s ]; do sleep 0.05; done; cat %s # site/orders", shellSingleQuote(marker), shellSingleQuote(marker+".finish"), shellSingleQuote(file))
	lifecycleSend(t, client, 14, "doops_shell", "pinned-shell", map[string]any{"command": cmd})
	requireEventually(t, 4*time.Second, func() bool { _, err := os.Stat(marker); return err == nil })
	_, err := f.store.db.Exec(`INSERT INTO cluster_inventory_sources(cluster,instance,asset_id,name,kube_system_uid,verified_at,verified_by,scope) VALUES('ops','one','cluster-id','cluster','other-uid',?,'fixture','list_namespaces')`, formatTime(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	newManifest := refreshExecutionTestManifest(t, f, user)
	if newManifest.Revision <= old.Revision {
		t.Fatal("catalog change did not advance revision")
	}
	if err := os.WriteFile(marker+".finish", nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, out = finishManifestTestCall(t, client, 14)
	if !strings.Contains(out, old.Digest) || strings.Contains(out, newManifest.Digest) {
		t.Fatalf("command did not read its pinned version: %s", out)
	}
	checkManifestAudit(t, f, user, "pinned-shell", ActionExec, "success", old.Revision, "site/orders")
	a := f.hub.getAgent("ops", "one")
	a.stateMu.Lock()
	a.resourceSync.lastObservedAt = formatTime(time.Now().Add(-4 * time.Minute))
	a.stateMu.Unlock()
	lifecycleSend(t, client, 15, "doops_agent_prompt", session, map[string]any{"instruction": "核对 cluster/cluster-id"})
	finishManifestTestCall(t, client, 15)
	third := <-turns
	version = fmt.Sprintf("目标清单版本 %d", newManifest.Revision)
	if third.Session != first.Session || !strings.Contains(third.Prompt, version) || !strings.Contains(third.Prompt, "可能过期") || !strings.Contains(third.System, "可能过期") || slices.Contains(third.Tools, "mcp_doops_k8s_read") || !slices.Contains(third.Tools, "Bash") {
		t.Fatalf("stale/conflicting cluster turn: %+v", third)
	}
	checkManifestAudit(t, f, user, session, ActionAsk, "success", newManifest.Revision, "cluster/cluster-id")
	if got := readPinnedManifest(t, session); got.Digest != newManifest.Digest {
		t.Fatal("reused session did not refresh snapshot")
	}
	// The target list (used by the UI) and the actual ACP tool list must
	// agree even though the Kubernetes API itself still answers.
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"lost", "unverified"} {
		if state == "lost" {
			_, err = f.store.db.Exec(`UPDATE agent_observations SET misses=3 WHERE cluster='ops' AND instance='one' AND kind='cluster'`)
		} else {
			_, err = f.store.db.Exec(`DELETE FROM agent_observations WHERE cluster='ops' AND instance='one' AND kind='cluster'`)
		}
		if err != nil {
			t.Fatal(err)
		}
		m := refreshExecutionTestManifest(t, f, user)
		if !slices.ContainsFunc(m.Resources, func(r targetManifestResource) bool { return r.Ref == "cluster/cluster-id" && r.State == state }) {
			t.Fatalf("missing fixture state %s: %+v", state, m.Resources)
		}
		var online struct{ Targets []GatewayTarget }
		if err := json.Unmarshal(f.http(t, user, "GET", "/v1/targets", "", 200), &online); err != nil || len(online.Targets) != 1 {
			t.Fatalf("read target list: %v %+v", err, online)
		}
		target := online.Targets[0]
		if target.KubernetesAvailable == nil || !*target.KubernetesAvailable || target.ManifestClusterAvailable == nil || *target.ManifestClusterAvailable || target.ManifestRevision != m.Revision {
			t.Fatalf("%s target list: %+v", state, target)
		}
		lifecycleSend(t, client, 16, "doops_agent_prompt", session, map[string]any{"instruction": "重新核对 cluster/cluster-id"})
		finishManifestTestCall(t, client, 16)
		turn := <-turns
		if slices.Contains(turn.Tools, "mcp_doops_k8s_read") || !strings.Contains(turn.Prompt, fmt.Sprintf("目标清单版本 %d", m.Revision)) {
			t.Fatalf("%s ask tools/facts: %+v", state, turn)
		}
		checkManifestAudit(t, f, user, session, ActionAsk, "success", m.Revision, "cluster/cluster-id")
	}
}

func TestManifestMonitorDispatchAndAudit(t *testing.T) {
	stubKubernetesProbe(t, false)
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())
	manifestTestACP(t)
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true, AgentLease: time.Hour})
	user := f.user(t, "manifest-monitor", ScopeGrant{Cluster: "ops", Instance: "one"})
	executionTestSite(t, f, "script-site")
	executionTestSite(t, f, "ai-site")
	const slug = "inspect-manifest"
	skill, err := json.Marshal(skillMutationRequest{
		Slug: slug, Name: slug, Scope: "custom", Description: "Inspect the target", Category: "inspection",
		SkillMD: "---\nname: inspect-manifest\ndescription: inspect target\nexec_mode: auto\n---\nInspect `site/ai-site`.\n",
		Files:   []platformSkillFile{{Path: "scripts/inspect.sh", Content: "#!/bin/sh\nprintf '%s\\n' '{\"status\":\"pass\",\"summary\":\"site/script-site\"}'\n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.http(t, user, "POST", "/v1/skills", string(skill), 200)
	dir := filepath.Join(t.TempDir(), "manifest")
	if err := os.WriteFile(dir, nil, 0600); err != nil {
		t.Fatal(err)
	}
	connectManifestTestAgent(t, f, dir)
	const command = `printf '%s\n' '{"status":"pass","summary":"site/script-site"}'`
	run := func(data map[string]any) (string, monitorRunItemRecord) {
		t.Helper()
		data["targets"] = []monitorTargetRef{{Cluster: "ops", Instance: "one"}}
		body, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		var result struct{ Run monitorRunRecord }
		if err := json.Unmarshal(f.http(t, user, "POST", "/v1/monitor/runs", string(body), 200), &result); err != nil {
			t.Fatal(err)
		}
		_, items := tunnelE2ERunDetail(t, f, user, result.Run.ID)
		if len(items) != 1 {
			t.Fatalf("run %s items: %+v", result.Run.ID, items)
		}
		return result.Run.ID, items[0]
	}
	var revision uint64
	check := func(item monitorRunItemRecord, session string, action GatewayAction, refs ...string) {
		t.Helper()
		if item.Status == "critical" || item.Status == "error" || item.Status == "skipped" {
			t.Fatalf("run item: %+v", item)
		}
		e := checkManifestAudit(t, f, user, session, action, "success", revision, refs...)
		if revision == 0 {
			if item.Evidence["manifest_warning"] != missingExecutionManifest || item.Evidence["manifest_revision"] != nil {
				t.Fatalf("missing manifest evidence: %+v", item.Evidence)
			}
			return
		}
		wantRefs, _ := json.Marshal(e.ResourceRefs)
		gotRefs, _ := json.Marshal(item.Evidence["resource_refs"])
		if item.Evidence["manifest_revision"] != float64(revision) || string(gotRefs) != string(wantRefs) || item.Evidence["manifest_warning"] != nil || readPinnedManifest(t, session).Revision != revision {
			t.Fatalf("monitor evidence %+v differs from audit %+v", item.Evidence, e)
		}
	}
	id, item := run(map[string]any{"command": command})
	check(item, "inspect-cmd-"+id, ActionExec)
	if err := os.Rename(dir, dir+"-blocked"); err != nil {
		t.Fatal(err)
	}
	revision = refreshExecutionTestManifest(t, f, user).Revision
	id, item = run(map[string]any{"command": command})
	check(item, "inspect-cmd-"+id, ActionExec, "site/script-site")
	id, item = run(map[string]any{"prompt": "inspect site/ai-site"})
	check(item, "prompt-inspect-"+id, ActionAsk, "site/ai-site")
	id, item = run(map[string]any{"skills": []string{slug}})
	if item.CollectorMethod != "skill_script" {
		t.Fatalf("script route: %+v", item)
	}
	check(item, "skill-script-"+id+"-"+slug, ActionExec, "site/script-site")
	id, item = run(map[string]any{"skills": []string{slug}, "prompt": "Inspect the deployed service"})
	if item.CollectorMethod != "agent_skill" {
		t.Fatalf("AI route: %+v", item)
	}
	check(item, "skill-inspect-"+id+"-"+slug, ActionAsk, "site/ai-site")
	id, item = run(map[string]any{"skills": []string{slug}, "prompt": "omit terminal event"})
	if item.Status != "critical" || item.Reason != "prompt_failed" || len(item.Checks) != 1 || !strings.Contains(item.Checks[0].Detail, "turn_finished_missing") {
		t.Fatalf("missing terminal event: %+v", item)
	}
	failed := checkManifestAudit(t, f, user, "skill-inspect-"+id+"-"+slug, ActionAsk, "outcome_unknown", revision, "site/ai-site")
	if !strings.Contains(failed.Error, "turn_finished_missing") {
		t.Fatalf("incomplete turn audit: %+v", failed)
	}
	// A non-AI failure must never trigger a different execution or be reported
	// as a missing provider. Force a real audit write failure before dispatch.
	_, err = f.store.db.Exec(`CREATE TRIGGER refuse_manifest_audit BEFORE INSERT ON agent_resource_audit BEGIN SELECT RAISE(FAIL,'audit test'); END`)
	if err != nil {
		t.Fatal(err)
	}
	id, item = run(map[string]any{"skills": []string{slug}, "prompt": "check"})
	if item.Status != "critical" || item.Reason != "prompt_failed" || len(item.Checks) != 1 || !strings.Contains(item.Checks[0].Detail, "target_manifest_audit_unavailable") {
		t.Fatalf("audit failure: %+v", item)
	}
	for _, e := range manifestAuditEvents(t, f, user) {
		if e.Session == "skill-script-"+id+"-"+slug {
			t.Fatal("audit failure started a script")
		}
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER refuse_manifest_audit`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DO_AGENT_URL", "http://127.0.0.1:1")
	id, item = run(map[string]any{"skills": []string{slug}, "prompt": "check"})
	if item.DispatchMode != "agent" || item.CollectorMethod != "skill_script" || item.Evidence["fallback_from_ai"] != "doagent_unavailable" {
		t.Fatalf("unavailable AI: %+v", item)
	}
	checkManifestAudit(t, f, user, "skill-inspect-"+id+"-"+slug, ActionAsk, "error", revision, "site/ai-site")
	check(item, "skill-script-"+id+"-"+slug, ActionExec, "site/script-site")
}

func TestManifestCLIAskAndExec(t *testing.T) {
	stubKubernetesProbe(t, false)
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	manifestTestACP(t)
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true, AgentLease: time.Hour})
	user := f.user(t, "manifest-cli", ScopeGrant{Cluster: "ops", Instance: "one"})
	executionTestSite(t, f, "orders")
	storage := filepath.Join(t.TempDir(), "manifest")
	if err := os.WriteFile(storage, nil, 0600); err != nil {
		t.Fatal(err)
	}
	connectManifestTestAgent(t, f, storage)
	config := writeAccountCLIConfig(t, f.server.URL, user.Plaintext)
	data, err := json.Marshal(map[string]any{"servers": []map[string]string{{"name": "target", "gateway": f.server.URL, "cluster": "ops", "instance": "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "doops")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cli/doops")
	build.Dir = "../../../cli"
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build cli: %v %s", err, output)
	}
	run := func(verb string, flags ...string) (string, string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		args := append([]string{"-session", "cli-" + verb, verb, "--target", "target"}, flags...)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		return string(out), stderr.String(), err
	}
	var rev uint64
	for _, missing := range []bool{true, false} {
		if !missing {
			if err := os.Rename(storage, storage+"-blocked"); err != nil {
				t.Fatal(err)
			}
			rev = refreshExecutionTestManifest(t, f, user).Revision
		}
		for _, c := range []struct {
			verb   string
			args   []string
			action GatewayAction
			output string
		}{
			{"ask", []string{"inspect site/orders"}, ActionAsk, `"pass"`},
			{"exec", []string{"--cmd", "printf 'checked site/orders'"}, ActionExec, "checked site/orders"},
			{"exec", []string{"--bg", "--cmd", "printf 'checked site/orders'"}, ActionExec, "checked site/orders"},
		} {
			out, stderr, err := run(c.verb, c.args...)
			if err != nil || !strings.Contains(out, c.output) || strings.Contains(out, missingExecutionManifest) || strings.Contains(stderr, missingExecutionManifest) != missing {
				t.Fatalf("CLI %s: %v stdout %q stderr %q", c.verb, err, out, stderr)
			}
			var refs []string
			if rev > 0 {
				refs = []string{"site/orders"}
			}
			checkManifestAudit(t, f, user, "cli-"+c.verb, c.action, "success", rev, refs...)
		}
		if missing {
			out, stderr, err := run("ask", "omit terminal event")
			if err == nil || !strings.Contains(out, "turn_finished_missing") || !strings.Contains(stderr, missingExecutionManifest) {
				t.Fatalf("CLI incomplete ask: %v stdout %q stderr %q", err, out, stderr)
			}
			checkManifestAudit(t, f, user, "cli-ask", ActionAsk, "outcome_unknown", 0)
			t.Run("pre-admission rejection", func(t *testing.T) {
				t.Setenv("DO_AGENT_URL", "http://127.0.0.1:1")
				out, stderr, err := run("ask", "inspect site/orders")
				if err == nil || !strings.Contains(out, "doagent_unavailable") || strings.Contains(stderr, missingExecutionManifest) {
					t.Fatalf("CLI failed ask: %v stdout %q stderr %q", err, out, stderr)
				}
				checkManifestAudit(t, f, user, "cli-ask", ActionAsk, "error", 0)
			})
		}
	}
}
