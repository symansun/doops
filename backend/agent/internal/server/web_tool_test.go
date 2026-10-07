package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

func TestWebReadCannotMutateOrChooseSession(t *testing.T) {
	for _, raw := range []string{
		`{"name":"web_read","arguments":{"operation":"deploy","confirm":true,"site_id":"demo"}}`,
		`{"name":"web_read","arguments":{"operation":"check","site_id":"demo","session_id":"other"}}`,
		`{"name":"web","arguments":{"operation":"deploy","site_id":"demo"}}`,
	} {
		response := callK8SMCPTool(t.Context(), k8sMCPRequest{ID: 1, Params: json.RawMessage(raw)}, "web-test")
		if response.Error == nil {
			result, ok := response.Result.(map[string]interface{})
			if !ok || result["isError"] != true {
				t.Fatalf("invalid authority admitted: %#v", response)
			}
		}
	}
}

func TestWebInputConfinedToSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	workspace := filepath.Join(root, "web-test")
	if err := os.MkdirAll(filepath.Join(workspace, "dist"), 0755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"..", "dist/..", "../other", outside, "escape"} {
		if _, err := resolveWebWorkspaceInput("web-test", value, "dist"); err == nil {
			t.Fatalf("input escaped workspace: %q", value)
		}
	}
	expected, _ := filepath.EvalSymlinks(filepath.Join(workspace, "dist"))
	if got, err := resolveWebWorkspaceInput("web-test", "dist", "dist"); err != nil || got != expected {
		t.Fatalf("valid workspace asset rejected: %s, %v", got, err)
	}
	if _, err := resolveWebSession("missing"); err == nil {
		t.Fatal("missing session silently changed workspace")
	}
}

func TestWebExecutionUsesRealNginxValidationHealthAndRollback(t *testing.T) {
	nginx, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("real Nginx is required for DoWeb functional validation")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for bundled DoWeb execution")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspaces", "web-functional")
	available, enabled, snippets := filepath.Join(root, "available"), filepath.Join(root, "enabled"), filepath.Join(root, "snippets")
	for _, path := range []string{workspace, available, enabled, snippets, filepath.Join(root, "logs")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := filepath.Join(root, "nginx.conf")
	user := ""
	if os.Geteuid() == 0 {
		user = "user root;\n"
	}
	configBody := fmt.Sprintf("%spid %s/nginx.pid;\nerror_log %s/logs/error.log;\nevents {}\nhttp { access_log off; include %s/*.conf; }\n", user, root, root, enabled)
	if err := os.WriteFile(config, []byte(configBody), 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(root, "nginx-wrapper")
	reloadFailure := filepath.Join(root, "reject-reload")
	if err := os.WriteFile(wrapper, []byte(fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = '-s' ] && [ -f '%s' ]; then exit 7; fi\nexec '%s' -p '%s' -c '%s' \"$@\"\n", reloadFailure, nginx, root, config)), 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(wrapper).CombinedOutput(); err != nil {
		t.Fatalf("start disposable Nginx: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command(wrapper, "-s", "quit").CombinedOutput(); err != nil {
			t.Errorf("stop disposable Nginx: %v: %s", err, output)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(root, "nginx.pid")); os.IsNotExist(err) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Error("disposable Nginx did not stop")
	})
	layout := map[string]interface{}{
		"base-dir": filepath.Join(root, "state"), "nginx-available-dir": available,
		"nginx-enabled-dir": enabled, "nginx-snippet-dir": snippets,
		"nginx-bin": wrapper, "reload-argv": []string{wrapper, "-s", "reload"}, "listen-port": port,
	}
	layoutPath := filepath.Join(root, "runtime.json")
	encoded, _ := json.Marshal(layout)
	if err := os.WriteFile(layoutPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../skills/doweb/scripts/doweb.mjs")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOWEB_SCRIPT", script)
	t.Setenv("DOWEB_NODE", node)
	t.Setenv("DOWEB_RUNTIME_CONFIG", layoutPath)
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(root, "workspaces"))
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	writeDist := func(name, body string) {
		path := filepath.Join(workspace, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "index.html"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	call := func(params api.WebParams) (string, error) {
		params.SessionID = "web-functional"
		raw, _ := json.Marshal(params)
		return handleWebTool(context.Background(), raw, func(notificationEvent) {})
	}
	assertBody := func(expected string) {
		deadline := time.Now().Add(3 * time.Second)
		client := &http.Client{Timeout: time.Second}
		for time.Now().Before(deadline) {
			response, err := client.Get(url)
			if err == nil {
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if response.StatusCode == 200 && string(body) == expected {
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("Nginx did not serve expected artifact: %q", expected)
	}
	writeDist("dist-a", "release-a")
	result, err := call(api.WebParams{Operation: "deploy", Confirm: true, SiteID: "demo", Domain: "localhost", Dist: "dist-a", HealthURLs: []string{url}})
	if err != nil || !strings.Contains(result, "validation") || !strings.Contains(result, "deployed") {
		t.Fatalf("first publication: %s: %v", result, err)
	}
	assertBody("release-a")
	writeDist("dist-b", "release-b")
	statePath := filepath.Join(root, "state", "state", "demo.json")
	previousState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	evidenceDir := filepath.Join(root, "state", "evidence", "demo")
	if err := os.Rename(evidenceDir, evidenceDir+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidenceDir, []byte("synthetic evidence write failure"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := call(api.WebParams{Operation: "deploy", Confirm: true, SiteID: "demo", Domain: "localhost", Dist: "dist-b", HealthURLs: []string{url}}); err == nil {
		t.Fatal("evidence persistence failure became success")
	}
	assertBody("release-a")
	restoredState, err := os.ReadFile(statePath)
	if err != nil || string(restoredState) != string(previousState) {
		t.Fatalf("failed publication changed the release index: %s: %v", restoredState, err)
	}
	if err := os.Remove(evidenceDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(evidenceDir+".saved", evidenceDir); err != nil {
		t.Fatal(err)
	}
	if _, err := call(api.WebParams{Operation: "deploy", Confirm: true, SiteID: "demo", Domain: "localhost", Dist: "dist-b", HealthURLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	assertBody("release-b")
	writeDist("dist-c", "release-c")
	if _, err := call(api.WebParams{Operation: "deploy", Confirm: true, SiteID: "demo", Domain: "localhost", Dist: "dist-c", HealthURLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	assertBody("release-c")
	if _, err := call(api.WebParams{Operation: "rollback", Confirm: true, SiteID: "demo", HealthURLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	assertBody("release-b")
	if _, err := call(api.WebParams{Operation: "rollback", Confirm: true, SiteID: "demo", HealthURLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	assertBody("release-a")
	if _, err := call(api.WebParams{Operation: "rollback", Confirm: true, SiteID: "demo", HealthURLs: []string{url}}); err == nil {
		t.Fatal("rollback from oldest release selected a newer release")
	}
	assertBody("release-a")
	failedHealth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "synthetic unhealthy site", http.StatusServiceUnavailable)
	}))
	defer failedHealth.Close()
	if _, err := call(api.WebParams{Operation: "deploy", Confirm: true, SiteID: "demo", Domain: "localhost", Dist: "dist-b", HealthURLs: []string{failedHealth.URL}}); err == nil {
		t.Fatal("failed HTTP health became success")
	}
	assertBody("release-a")
	badConfig := filepath.Join(workspace, "invalid.conf")
	if err := os.WriteFile(badConfig, []byte("server { this_is_not_an_nginx_directive; }"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := call(api.WebParams{Operation: "config-set", Confirm: true, SiteID: "demo", ConfigFile: "invalid.conf"}); err == nil {
		t.Fatal("invalid Nginx config became success")
	}
	assertBody("release-a")
	if _, err := call(api.WebParams{Operation: "check", SiteID: "demo", HealthURLs: []string{url}}); err != nil {
		t.Fatalf("site did not recover: %v", err)
	}
	if _, err := call(api.WebParams{Operation: "snippet-set", Confirm: true, SiteID: "demo", SnippetName: "upload", Snippet: "client_max_body_size 20m;", HealthURLs: []string{url}}); err != nil {
		t.Fatalf("natural-language snippet could not be written: %v", err)
	}
	snippetBody, err := os.ReadFile(filepath.Join(snippets, "demo", "upload.conf"))
	if err != nil || string(snippetBody) != "client_max_body_size 20m;" {
		t.Fatalf("candidate snippet was not persisted: %s: %v", snippetBody, err)
	}
	if _, err := call(api.WebParams{Operation: "snippet-set", Confirm: true, SiteID: "demo", SnippetName: "upload", Snippet: "client_max_body_size 50m;", HealthURLs: []string{failedHealth.URL}}); err == nil {
		t.Fatal("snippet update ignored failed HTTP health")
	}
	snippetBody, err = os.ReadFile(filepath.Join(snippets, "demo", "upload.conf"))
	if err != nil || string(snippetBody) != "client_max_body_size 20m;" {
		t.Fatalf("failed snippet update was not restored: %s: %v", snippetBody, err)
	}
	activeConfig, err := os.ReadFile(filepath.Join(available, "doweb-demo.conf"))
	if err != nil {
		t.Fatal(err)
	}
	include := "  include " + filepath.Join(snippets, "demo", "*.conf") + ";\n"
	withoutSnippets := strings.Replace(string(activeConfig), include, "", 1)
	if withoutSnippets == string(activeConfig) {
		t.Fatal("managed snippet include missing from functional fixture")
	}
	if _, err := call(api.WebParams{Operation: "config-set", Confirm: true, SiteID: "demo", Config: withoutSnippets, HealthURLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(api.WebParams{Operation: "snippet-set", Confirm: true, SiteID: "demo", SnippetName: "upload", Snippet: "client_max_body_size 50m;", HealthURLs: []string{url}}); err == nil || !strings.Contains(err.Error(), "does not include the managed snippet") {
		t.Fatalf("unreferenced snippet reported success: %v", err)
	}
	if _, err := call(api.WebParams{Operation: "config-set", Confirm: true, SiteID: "demo", Config: string(activeConfig), HealthURLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	assertBody("release-a")
	if err := os.WriteFile(reloadFailure, []byte("synthetic reload failure"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = call(api.WebParams{Operation: "deploy", Confirm: true, SiteID: "demo", Domain: "localhost", Dist: "dist-b", HealthURLs: []string{url}})
	if !errors.Is(err, errExecutionOutcomeUnknown) {
		t.Fatalf("failed recovery did not report unknown outcome: %v", err)
	}
	if err := os.Remove(reloadFailure); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(wrapper, "-s", "reload").CombinedOutput(); err != nil {
		t.Fatalf("restore disposable runtime after failure injection: %v: %s", err, output)
	}
	assertBody("release-a")
}

func TestApplyWebUsesSignedToolEvidenceAndCannotHideFailure(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	bindApplyCollector(collector, "apply-test")
	input := map[string]interface{}{"operation": "deploy", "site_id": "demo", "dist": "dist", "confirm": true}
	trace := attestedMutationTrace(t, "mcp_doops_web", input)
	collector.calls["publish"] = trace
	if err := collector.requireCompletedToolEvidence(); err != nil {
		t.Fatalf("signed web publication rejected: %v", err)
	}
	if collector.hasUnresolvedMutation() {
		t.Fatal("completed web mutation marked unknown")
	}
	collector.calls["unfinished"] = doagentToolTrace{ToolName: "mcp_doops_web", Status: "running"}
	if !collector.hasUnresolvedMutation() {
		t.Fatal("unfinished website mutation disappeared")
	}
}
