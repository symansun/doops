package server

// Ephemeral process-private runtime materialization for opaque credentials
// (CAPABILITY_PLUGIN_INTEGRATION_SPEC.md §3.5 / AliDNS & API-key Skills).
//
// Follows the same zero-leak architecture as native_helm_context.go:
//   - Decrypted payloads travel ONLY from Gateway to an enrolled target Agent
//     over the private internal doops_credential_materialize channel.
//   - On the Agent, the payload is written to a 0600 JSON file inside a 0700
//     directory outside the session workspace (/tmp/doops-runtime-opaque-*).
//   - Skill scripts receive ONLY the file path (DOOPS_CRED_FILE_<KEY>), never
//     secret values in argv, prompts, workspace files, or audit logs.
//   - Cleanup zeroes the file bytes before unlinking the directory.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const runtimeOpaqueContextKind = "runtime-opaque"

type runtimeOpaqueRecord struct {
	SessionID    string
	CredentialID string
	GrantID      string
	VersionID    string
	Namespace    string
	ResourceVer  string
	Digest       string
	Dir          string
	FilePath     string
}

var runtimeOpaqueContexts = struct {
	sync.Mutex
	byPath map[string]*runtimeOpaqueRecord
}{byPath: make(map[string]*runtimeOpaqueRecord)}

func validRuntimeOpaqueEnvPath(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned != strings.TrimSpace(path) || !filepath.IsAbs(cleaned) {
		return false
	}
	base := filepath.Base(cleaned)
	parent := filepath.Base(filepath.Dir(cleaned))
	return base == "env.json" && strings.HasPrefix(parent, "doops-runtime-opaque-")
}

func materializeRuntimeEnvOnly(request *CredentialMaterializeRequest) {
	request.RuntimeEnvOnly = true
}

func materializeRuntimeOpaqueCredential(
	ctx context.Context,
	req CredentialMaterializeRequest,
	data map[string]string,
	digest string,
) (CredentialMaterialization, error) {
	if ctx.Err() != nil ||
		req.CredentialType != CredentialTypeOpaque ||
		req.Scope.Use != CredentialUseOpaqueSecret ||
		!validCredentialSHA256Digest(digest) ||
		len(data) == 0 {
		return CredentialMaterialization{}, errors.New("runtime_opaque_context_invalid")
	}
	dir, err := os.MkdirTemp("", "doops-runtime-opaque-")
	if err != nil {
		return CredentialMaterialization{}, errors.New("runtime_opaque_context_unavailable")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return CredentialMaterialization{}, errors.New("runtime_opaque_context_unavailable")
	}
	filePath := filepath.Join(dir, "env.json")
	encoded, err := json.Marshal(data)
	if err != nil {
		_ = os.RemoveAll(dir)
		return CredentialMaterialization{}, errors.New("runtime_opaque_context_invalid")
	}
	defer clear(encoded)

	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return CredentialMaterialization{}, errors.New("runtime_opaque_context_unavailable")
	}
	_, writeErr := file.Write(encoded)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		shredRuntimeOpaqueDir(filePath, dir)
		return CredentialMaterialization{}, errors.New("runtime_opaque_context_unavailable")
	}

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rec := &runtimeOpaqueRecord{
		SessionID:    req.SessionID,
		CredentialID: req.CredentialID,
		GrantID:      req.GrantID,
		VersionID:    req.VersionID,
		Namespace:    req.Scope.Namespace,
		ResourceVer:  "runtime-" + randomHex(16),
		Digest:       digest,
		Dir:          dir,
		FilePath:     filePath,
	}
	runtimeOpaqueContexts.Lock()
	runtimeOpaqueContexts.byPath[filePath] = rec
	runtimeOpaqueContexts.Unlock()

	return CredentialMaterialization{
		CredentialID:    req.CredentialID,
		GrantID:         req.GrantID,
		VersionID:       req.VersionID,
		ResourceName:    credentialSecretName(req.CredentialID, req.GrantID),
		Namespace:       req.Scope.Namespace,
		Status:          "verified",
		Digest:          digest,
		Keys:            keys,
		ResourceVersion: rec.ResourceVer,
		ContextKind:     runtimeOpaqueContextKind,
		ContextSocket:   filePath,
	}, nil
}

func shredRuntimeOpaqueDir(filePath, dir string) {
	if info, err := os.Stat(filePath); err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= 2<<20 {
		if f, openErr := os.OpenFile(filePath, os.O_WRONLY, 0600); openErr == nil {
			zeros := make([]byte, info.Size())
			_, _ = f.Write(zeros)
			_ = f.Sync()
			_ = f.Close()
		}
	}
	_ = os.RemoveAll(dir)
}

func cleanupRuntimeOpaqueCredential(req CredentialCleanupRequest) (bool, error) {
	var matched []*runtimeOpaqueRecord
	runtimeOpaqueContexts.Lock()
	for path, rec := range runtimeOpaqueContexts.byPath {
		if rec.CredentialID != req.CredentialID || rec.GrantID != req.GrantID {
			continue
		}
		if req.SessionID != "" && rec.SessionID != req.SessionID {
			continue
		}
		if !req.AnyVersion && req.VersionID != "" && rec.VersionID != req.VersionID {
			continue
		}
		matched = append(matched, rec)
		delete(runtimeOpaqueContexts.byPath, path)
	}
	runtimeOpaqueContexts.Unlock()
	if len(matched) == 0 {
		return req.RuntimeEnvOnly, nil
	}
	for _, rec := range matched {
		shredRuntimeOpaqueDir(rec.FilePath, rec.Dir)
	}
	return true, nil
}

func runtimeOpaquePayloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// prepareSkillRuntimeOpaqueCredentials materializes validated opaqueSecret
// credential_ref inputs over the private internal channel for a Skill script
// run, returning DOOPS_CRED_FILE_<KEY> environment mappings (opaque 0600 file
// paths — never secret values) and a cleanup callback that shreds every file.
func (h *GatewayHub) prepareSkillRuntimeOpaqueCredentials(
	ctx context.Context,
	auth TokenAuth,
	sk platformSkillRecord,
	target monitorTargetRef,
	sessionID string,
	inputs map[string]string,
) (map[string]string, func(), error) {
	if h == nil || h.store == nil || len(inputs) == 0 {
		return nil, func() {}, nil
	}
	manifest := decodeStoredSkillUIManifest(sk.UIManifestJSON)
	if manifest == nil || len(manifest.UI.Inputs) == 0 {
		return nil, func() {}, nil
	}

	envFiles := make(map[string]string)
	var cleanups []CredentialMaterializeRequest
	cleanupAll := func() {
		for _, req := range cleanups {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = h.cleanupRuntimeOpaqueVersion(cleanCtx, req)
			cancel()
		}
	}

	for _, spec := range manifest.UI.Inputs {
		if spec.Type != "credential_ref" {
			continue
		}
		credName := strings.TrimSpace(inputs[spec.Key])
		if credName == "" {
			continue
		}
		if spec.CredentialUse != "" && spec.CredentialUse != string(CredentialUseOpaqueSecret) {
			continue
		}
		actorID := auth.UserID
		if actorID == "" || actorID == "system" {
			// Scheduled/system runs resolve bound active opaque credentials on the target.
			continue
		}
		resolved, err := h.store.AuthorizeCredentialUse(CredentialUseRequest{
			ActorID:    actorID,
			Credential: credName,
			Scope: CredentialGrantScope{
				Cluster:  target.Cluster,
				Instance: target.Instance,
				Use:      CredentialUseOpaqueSecret,
			},
		})
		if err != nil {
			cleanupAll()
			return nil, func() {}, err
		}
		if resolved.Resource.Type != CredentialTypeOpaque {
			continue
		}
		scope := normalizeCredentialGrantScope(resolved.Grant.Scope)
		scope.Use = CredentialUseOpaqueSecret
		ref := CredentialPlanReference{Name: resolved.Resource.Name, Scope: scope}
		mat, req, err := h.materializeCredentialVersion(
			ctx, target.Cluster, target.Instance, sessionID,
			resolved.Resource, resolved.Version, "", resolved.Grant.ID, ref,
			materializeRuntimeEnvOnly,
		)
		if err != nil {
			cleanupAll()
			return nil, func() {}, err
		}
		if mat.ContextKind != runtimeOpaqueContextKind || !validRuntimeOpaqueEnvPath(mat.ContextSocket) {
			cleanupAll()
			return nil, func() {}, errors.New("runtime_opaque_materialization_invalid")
		}
		cleanups = append(cleanups, req)
		envFiles["DOOPS_CRED_FILE_"+spec.Key] = mat.ContextSocket
	}
	if len(envFiles) == 0 {
		return nil, func() {}, nil
	}
	return envFiles, cleanupAll, nil
}

func (h *GatewayHub) cleanupRuntimeOpaqueVersion(ctx context.Context, request CredentialMaterializeRequest) error {
	cleanupReq := CredentialCleanupRequest{
		CredentialType: request.CredentialType,
		SessionID:      request.SessionID,
		CredentialID:   request.CredentialID,
		GrantID:        request.GrantID,
		VersionID:      request.VersionID,
		Namespace:      request.Scope.Namespace,
		RuntimeEnvOnly: true,
	}
	encoded, err := json.Marshal(cleanupReq)
	if err != nil {
		return err
	}
	var toolArgs map[string]any
	if err := json.Unmarshal(encoded, &toolArgs); err != nil {
		return err
	}
	_, err = h.RunInternalToolCall(ctx, request.Cluster, request.Instance, "doops_credential_cleanup", toolArgs)
	return err
}
