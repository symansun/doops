package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestLooksLikeSSHCommand(t *testing.T) {
	for _, command := range []string{"ssh ops@example.com hostname", "/usr/bin/ssh ops@example.com hostname"} {
		if !looksLikeSSHCommand(command) {
			t.Fatalf("expected SSH command: %q", command)
		}
	}
	for _, command := range []string{"echo ssh", "sudo ssh ops@example.com hostname", "sshpass -e ssh ops@example.com hostname"} {
		if looksLikeSSHCommand(command) {
			t.Fatalf("unexpected direct SSH classification: %q", command)
		}
	}
}

func TestNormalizeSSHOptionsRequiresOpenSSHArgv(t *testing.T) {
	got, err := normalizeSSHOptions([]string{"BatchMode=yes", "-p2222"})
	if err != nil {
		t.Fatalf("normalize SSH options: %v", err)
	}
	want := []string{"-oBatchMode=yes", "-p2222"}
	if len(got) != len(want) {
		t.Fatalf("normalized options = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("normalized option %d = %q, want %q", index, got[index], want[index])
		}
	}

	if _, err := normalizeSSHOptions([]string{"BatchMode"}); err == nil {
		t.Fatal("unprefixed SSH option without a value must be rejected")
	}
	for _, rejected := range []string{"ops@evil", "-Wlocalhost:22", "ProxyCommand=nc evil 22", "-oHostName=evil", "-Jjump.example"} {
		if _, err := normalizeSSHOptions([]string{rejected}); err == nil {
			t.Fatalf("SSH option %q was accepted", rejected)
		}
	}
}

func TestStructuredSSHCapabilityRequiresExactSchema(t *testing.T) {
	current := map[string]interface{}{
		"schemaVersion":  "doops.sh/structured-ssh/v1",
		"tool":           "doops_ssh",
		"structuredArgv": true,
	}
	if !protocolCapabilityCompatible("structuredSSH", current) {
		t.Fatal("current structured SSH capability must be accepted")
	}
	for _, legacy := range []interface{}{
		nil,
		"doops.sh/structured-ssh/v1",
		map[string]interface{}{
			"schemaVersion":  "doops.sh/structured-ssh/v1",
			"tool":           "doops_shell",
			"structuredArgv": true,
		},
		map[string]interface{}{
			"schemaVersion": "doops.sh/structured-ssh/v1",
			"tool":          "doops_ssh",
		},
	} {
		if protocolCapabilityCompatible("structuredSSH", legacy) {
			t.Fatalf("legacy or incomplete capability was accepted: %#v", legacy)
		}
	}
}

func TestCLISSHRawAndQuietSuppressBannerHeartbeatAndTerminalText(t *testing.T) {
	const jsonPayload = `{"host":"app-1","ok":true}`
	const progressTick = "[agent] ssh progress heartbeat"
	const legacyHeartbeat = "\r\x1b[K[agent] ⏳ 命令后台执行中... (耗时 5s)"

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			switch req["method"] {
			case "initialize":
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]interface{}{
						"protocolVersion": "2024-11-05",
						"capabilities": map[string]interface{}{
							"protocols": map[string]interface{}{
								"structuredSSH": map[string]interface{}{
									"schemaVersion":  "doops.sh/structured-ssh/v1",
									"tool":           "doops_ssh",
									"structuredArgv": true,
								},
							},
						},
					},
				})
			case "tools/call":
				params, _ := req["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				cmdList, _ := args["command"].([]interface{})
				mode := ""
				if len(cmdList) > 0 {
					mode, _ = cmdList[0].(string)
				}
				sendNotice := func(kind, data string) {
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"method":  "notifications/message",
						"params": map[string]interface{}{
							"request_id": req["id"],
							"kind":       kind,
							"data":       data,
						},
					})
				}
				switch mode {
				case "emit-json":
					sendNotice("progress", progressTick)
					sendNotice("raw", jsonPayload)
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "Operation complete."}},
							"structuredContent": map[string]interface{}{
								"kind":     "doops.command-result",
								"exitCode": 0,
								"streamed": true,
							},
						},
					})
				case "legacy-emit-json":
					sendNotice("raw", legacyHeartbeat)
					sendNotice("raw", jsonPayload+"\n")
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "SSH command finished."}},
						},
					})
				case "silent-ok":
					sendNotice("progress", progressTick)
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "SSH command finished."}},
						},
					})
				case "fail-cmd":
					sendNotice("progress", progressTick)
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content":     []map[string]interface{}{{"type": "text", "text": "SSH command failed: exit status 7"}},
							"isError":     true,
							"sshExitCode": 7,
						},
					})
				}
			}
		}
	}))
	defer gateway.Close()

	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "dev", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "synthetic-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}

	bin := buildDoopsCLIBinary(t)
	run := func(args ...string) (string, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}

	// Default interactive mode keeps [TARGETING] banner and kind="progress".
	defaultOut, _, err := run("-session", "ssh-default", "ssh", "--target", "test", "--host", "ops@app.internal", "--", "emit-json")
	if err != nil {
		t.Fatalf("default ssh failed: %v", err)
	}
	if !strings.Contains(defaultOut, "[TARGETING]") || !strings.Contains(defaultOut, progressTick) {
		t.Fatalf("default ssh must keep banner and progress, got: %q", defaultOut)
	}

	// --raw and --quiet emit pure JSON without banner, progress, or wrapper text.
	for _, flagName := range []string{"--raw", "--quiet"} {
		t.Run("structured_"+flagName, func(t *testing.T) {
			stdout, stderr, err := run("-session", "ssh-raw", "ssh", flagName, "--target", "test", "--host", "ops@app.internal", "--", "emit-json")
			if err != nil {
				t.Fatalf("ssh %s failed: %v (stderr=%q)", flagName, err, stderr)
			}
			if stdout != jsonPayload {
				t.Fatalf("ssh %s stdout = %q, want exact %q", flagName, stdout, jsonPayload)
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
				t.Fatalf("ssh %s stdout not valid JSON: %v (%q)", flagName, err, stdout)
			}
		})
	}

	// Legacy Agent compatibility with --raw.
	legacyOut, legacyErr, err := run("-session", "ssh-legacy", "ssh", "--raw", "--target", "test", "--host", "ops@app.internal", "--", "legacy-emit-json")
	if err != nil {
		t.Fatalf("legacy ssh --raw failed: %v (stderr=%q)", err, legacyErr)
	}
	if legacyOut != jsonPayload+"\n" {
		t.Fatalf("legacy ssh --raw stdout = %q, want %q", legacyOut, jsonPayload+"\n")
	}

	// -verbose goes to stderr in --raw mode.
	verboseOut, verboseErr, err := run("-verbose", "-session", "ssh-verbose", "ssh", "--raw", "--target", "test", "--host", "ops@app.internal", "--", "emit-json")
	if err != nil {
		t.Fatalf("verbose ssh --raw failed: %v", err)
	}
	if verboseOut != jsonPayload {
		t.Fatalf("verbose ssh --raw polluted stdout: %q", verboseOut)
	}
	if !strings.Contains(verboseErr, "[CLIENT:") {
		t.Fatalf("verbose ssh --raw expected client logs on stderr, got: %q", verboseErr)
	}

	// Silent command suppresses legacy wrapper text.
	silentOut, silentErr, err := run("-session", "ssh-silent", "ssh", "--raw", "--target", "test", "--host", "ops@app.internal", "--", "silent-ok")
	if err != nil {
		t.Fatalf("silent ssh --raw failed: %v (stderr=%q)", err, silentErr)
	}
	if silentOut != "" {
		t.Fatalf("silent ssh --raw must keep stdout empty, got: %q", silentOut)
	}

	// Remote failure and missing target route errors to stderr in --raw mode.
	failOut, failErr, err := run("-session", "ssh-fail", "ssh", "--raw", "--target", "test", "--host", "ops@app.internal", "--", "fail-cmd")
	if err == nil {
		t.Fatal("expected failing ssh --raw to exit non-zero")
	}
	if failOut != "" || !strings.Contains(failErr, "exit status 7") {
		t.Fatalf("failing ssh --raw stdout=%q stderr=%q", failOut, failErr)
	}

	missingOut, missingErr, err := run("-session", "ssh-missing", "ssh", "--raw", "--target", "no-such-target", "--host", "ops@app.internal", "--", "emit-json")
	if err == nil {
		t.Fatal("expected missing target to fail")
	}
	if missingOut != "" || !strings.Contains(missingErr, `target "no-such-target" is not configured`) {
		t.Fatalf("missing target in ssh --raw must report on stderr only: stdout=%q stderr=%q", missingOut, missingErr)
	}
}
