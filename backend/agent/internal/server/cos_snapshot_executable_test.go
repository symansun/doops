package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type cosSnapshotHTTPBucket struct {
	fakeCOSBucket
	url string
}

func (b *cosSnapshotHTTPBucket) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return b.url + "/" + key, nil
}

func cosSnapshotService(bucket cosBucket) cosService {
	return cosService{
		load: func(string, string, string, string) (cosSecret, error) { return cosSecret{Bucket: cosBucketName}, nil },
		open: func(context.Context, cosSecret) (cosBucket, error) { return bucket, nil },
	}
}

func TestCOSSnapshotGatewayToAgentPreservesExecutableBit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	commit := strings.Repeat("a", 40)
	call := cosCall{ActorID: "caller", Cluster: "test", Instance: "node", SessionID: "snapshot", Operation: "sync", Path: commit}
	manifestKey, _ := cosObjectKey(call.ActorID, call.SessionID, "snapshots", commit+"/.doops/manifest.json")
	fileKey, _ := cosObjectKey(call.ActorID, call.SessionID, "snapshots", commit+"/run.sh")
	bucket := &cosSnapshotHTTPBucket{fakeCOSBucket: fakeCOSBucket{objects: map[string][]byte{fileKey: []byte("#!/bin/sh\nexit 0\n")}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bucket.objects[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	bucket.url = srv.URL
	service := cosSnapshotService(bucket)
	service.sync = func(_ context.Context, _, _, session string, files []cosSyncFile) (string, error) {
		_, err := handleCOSSync(mustJSON(t, map[string]interface{}{"session_id": session, "files": files}))
		return "synced", err
	}
	for _, executable := range []bool{true, false, true} {
		body, _ := json.Marshal(cosManifest{Commit: commit, Files: []cosManifestEntry{{Path: "run.sh", Size: int64(len(bucket.objects[fileKey])), Executable: &executable}}})
		bucket.objects[manifestKey] = body
		result, err := service.Handle(context.Background(), call)
		if err != nil || result.WorkspaceStatus != "synced" {
			t.Fatalf("sync = %#v, %v", result, err)
		}
		got, err := os.ReadFile(filepath.Join(root, "snapshot", "run.sh"))
		if err != nil || string(got) != string(bucket.objects[fileKey]) {
			t.Fatalf("content = %q, %v", got, err)
		}
		info, err := os.Stat(filepath.Join(root, "snapshot", "run.sh"))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if executable {
			mode = 0o755
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("mode = %#o, want %#o", info.Mode().Perm(), mode)
		}
	}
}

func TestCOSSnapshotManifestRejectedBeforeDispatch(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, field := range []string{"", `,"executable":null`, `,"executable":1`, `,"executable":"true"`} {
		t.Run(field, func(t *testing.T) {
			call := cosCall{ActorID: "caller", SessionID: "snapshot", Operation: "sync", Path: commit}
			key, _ := cosObjectKey(call.ActorID, call.SessionID, "snapshots", commit+"/.doops/manifest.json")
			body := []byte(fmt.Sprintf(`{"commit":%q,"files":[{"path":"valid","size":1,"executable":false},{"path":"invalid","size":1%s}]}`, commit, field))
			bucket := &fakeCOSBucket{objects: map[string][]byte{key: body}}
			service := cosSnapshotService(bucket)
			dispatches := 0
			service.sync = func(context.Context, string, string, string, []cosSyncFile) (string, error) {
				dispatches++
				return "synced", nil
			}
			_, err := service.Handle(context.Background(), call)
			want := cosErrManifestInvalid
			if field == "" || field == `,"executable":null` {
				want = cosErrPermissionsMissing
			}
			if cosCategory(err) != want || dispatches != 0 {
				t.Fatalf("err = %v, dispatches = %d", err, dispatches)
			}
			if string(bucket.objects[key]) != string(body) {
				t.Fatal("invalid historical snapshot was modified")
			}
		})
	}
}

func TestCOSSnapshotManifestPutRejectsLegacyBeforeChangingObjects(t *testing.T) {
	commit := strings.Repeat("a", 40)
	manifestRel := commit + "/.doops/manifest.json"
	manifestKey, _ := cosObjectKey("caller", "snapshot", "snapshots", manifestRel)
	latestKey, _ := cosObjectKey("caller", "snapshot", "snapshots", "latest.json")
	currentManifest := []byte(fmt.Sprintf(`{"commit":%q,"files":[{"path":"run.sh","size":1,"executable":true}]}`, commit))
	currentLatest := []byte(fmt.Sprintf(`{"commit":%q}`, commit))
	for _, rel := range []string{manifestRel, "  " + manifestRel + "  "} {
		bucket := &fakeCOSBucket{objects: map[string][]byte{manifestKey: currentManifest, latestKey: currentLatest}}
		service := cosSnapshotService(bucket)
		call := cosCall{ActorID: "caller", SessionID: "snapshot", Operation: "put", Kind: "snapshots", Path: rel,
			Body: []byte(fmt.Sprintf(`{"commit":%q,"files":[{"path":"run.sh","size":1}]}`, commit))}
		_, err := service.Handle(context.Background(), call)
		if cosCategory(err) != cosErrPermissionsMissing || len(bucket.puts) != 0 {
			t.Fatalf("legacy upload err=%v puts=%v", err, bucket.puts)
		}
		if string(bucket.objects[manifestKey]) != string(currentManifest) || string(bucket.objects[latestKey]) != string(currentLatest) {
			t.Fatal("rejected legacy upload changed manifest or latest")
		}
	}
	bucket := &fakeCOSBucket{}
	service := cosSnapshotService(bucket)
	_, err := service.Handle(context.Background(), cosCall{ActorID: "caller", SessionID: "snapshot", Operation: "put", Kind: "snapshots", Path: manifestRel, Body: currentManifest})
	if err != nil || len(bucket.puts) != 1 || string(bucket.objects[manifestKey]) != string(currentManifest) {
		t.Fatalf("current manifest put = %v, puts=%v", err, bucket.puts)
	}
}

func TestCOSAgentValidatesAllMetadataBeforeDownloading(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	if err := os.Mkdir(filepath.Join(root, "snapshot"), 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "snapshot", "existing")
	if err := os.WriteFile(file, []byte("keep"), 0700); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte("new")) }))
	defer srv.Close()
	for _, bad := range []map[string]interface{}{
		{"path": "missing", "url": srv.URL, "size": 3},
		{"path": "invalid", "url": srv.URL, "size": 3, "executable": "true"},
		{"path": "negative", "url": srv.URL, "size": -1, "executable": false},
		{"path": "../outside", "url": srv.URL, "size": 3, "executable": false},
		{"path": "bad-url", "url": "http://example.invalid", "size": 3, "executable": false},
	} {
		_, err := handleCOSSync(mustJSON(t, map[string]interface{}{"session_id": "snapshot", "files": []map[string]interface{}{
			{"path": "existing", "url": srv.URL, "size": 3, "executable": false}, bad,
		}}))
		if err == nil || requests.Load() != 0 {
			t.Fatalf("invalid metadata %v: err=%v, requests=%d", bad, err, requests.Load())
		}
		got, readErr := os.ReadFile(file)
		info, statErr := os.Stat(file)
		if readErr != nil || statErr != nil || string(got) != "keep" || info.Mode().Perm() != 0700 {
			t.Fatalf("existing destination changed: %q, %v, %v", got, readErr, statErr)
		}
	}
}

func TestCOSSnapshotUnsupportedAgentIsBlockedBeforeDispatch(t *testing.T) {
	for name, capability := range map[string]interface{}{
		"missing":      nil,
		"wrong-schema": map[string]interface{}{"schemaVersion": "old", "executable": true},
		"disabled":     map[string]interface{}{"schemaVersion": cosSnapshotExecutableSchema, "executable": false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			initialize := gatewayTestInitializeResult("cos-" + name)
			initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})[cosSnapshotExecutableProtocol] = capability
			agent := f.agentWithInitialize(t, initialize)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := f.hub.RunInternalToolCall(ctx, "dev", "local", "doops_cos_sync", map[string]interface{}{"session_id": "cos-rejected", "files": []interface{}{}})
			if cosCategory(err) != cosErrAgentSnapshotUnsupported {
				t.Fatalf("unsupported Agent err = %v", err)
			}
			select {
			case call := <-agent.calls:
				t.Fatalf("unsupported Agent received %#v", call)
			default:
			}
			if got := f.hub.listActiveOperations(); len(got) != 0 {
				t.Fatalf("retained operations: %#v", got)
			}
			events, err := f.store.ListAuditFiltered(AuditFilter{Session: "cos-rejected"})
			if err != nil || len(events) != 1 || events[0].Status != "blocked" || events[0].EndedAt.IsZero() {
				t.Fatalf("audit = %#v, %v", events, err)
			}
		})
	}
}

func TestCOSSnapshotChecksReplacementAgentCapability(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	initialize := gatewayTestInitializeResult("cos-supported")
	initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})[cosSnapshotExecutableProtocol] = cosSnapshotExecutableCapability()
	agent := f.agentWithInitialize(t, initialize)
	completed := make(chan error, 1)
	go func() {
		_, err := f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_cos_sync", map[string]interface{}{"session_id": "cos-supported", "files": []interface{}{}})
		completed <- err
	}()
	agent.complete(t, agent.call(t, "doops_cos_sync", "cos-supported"), "")
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supported COS dispatch did not finish")
	}
	legacy := f.agentWithInitialize(t, gatewayTestInitializeResult("replacement-legacy"))
	_, err := f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_cos_sync", map[string]interface{}{"session_id": "cos-replacement", "files": []interface{}{}})
	if cosCategory(err) != cosErrAgentSnapshotUnsupported {
		t.Fatalf("replacement Agent was not checked: %v", err)
	}
	select {
	case call := <-legacy.calls:
		t.Fatalf("legacy replacement received %#v", call)
	default:
	}
}

func TestCOSSnapshotSyncPreservesOfflineAndProtocolFailure(t *testing.T) {
	commit := strings.Repeat("a", 40)
	call := cosCall{ActorID: "caller", SessionID: "snapshot", Operation: "sync", Path: commit}
	key, _ := cosObjectKey(call.ActorID, call.SessionID, "snapshots", commit+"/.doops/manifest.json")
	bucket := &fakeCOSBucket{objects: map[string][]byte{key: []byte(fmt.Sprintf(`{"commit":%q,"files":[]}`, commit))}}
	service := cosSnapshotService(bucket)
	service.sync = func(context.Context, string, string, string, []cosSyncFile) (string, error) {
		return "", cosFail(cosErrAgentSnapshotUnsupported)
	}
	result, err := service.Handle(context.Background(), call)
	if cosCategory(err) != cosErrAgentSnapshotUnsupported || result.WorkspaceStatus == "synced" {
		t.Fatalf("protocol mismatch was hidden: %#v, %v", result, err)
	}
	service.sync = func(context.Context, string, string, string, []cosSyncFile) (string, error) {
		return "", cosFail(cosErrWorkspaceNotSync)
	}
	result, err = service.Handle(context.Background(), call)
	if err != nil || result.WorkspaceStatus != cosErrWorkspaceNotSync {
		t.Fatalf("offline upload result = %#v, %v", result, err)
	}
}
