package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	cosBucketName         = "doops-1253657668"
	cosGatewayPutMaxBytes = int64(16 << 20)
)

type cosObjectResult struct {
	Storage         string            `json:"storage"`
	Bucket          string            `json:"bucket"`
	Key             string            `json:"key"`
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers"`
	Size            int64             `json:"size"`
	Commit          string            `json:"commit"`
	ContentB64      string            `json:"content_b64"`
	WorkspaceStatus string            `json:"workspace_status"`
}

type cosTransfer interface {
	Probe() error
	Put(kind, rel string, body []byte) (cosObjectResult, error)
	PutFile(kind, rel, filePath string, size int64) (cosObjectResult, error)
	Get(kind, rel string) ([]byte, error)
	PresignGet(kind, rel string) (cosObjectResult, error)
	Sync(commit string) (string, error)
}

func normalizeStorageMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "cos":
		return "cos", nil
	case "gateway":
		return "gateway", nil
	default:
		return "", fmt.Errorf("storage must be cos or gateway")
	}
}

func cosCategories() []string {
	return []string{
		"cos_not_configured",
		"cos_credential_missing",
		"cos_credential_invalid",
		"cos_bucket_mismatch",
		"cos_bucket_unavailable",
		"cos_key_rejected",
		"cos_gateway_put_limit",
		"cos_size_mismatch",
		"cos_put_failed",
		"cos_object_missing",
		"cos_manifest_invalid",
		"cos_workspace_not_synced",
		"cos_workspace_not_updated",
		"audit_unavailable",
		"forbidden",
	}
}

func cosError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, category := range cosCategories() {
		if strings.Contains(message, category) {
			return fmt.Errorf("%s", category)
		}
	}
	return err
}

type gatewayCOS struct {
	client *MCPClient
}

func newGatewayCOS(server Server, token, session string) (*gatewayCOS, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("gateway_auth_missing")
	}
	client := NewMCPClient(server, NewSessionStore(), session, false)
	client.Token = token
	return &gatewayCOS{client: client}, nil
}

func (g *gatewayCOS) call(operation, kind, rel string, extra map[string]interface{}) (cosObjectResult, error) {
	args := map[string]interface{}{
		"operation": operation,
		"kind":      kind,
		"path":      rel,
	}
	for key, value := range extra {
		args[key] = value
	}
	var result cosObjectResult
	if err := g.client.CallStructured("doops_cos", args, &result); err != nil {
		return cosObjectResult{}, cosError(err)
	}
	if result.Storage != "" && result.Storage != "cos" {
		return cosObjectResult{}, fmt.Errorf("cos_credential_invalid")
	}
	return result, nil
}

func (g *gatewayCOS) Probe() error {
	_, err := g.call("probe", "", "latest.json", nil)
	return err
}

func (g *gatewayCOS) Put(kind, rel string, body []byte) (cosObjectResult, error) {
	if int64(len(body)) > cosGatewayPutMaxBytes {
		return cosObjectResult{}, fmt.Errorf("cos_gateway_put_limit")
	}
	return g.call("put", kind, rel, map[string]interface{}{
		"content_b64": base64.StdEncoding.EncodeToString(body),
		"size":        int64(len(body)),
	})
}

func (g *gatewayCOS) PutFile(kind, rel, filePath string, size int64) (cosObjectResult, error) {
	if size <= cosGatewayPutMaxBytes {
		body, err := os.ReadFile(filePath)
		if err != nil {
			return cosObjectResult{}, err
		}
		return g.Put(kind, rel, body)
	}
	presign, err := g.call("presign_put", kind, rel, map[string]interface{}{"size": size})
	if err != nil {
		return cosObjectResult{}, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return cosObjectResult{}, err
	}
	defer file.Close()
	if err := putPresigned(presign, file, size); err != nil {
		return cosObjectResult{}, err
	}
	return g.call("confirm", kind, rel, map[string]interface{}{"size": size})
}

func (g *gatewayCOS) Get(kind, rel string) ([]byte, error) {
	result, err := g.call("get", kind, rel, nil)
	if err != nil {
		return nil, err
	}
	if result.ContentB64 == "" {
		return []byte{}, nil
	}
	return base64.StdEncoding.DecodeString(result.ContentB64)
}

func (g *gatewayCOS) PresignGet(kind, rel string) (cosObjectResult, error) {
	return g.call("presign_get", kind, rel, nil)
}

func (g *gatewayCOS) Sync(commit string) (string, error) {
	result, err := g.call("sync", "snapshots", commit, nil)
	if err != nil {
		return "", err
	}
	return result.WorkspaceStatus, nil
}

func (g *gatewayCOS) Close() {
	if g != nil && g.client != nil {
		g.client.Close()
	}
}

func putPresigned(presign cosObjectResult, body io.Reader, size int64) error {
	method := presign.Method
	if method == "" {
		method = http.MethodPut
	}
	req, err := http.NewRequest(method, presign.URL, body)
	if err != nil {
		return fmt.Errorf("cos_put_failed")
	}
	req.ContentLength = size
	for key, value := range presign.Headers {
		if strings.EqualFold(key, "Host") {
			continue
		}
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("cos_put_failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("cos_put_failed")
	}
	return nil
}

func downloadPresigned(rawURL, dest string, size int64) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("cos_object_missing")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("cos_object_missing")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cos_object_missing")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer output.Close()
	limit := size
	if limit < 0 {
		limit = 0
	}
	written, err := io.Copy(output, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("cos_object_missing")
	}
	if written != size {
		return fmt.Errorf("cos_size_mismatch")
	}
	return nil
}

func localRelPath(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || strings.Contains(rel, "\\") || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("cos_key_rejected")
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("cos_key_rejected")
	}
	dest := filepath.Join(root, filepath.FromSlash(clean))
	relative, err := filepath.Rel(root, dest)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("cos_key_rejected")
	}
	return dest, nil
}

type cosManifestFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type cosSnapshotManifest struct {
	Commit string            `json:"commit"`
	Files  []cosManifestFile `json:"files"`
}

func uploadCOSSnapshot(root string, files []string, commit string, transfer cosTransfer) (string, error) {
	manifest := cosSnapshotManifest{Commit: commit}
	for _, rel := range files {
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			continue
		}
		if _, err := transfer.PutFile("snapshots", commit+"/"+rel, full, info.Size()); err != nil {
			return "", err
		}
		manifest.Files = append(manifest.Files, cosManifestFile{Path: rel, Size: info.Size()})
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if _, err := transfer.Put("snapshots", commit+"/.doops/manifest.json", body); err != nil {
		return "", err
	}
	pointer, err := json.Marshal(map[string]string{"commit": commit})
	if err != nil {
		return "", err
	}
	if _, err := transfer.Put("snapshots", "latest.json", pointer); err != nil {
		return "", err
	}
	return transfer.Sync(commit)
}

func runCOSWrite(server Server, session, credential, token, relPath string, body []byte, transfer cosTransfer) (cosObjectResult, error) {
	modeTransfer, closeTransfer, err := ensureCOSTransfer(server, session, token, transfer)
	if err != nil {
		return cosObjectResult{}, err
	}
	defer closeTransfer()
	if err := modeTransfer.Probe(); err != nil {
		return cosObjectResult{}, err
	}
	if int64(len(body)) <= cosGatewayPutMaxBytes {
		return modeTransfer.Put("files", relPath, body)
	}
	file, err := os.CreateTemp("", "doops-cos-write-")
	if err != nil {
		return cosObjectResult{}, err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(body); err != nil {
		file.Close()
		return cosObjectResult{}, err
	}
	if err := file.Close(); err != nil {
		return cosObjectResult{}, err
	}
	return modeTransfer.PutFile("files", relPath, name, int64(len(body)))
}

func runCOSRead(server Server, session, credential, token, relPath string, transfer cosTransfer) (cosObjectResult, error) {
	modeTransfer, closeTransfer, err := ensureCOSTransfer(server, session, token, transfer)
	if err != nil {
		return cosObjectResult{}, err
	}
	defer closeTransfer()
	if err := modeTransfer.Probe(); err != nil {
		return cosObjectResult{}, err
	}
	return modeTransfer.PresignGet("files", relPath)
}

func pullCOS(server Server, dest, session, credential, token, commit string, transfer cosTransfer) error {
	if strings.TrimSpace(session) == "" {
		return fmt.Errorf("session ID 必传：请通过 -session 指定要拉取的远端工作区")
	}
	if strings.TrimSpace(dest) == "" {
		dest = session
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("解析目标路径失败: %v", err)
	}
	modeTransfer, closeTransfer, err := ensureCOSTransfer(server, session, token, transfer)
	if err != nil {
		return err
	}
	defer closeTransfer()
	if err := modeTransfer.Probe(); err != nil {
		return err
	}
	if strings.TrimSpace(commit) == "" {
		pointer, err := modeTransfer.Get("snapshots", "latest.json")
		if err != nil {
			return err
		}
		var latest struct {
			Commit string `json:"commit"`
		}
		if err := json.Unmarshal(pointer, &latest); err != nil || strings.TrimSpace(latest.Commit) == "" {
			return fmt.Errorf("cos_manifest_invalid")
		}
		commit = latest.Commit
	}
	raw, err := modeTransfer.Get("snapshots", commit+"/.doops/manifest.json")
	if err != nil {
		return err
	}
	var manifest cosSnapshotManifest
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Commit != commit {
		return fmt.Errorf("cos_manifest_invalid")
	}
	if err := os.MkdirAll(absDest, 0o755); err != nil {
		return err
	}
	for _, file := range manifest.Files {
		target, err := localRelPath(absDest, file.Path)
		if err != nil {
			return err
		}
		presign, err := modeTransfer.PresignGet("snapshots", commit+"/"+file.Path)
		if err != nil {
			return err
		}
		if err := downloadPresigned(presign.URL, target, file.Size); err != nil {
			return err
		}
	}
	fmt.Printf("✅ 已从 COS 拉取快照 %s 到本地: %s\n", commit, absDest)
	return nil
}

func ensureCOSTransfer(server Server, session, token string, transfer cosTransfer) (cosTransfer, func(), error) {
	if strings.TrimSpace(token) == "" {
		return nil, func() {}, fmt.Errorf("gateway_auth_missing")
	}
	if transfer != nil {
		return transfer, func() {}, nil
	}
	client, err := newGatewayCOS(server, token, session)
	if err != nil {
		return nil, func() {}, err
	}
	return client, client.Close, nil
}

func pushCOS(server Server, src, dest string, dryRun bool, session, credential, token string, transfer cosTransfer) (string, error) {
	modeTransfer := transfer
	if !dryRun && modeTransfer == nil {
		client, err := newGatewayCOS(server, token, session)
		if err != nil {
			return "", err
		}
		defer client.Close()
		modeTransfer = client
	}
	return pushSnapshot(server, src, dest, dryRun, nil, session, nil, "cos", credential, token, modeTransfer)
}
