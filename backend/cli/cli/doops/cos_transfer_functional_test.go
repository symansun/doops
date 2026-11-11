package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCOSPushUsesResolvedSessionForEveryRequest(t *testing.T) {
	for _, tc := range []struct {
		name, session, dest, expected string
	}{
		{name: "generated"},
		{name: "destination", dest: "/root/ws/destination-session", expected: "destination-session"},
		{name: "explicit", session: "explicit-session", dest: "/root/ws/ignored", expected: "explicit-session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			t.Setenv("DOOPS_CLI_CALL_TIMEOUT", "5s")
			captured := make(chan []map[string]interface{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var calls []map[string]interface{}
				defer func() { captured <- calls }()
				for {
					var request struct {
						ID     interface{} `json:"id"`
						Method string      `json:"method"`
						Params struct {
							Arguments map[string]interface{} `json:"arguments"`
						} `json:"params"`
					}
					if conn.ReadJSON(&request) != nil {
						return
					}
					result := map[string]interface{}{"capabilities": map[string]interface{}{"gatewayProtocols": map[string]interface{}{
						"cosSnapshotExecutable": map[string]interface{}{"schemaVersion": cosSnapshotExecutableSchema, "executable": true},
					}}}
					if request.Method == "tools/call" {
						calls = append(calls, request.Params.Arguments)
						result = map[string]interface{}{"structuredContent": map[string]interface{}{"storage": "cos", "workspace_status": "synced"}}
					}
					if conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": result}) != nil {
						return
					}
				}
			}))
			defer srv.Close()
			src := t.TempDir()
			if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("session fixture\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			commit, err := pushCOS(Server{Name: "test", Gateway: srv.URL, Cluster: "test", Instance: "node"}, src, tc.dest, false, tc.session, "", "synthetic-token", nil)
			if err != nil {
				t.Fatal(err)
			}
			var calls []map[string]interface{}
			select {
			case calls = <-captured:
			case <-time.After(5 * time.Second):
				t.Fatal("COS transport did not close after push")
			}
			if len(calls) < 5 || calls[0]["operation"] != "probe" || calls[len(calls)-1]["operation"] != "sync" {
				t.Fatalf("incomplete push: %#v", calls)
			}
			expected := tc.expected
			if expected == "" {
				expected, _ = calls[0]["session_id"].(string)
				if !strings.HasPrefix(expected, "ses_") {
					t.Fatalf("generated session = %q", expected)
				}
			}
			for _, call := range calls {
				if call["session_id"] != expected {
					t.Fatalf("%v session = %v, want %q", call["operation"], call["session_id"], expected)
				}
			}
			if calls[len(calls)-1]["path"] != commit {
				t.Fatalf("sync commit differs from pushed commit %s", commit)
			}
		})
	}
}

type snapshotHTTPTransfer struct {
	objects map[string][]byte
	baseURL string
}

func (r *snapshotHTTPTransfer) Probe() error         { return nil }
func (r *snapshotHTTPTransfer) ProbeSnapshot() error { return nil }
func (r *snapshotHTTPTransfer) Put(kind, rel string, body []byte) (cosObjectResult, error) {
	r.objects[kind+"/"+rel] = append([]byte(nil), body...)
	return cosObjectResult{}, nil
}
func (r *snapshotHTTPTransfer) PutFile(kind, rel, name string, _ int64) (cosObjectResult, error) {
	body, err := os.ReadFile(name)
	if err != nil {
		return cosObjectResult{}, err
	}
	return r.Put(kind, rel, body)
}
func (r *snapshotHTTPTransfer) Get(kind, rel string) ([]byte, error) {
	return r.objects[kind+"/"+rel], nil
}
func (r *snapshotHTTPTransfer) PresignGet(kind, rel string) (cosObjectResult, error) {
	return cosObjectResult{URL: r.baseURL + "/" + kind + "/" + rel}, nil
}
func (r *snapshotHTTPTransfer) Sync(string) (string, error) { return "synced", nil }

func TestCOSSnapshotRoundTripPreservesGitExecutableBit(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	transfer := &snapshotHTTPTransfer{objects: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, ok := transfer.objects[strings.TrimPrefix(req.URL.Path, "/")]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	transfer.baseURL = srv.URL
	const name = "run.sh"
	const content = "#!/bin/sh\nprintf 'snapshot works'\n"
	for _, mode := range []os.FileMode{0o755, 0o644, 0o750, 0o654} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(src, name), mode); err != nil {
			t.Fatal(err)
		}
		commit, err := createWorkspaceSnapshotCommit(src, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := uploadCOSSnapshot(src, []string{name}, commit, transfer); err != nil {
			t.Fatal(err)
		}
		var manifest map[string]interface{}
		if err := json.Unmarshal(transfer.objects["snapshots/"+commit+"/.doops/manifest.json"], &manifest); err != nil {
			t.Fatal(err)
		}
		if err := pullCOS(Server{}, dest, "snapshot-session", "", "synthetic-token", "", transfer); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(dest, name))
		if err != nil {
			t.Fatal(err)
		}
		expectedMode := os.FileMode(0o644)
		if mode&0o100 != 0 {
			expectedMode = 0o755
		}
		if info.Mode().Perm() != expectedMode {
			t.Fatalf("round trip mode = %#o, want %#o; source=%#o manifest=%v", info.Mode().Perm(), expectedMode, mode, manifest)
		}
		body, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(body) != content {
			t.Fatalf("round trip content = %q, %v", body, err)
		}
		output, err := exec.Command(filepath.Join(dest, name)).CombinedOutput()
		if expectedMode == 0o755 {
			if err != nil || string(output) != "snapshot works" {
				t.Fatalf("restored script did not run: output=%q err=%v", output, err)
			}
		} else if err == nil || !os.IsPermission(err) {
			t.Fatalf("non-executable snapshot could still run: %q err=%v", output, err)
		}
	}
}

func TestCOSPullRejectsInvalidExecutableMetadataBeforeWriting(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, field := range []string{"", `,"executable":null`, `,"executable":1`, `,"executable":"true"`, `,"executable":{}`} {
		t.Run(field, func(t *testing.T) {
			dest := t.TempDir()
			original := filepath.Join(dest, "existing.txt")
			if err := os.WriteFile(original, []byte("keep me"), 0o600); err != nil {
				t.Fatal(err)
			}
			transfer := &recordingTransfer{t: t, objects: map[string][]byte{
				"snapshots/" + commit + "/.doops/manifest.json": []byte(`{"commit":"` + commit + `","files":[{"path":"existing.txt","size":7,"executable":false},{"path":"invalid.txt","size":1` + field + `}]}`),
			}}
			err := pullCOS(Server{}, dest, "snapshot-session", "", "synthetic-token", commit, transfer)
			if err == nil || !(strings.Contains(err.Error(), "cos_manifest_invalid") || strings.Contains(err.Error(), "cos_snapshot_permissions_missing")) {
				t.Fatalf("mode %q error = %v", field, err)
			}
			body, err := os.ReadFile(original)
			if err != nil || string(body) != "keep me" {
				t.Fatalf("invalid manifest changed an existing file: %q, %v", body, err)
			}
		})
	}
}

func TestCOSPullRejectsLegacySnapshotWithReuploadInstruction(t *testing.T) {
	commit := strings.Repeat("a", 40)
	transfer := &recordingTransfer{t: t, objects: map[string][]byte{
		"snapshots/" + commit + "/.doops/manifest.json": []byte(`{"commit":"` + commit + `","files":[{"path":"run.sh","size":1}]}`),
	}}
	err := pullCOS(Server{}, t.TempDir(), "snapshot-session", "", "synthetic-token", commit, transfer)
	if err == nil || !strings.Contains(err.Error(), "cos_snapshot_permissions_missing") || !strings.Contains(err.Error(), "push again") {
		t.Fatalf("legacy snapshot error = %v", err)
	}
}

func TestCOSSnapshotRejectsUnsupportedTreeBeforeUpload(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "regular.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("regular.sh", filepath.Join(root, "link.sh")); err != nil {
		t.Fatal(err)
	}
	commit, err := createWorkspaceSnapshotCommit(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	transfer := &recordingTransfer{t: t}
	_, err = uploadCOSSnapshot(root, []string{"regular.sh", "link.sh"}, commit, transfer)
	if err == nil || !strings.Contains(err.Error(), "cos_snapshot_file_type_unsupported") || transfer.puts != 0 {
		t.Fatalf("unsupported tree error=%v puts=%d", err, transfer.puts)
	}
}

func TestCOSSnapshotExecutableComesFromTheCommittedTree(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "run.sh")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	commit, err := createWorkspaceSnapshotCommit(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	transfer := &snapshotHTTPTransfer{objects: map[string][]byte{}}
	if _, err := uploadCOSSnapshot(root, []string{"run.sh"}, commit, transfer); err != nil {
		t.Fatal(err)
	}
	var manifest cosSnapshotManifest
	if err := json.Unmarshal(transfer.objects["snapshots/"+commit+"/.doops/manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Executable == nil || !*manifest.Files[0].Executable {
		t.Fatalf("permissions came from mutable filesystem instead of commit: %+v", manifest)
	}
}
