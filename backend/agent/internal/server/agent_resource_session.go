package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
	"golang.org/x/sys/unix"
)

const agentResourceProbeTimeout = 12 * time.Second

type resourceHeartbeat struct {
	Digest          string                 `json:"observation_digest"`
	AppliedRevision uint64                 `json:"applied_revision"`
	AppliedDigest   string                 `json:"applied_digest,omitempty"`
	Snapshot        *agentResourceSnapshot `json:"snapshot,omitempty"`
}

type resourceHeartbeatRequest struct {
	KnownDigest string `json:"observation_digest,omitempty"`
}

type agentResourceSession struct {
	target targetManifestTarget
	dir    string
}

func agentManifestDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".doops", "manifest"), nil
}

func (s *agentResourceSession) accepts(manifest targetManifest) bool {
	return s.target.Cluster != "" && s.target.Instance != "" && manifest.Target.Cluster == s.target.Cluster &&
		manifest.Target.Instance == s.target.Instance && manifest.Revision > 0 && manifest.Digest == manifest.contentDigest() &&
		validAgentResourceMode(manifest.Target.Mode) && manifest.Target.KubernetesAvailable != nil
}

func (s *agentResourceSession) current() (targetManifest, []byte, error) {
	var manifest targetManifest
	if s.dir == "" {
		return manifest, nil, errTargetManifestUnavailable
	}
	file, err := os.Open(filepath.Join(s.dir, "current.json"))
	if err != nil {
		return manifest, nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, targetManifestMaxBytes+1))
	if err != nil || decodeResourceSync(data, targetManifestMaxBytes, &manifest) != nil || !s.accepts(manifest) {
		return manifest, nil, errTargetManifestUnavailable
	}
	return manifest, data, nil
}

func (s *agentResourceSession) observe(ctx context.Context, params json.RawMessage) (resourceHeartbeat, error) {
	var request resourceHeartbeatRequest
	if err := decodeResourceSync(params, 512, &request); err != nil {
		return resourceHeartbeat{}, err
	}
	if request.KnownDigest != "" && !validCredentialSHA256Digest(request.KnownDigest) {
		return resourceHeartbeat{}, errors.New("invalid_observation_digest")
	}
	ctx, cancel := context.WithTimeout(ctx, agentResourceProbeTimeout)
	defer cancel()
	snapshot := collectAgentResourceSnapshot(ctx)
	snapshot.canonicalize()
	if !snapshot.valid() {
		return resourceHeartbeat{}, errors.New("invalid_agent_observations")
	}
	result := resourceHeartbeat{Digest: agentResourceDigest(snapshot)}
	if request.KnownDigest != result.Digest {
		encoded, err := json.Marshal(snapshot)
		if err != nil || len(encoded) > agentObservationMaxBytes {
			return resourceHeartbeat{}, errors.New("agent_observations_too_large")
		}
		result.Snapshot = &snapshot
	}
	current, _, err := s.current()
	if err == nil {
		result.AppliedRevision = current.Revision
		result.AppliedDigest = current.Digest
	}
	return result, nil
}

func (s *agentResourceSession) apply(params json.RawMessage) (uint64, error) {
	var manifest targetManifest
	if err := decodeResourceSync(params, targetManifestMaxBytes, &manifest); err != nil {
		return 0, err
	}
	if !s.accepts(manifest) || s.dir == "" {
		return 0, errors.New("target_manifest_invalid")
	}
	// Connections (and rolling replacement processes) can share this volume.
	// Lock the directory itself: current.json is replaced by a rename.
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return 0, errors.New("target_manifest_directory_unavailable")
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return 0, errors.New("target_manifest_directory_unavailable")
	}
	defer dir.Close() // releases the flock
	if err := unix.Flock(int(dir.Fd()), unix.LOCK_EX); err != nil {
		return 0, errors.New("target_manifest_lock_failed")
	}
	if current, previous, err := s.current(); err == nil {
		if current.Revision > manifest.Revision || current.Revision == manifest.Revision && current.Digest != manifest.Digest {
			return 0, errors.New("target_manifest_revision_conflict")
		}
		if current.Revision == manifest.Revision && current.Digest == manifest.Digest {
			return manifest.Revision, nil
		}
		if err := writeFileAtomic0600(filepath.Join(s.dir, "previous.json"), previous); err != nil {
			return 0, errors.New("target_manifest_previous_write_failed")
		}
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errTargetManifestUnavailable) {
		return 0, errors.New("target_manifest_current_read_failed")
	}
	if err := writeFileAtomic0600(filepath.Join(s.dir, "current.json"), params); err != nil {
		return 0, errors.New("target_manifest_write_failed")
	}
	return manifest.Revision, nil
}

func (s *agentResourceSession) handle(ctx context.Context, req api.JSONRPCRequest, writeJSON func(interface{})) {
	var result any
	var err error
	switch req.Method {
	case agentResourceObserveMethod:
		result, err = s.observe(ctx, req.Params)
	case agentResourceApplyMethod:
		var revision uint64
		revision, err = s.apply(req.Params)
		result = map[string]uint64{"applied_revision": revision}
	}
	if err != nil {
		writeJSON(buildErrorResponse(req.ID, -32603, err.Error()))
		return
	}
	writeJSON(api.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

type executionTargetManifest struct {
	Manifest       targetManifest `json:"manifest"`
	LastObservedAt string         `json:"last_observed_at,omitempty"`
}

type executionTargetManifestKey struct{}

func (s *agentResourceSession) pinExecution(ctx context.Context, params api.ToolCallParams) (context.Context, error) {
	session := extractSession(params.Arguments)
	if session == "" {
		session = "default" // Same workspace as handleToolCallOverWS.
	}
	if err := validateSession(session); err != nil {
		return ctx, err
	}
	var execution executionTargetManifest
	if len(params.TargetManifest) == 0 {
		// Never let the previous turn's file masquerade as this turn's snapshot.
		root, err := openWorkspaceFileRoot(session, false)
		if err == nil {
			defer root.Close()
			err = root.Remove("target.json")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return ctx, errors.New("target_manifest_snapshot_remove_failed")
		}
	} else {
		if err := decodeResourceSync(params.TargetManifest, targetManifestMaxBytes+256, &execution); err != nil || !s.accepts(execution.Manifest) {
			return ctx, errors.New("target_manifest_snapshot_invalid")
		}
		if err := writeSessionTargetManifest(session, execution.Manifest); err != nil {
			return ctx, errors.New("target_manifest_snapshot_write_failed")
		}
	}
	return context.WithValue(ctx, executionTargetManifestKey{}, execution), nil
}

func writeSessionTargetManifest(session string, manifest targetManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	root, err := openWorkspaceFileRoot(session, true)
	if err != nil {
		return err
	}
	defer root.Close()
	temporary := ".target-" + randomHex(16) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	// os.Root.Rename needs go1.25 while this module targets go1.24; the temp file
	// and target share one validated session directory, so a plain rename is atomic.
	directory := filepath.Join(workspaceRoot(), session)
	return os.Rename(filepath.Join(directory, temporary), filepath.Join(directory, "target.json"))
}

func (m targetManifest) hasUsableCluster() bool {
	if m.Target.KubernetesAvailable != nil && *m.Target.KubernetesAvailable {
		for _, r := range m.Resources {
			if r.Kind == "cluster" && (r.State == "verified" || r.State == "discovered") {
				return true
			}
		}
	}
	return false
}

func (e executionTargetManifest) promptFacts(now time.Time) string {
	m := e.Manifest
	if m.Revision == 0 {
		return "\n" + missingExecutionManifest + "\n"
	}
	var result strings.Builder
	fmt.Fprintf(&result, "\n当前目标清单版本 %d，接入模式 %s，Kubernetes 可用集群：%t。详细元数据可用 Read 读取工作区 target.json。", m.Revision, m.Target.Mode, m.hasUsableCluster())
	lastObserved, err := parseTime(e.LastObservedAt)
	if err != nil || now.Sub(lastObserved) > 3*time.Minute {
		result.WriteString(" 资源观测可能过期，使用前请核对现场。")
	}
	result.WriteByte('\n')
	kinds := make([]string, 0, len(m.Counts))
	for kind := range m.Counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		var refs []string
		for _, resource := range m.Resources {
			if resource.Kind == kind && len(refs) < 3 {
				refs = append(refs, resource.Ref)
			}
		}
		fmt.Fprintf(&result, "- %s %d 项：%s\n", kind, m.Counts[kind], strings.Join(refs, ", "))
	}
	return result.String()
}
