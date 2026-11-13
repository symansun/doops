package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorageModeDefaultsToCOS(t *testing.T) {
	mode, err := normalizeStorageMode("")
	if err != nil || mode != "cos" {
		t.Fatalf("default mode = %q, %v", mode, err)
	}
	mode, err = normalizeStorageMode("gateway")
	if err != nil || mode != "gateway" {
		t.Fatalf("gateway mode = %q, %v", mode, err)
	}
	if _, err := normalizeStorageMode("s3"); err == nil {
		t.Fatal("unknown storage mode was accepted")
	}
}

func TestCOSPushRejectsMissingTokenAndCredentialBeforeTransfer(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	transfer := &recordingTransfer{t: t}
	_, err := pushSnapshot(Server{Name: "demo"}, src, "", false, nil, "sess_1", nil, "cos", "cos-bucket", "", transfer)
	if err == nil || err.Error() != "gateway_auth_missing" || transfer.calls != 0 {
		t.Fatalf("missing token error = %v, calls = %d", err, transfer.calls)
	}
	transfer.probeErr = errString("cos_bucket_unavailable")
	_, err = pushSnapshot(Server{Name: "demo"}, src, "", false, nil, "sess_1", nil, "cos", "", "token", transfer)
	if err == nil || err.Error() != "cos_bucket_unavailable" || transfer.calls != 1 {
		t.Fatalf("empty credential still uses cos, error = %v, calls = %d", err, transfer.calls)
	}
}

func TestCOSPushStaysOnCOSWhenProbeFails(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	transfer := &recordingTransfer{t: t, probeErr: errString("cos_bucket_unavailable")}
	_, err := pushSnapshot(Server{Name: "demo", Gateway: "https://gateway.example", Cluster: "dev", Instance: "local"}, src, "", false, nil, "sess_1", nil, "cos", "cos-bucket", "token", transfer)
	if err == nil || err.Error() != "cos_bucket_unavailable" {
		t.Fatalf("probe error = %v", err)
	}
	if transfer.calls != 1 || transfer.puts != 0 {
		t.Fatalf("transfer calls = %d puts = %d", transfer.calls, transfer.puts)
	}
}

func TestCOSPullRejectsEscapingManifestPath(t *testing.T) {
	transfer := &recordingTransfer{t: t}
	transfer.objects = map[string][]byte{
		"snapshots/latest.json": []byte(`{"commit":"` + strings.Repeat("a", 40) + `"}`),
		"snapshots/" + strings.Repeat("a", 40) + "/.doops/manifest.json": []byte(`{"commit":"` + strings.Repeat("a", 40) + `","files":[{"path":"../outside","size":1,"executable":false}]}`),
	}
	err := pullCOS(Server{}, t.TempDir(), "sess_1", "cos-bucket", "token", "", transfer)
	if err == nil || err.Error() != "cos_key_rejected" {
		t.Fatalf("error = %v", err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

type recordingTransfer struct {
	t        *testing.T
	probeErr error
	calls    int
	puts     int
	objects  map[string][]byte
}

func (r *recordingTransfer) Probe() error {
	r.calls++
	return r.probeErr
}

func (r *recordingTransfer) ProbeSnapshot() error { return r.Probe() }

func (r *recordingTransfer) Put(kind, rel string, body []byte) (cosObjectResult, error) {
	r.puts++
	if r.objects == nil {
		r.objects = map[string][]byte{}
	}
	r.objects[kind+"/"+rel] = append([]byte(nil), body...)
	return cosObjectResult{Storage: "cos", Bucket: cosBucketName, Key: kind + "/" + rel, URL: "https://example.invalid/" + rel}, nil
}

func (r *recordingTransfer) PutFile(kind, rel, _ string, size int64) (cosObjectResult, error) {
	if size > cosGatewayPutMaxBytes {
		r.t.Fatalf("test double received an unexpected large put of %d", size)
	}
	return r.Put(kind, rel, []byte("x"))
}

func (r *recordingTransfer) Get(kind, rel string) ([]byte, error) {
	body, ok := r.objects[kind+"/"+rel]
	if !ok {
		return nil, errString("cos_object_missing")
	}
	return append([]byte(nil), body...), nil
}

func (r *recordingTransfer) PresignGet(kind, rel string) (cosObjectResult, error) {
	r.t.Fatalf("presign get %s/%s was not expected", kind, rel)
	return cosObjectResult{}, nil
}

func (r *recordingTransfer) Sync(string) (string, error) {
	return "synced", nil
}

func TestDownloadPresignedStopsWhenTheBodyStalls(t *testing.T) {
	previous := cosIOStall
	cosIOStall = 200 * time.Millisecond
	t.Cleanup(func() { cosIOStall = previous })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n"))
		time.Sleep(time.Second)
	}()

	started := time.Now()
	err = downloadPresigned("http://"+listener.Addr().String()+"/snapshot", filepath.Join(t.TempDir(), "snapshot"), 4, false)
	if err == nil {
		t.Fatal("stalled download succeeded")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("stalled download waited %s", time.Since(started))
	}
}
