package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type gatewayAgentResourceSync struct {
	mu             sync.Mutex
	observed       *agentResourceSnapshot
	digest         string
	lastObservedAt string // protected by GatewayAgent.stateMu
	changed        chan struct{}
	manifest       targetManifest // protected by GatewayAgent.stateMu
}

func (a *GatewayAgent) resourceRPC(ctx context.Context, method string, params any, limit int, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := atomic.AddInt64(&a.reqID, 1)
	queue := a.registerPending(id, "")
	defer a.unregisterPending(id)
	if err := a.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return errResourceSyncDisconnected
	}
	for {
		if message, ok, err := queue.dequeue(); err != nil {
			return err
		} else if ok {
			var response struct {
				Result json.RawMessage `json:"result"`
			}
			if len(message.Raw) > limit+1024 || json.Unmarshal(message.Raw, &response) != nil ||
				decodeResourceSync(response.Result, limit, result) != nil {
				return errTargetManifestUnavailable
			}
			return nil
		}
		select {
		case <-queue.notify:
		case <-ctx.Done():
			return ctx.Err()
		case <-a.closed:
			return errResourceSyncDisconnected
		}
	}
}

func (h *GatewayHub) runAgentResourceSync(agent *GatewayAgent) {
	if !agent.AgentResourceSync {
		return
	}
	if !h.store.agentResourceSyncReady(context.Background()) {
		log.Printf("[gateway] target resource sync disabled: apply agent_resource_sync migration and inventory prerequisites")
		return
	}
	interval := max(h.opts.AgentLease/3, time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sync := func(observe bool) {
		ctx, cancel := context.WithTimeout(context.Background(), agentResourceProbeTimeout+8*time.Second)
		defer cancel()
		if err := h.syncAgentResources(ctx, agent, observe); err != nil && !errors.Is(err, errResourceSyncDisconnected) {
			log.Printf("[gateway] target resource sync failed: %s: %v", agent.Key, err)
		}
	}
	sync(true)
	for {
		select {
		case <-agent.closed:
			return
		case <-ticker.C:
			sync(true)
		case <-agent.resourceSync.changed:
			sync(false)
		}
	}
}

func (h *GatewayHub) syncAgentResources(ctx context.Context, agent *GatewayAgent, observe bool) error {
	sync := &agent.resourceSync
	sync.mu.Lock()
	defer sync.mu.Unlock()
	if h.registry.Get(agent.Cluster, agent.Instance) != agent {
		return errResourceSyncDisconnected
	}
	if !agent.AgentResourceSync {
		return errResourceSyncUnsupported
	}
	if !h.store.agentResourceSyncReady(ctx) {
		return errResourceSyncMigration
	}
	var heartbeat resourceHeartbeat
	var observation *agentResourceSnapshot
	if observe || sync.observed == nil {
		if err := agent.resourceRPC(ctx, agentResourceObserveMethod, resourceHeartbeatRequest{KnownDigest: sync.digest}, agentObservationMaxBytes+512, &heartbeat); err != nil {
			return err
		}
		if heartbeat.Snapshot == nil {
			if sync.observed == nil || heartbeat.Digest != sync.digest {
				return errTargetManifestUnavailable
			}
		} else {
			heartbeat.Snapshot.canonicalize()
			if !heartbeat.Snapshot.valid() || heartbeat.Digest != agentResourceDigest(*heartbeat.Snapshot) {
				return errTargetManifestUnavailable
			}
			sync.observed, sync.digest = heartbeat.Snapshot, heartbeat.Digest
		}
		observation = sync.observed
	}
	manifest, err := h.store.reconcileAgentResources(ctx, agent, observation, sync.digest)
	if err != nil {
		return err
	}
	agent.stateMu.Lock()
	previous := sync.manifest
	sync.manifest = manifest
	if observation != nil {
		sync.lastObservedAt = formatTime(time.Now().UTC())
	}
	agent.stateMu.Unlock()
	if observation == nil && previous.Digest == manifest.Digest && previous.Revision == manifest.Revision {
		return nil
	}
	if heartbeat.AppliedRevision != manifest.Revision || heartbeat.AppliedDigest != manifest.Digest {
		var ack struct {
			Revision uint64 `json:"applied_revision"`
		}
		if err := agent.resourceRPC(ctx, agentResourceApplyMethod, manifest, 512, &ack); err != nil {
			return err
		}
		if ack.Revision != manifest.Revision {
			return errTargetManifestUnavailable
		}
	}
	return h.store.saveAgentResourceApplied(ctx, agent, manifest.Revision)
}

func (h *GatewayHub) notifyResourceCatalogChange() {
	for _, agent := range h.registry.resourceSyncAgents() {
		select {
		case agent.resourceSync.changed <- struct{}{}:
		default:
		}
	}
}

// The catalogue and grant handlers commit their writes before returning. A
// single coalesced signal lets each online target recompute its own projection.
func (h *GatewayHub) resourceCatalogHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r)
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			h.notifyResourceCatalogChange()
		}
	}
}

func manifestHTTPProblem(err error) machineInventoryProblem {
	var problem machineInventoryProblem
	switch {
	case errors.As(err, &problem):
		return problem
	case errors.Is(err, ErrAgentStatusNotFound):
		return machineInventoryProblem{404, "target_not_registered", "目标未登记或已归档。"}
	case errors.Is(err, errResourceSyncDisconnected), errors.Is(err, errResourceSyncUnsupported):
		return machineInventoryProblem{409, err.Error(), "当前 Agent 无法同步资源。"}
	case errors.Is(err, errResourceSyncMigration):
		return machineInventoryProblem{503, err.Error(), "目标清单迁移未完成。"}
	default:
		return machineInventoryProblem{503, errTargetManifestUnavailable.Error(), "目标清单暂时不可用。"}
	}
}

func (h *GatewayHub) HandleTargetManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	status, err := h.targetManifestRequest(w, r, auth)
	if err != nil {
		problem := manifestHTTPProblem(err)
		writeDoopsError(w, problem.status, "", doopsErrorResponse{Error: problem.code, Code: problem.code, Message: problem.message})
		return
	}
	writeJSONHTTP(w, status)
}

func (h *GatewayHub) targetManifestRequest(w http.ResponseWriter, r *http.Request, auth TokenAuth) (targetManifestStatus, error) {
	var req struct {
		workspaceTarget
		Action string `json:"action"`
		Ref    string `json:"ref"`
	}
	switch r.Method {
	case http.MethodGet:
		req.Cluster, req.Instance = r.URL.Query().Get("cluster"), r.URL.Query().Get("instance")
	case http.MethodPost:
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil || decodeResourceSync(data, 4096, &req) != nil || (req.Action != "refresh" && req.Action != "register") ||
			(req.Action == "refresh" && req.Ref != "") || (req.Action == "register" && !validManifestRef(req.Ref)) {
			return targetManifestStatus{}, machineBadRequest("invalid_target_manifest_request", "请提供目标和有效的清单操作。")
		}
	default:
		w.Header().Set("Allow", "GET, POST")
		return targetManifestStatus{}, machineInventoryProblem{405, "method_not_allowed", "method not allowed"}
	}
	if !monitorID(req.Cluster) || !monitorID(req.Instance) {
		return targetManifestStatus{}, machineBadRequest("invalid_target", "请选择具体的 Agent。")
	}
	if !h.store.UserCanUseTarget(auth.UserID, req.Cluster, req.Instance) {
		return targetManifestStatus{}, machineInventoryProblem{403, "inventory_forbidden", "当前账号无权使用该目标。"}
	}
	ctx := r.Context()
	if req.Action == "refresh" {
		agent := h.registry.Get(req.Cluster, req.Instance)
		if agent == nil {
			return targetManifestStatus{}, errResourceSyncDisconnected
		}
		syncCtx, cancel := context.WithTimeout(ctx, agentResourceProbeTimeout+8*time.Second)
		defer cancel()
		auditID, err := h.store.StartAudit(AuditEvent{UserID: auth.UserID, TokenID: auth.TokenID, Cluster: req.Cluster, Instance: req.Instance, Action: GatewayAction("target_manifest:refresh")})
		if err != nil {
			return targetManifestStatus{}, err
		}
		err = h.syncAgentResources(syncCtx, agent, true)
		finish := AuditFinish{Status: "success"}
		if err != nil {
			finish = AuditFinish{Status: "error", Error: manifestHTTPProblem(err).code}
		}
		if auditErr := h.store.FinishAudit(auditID, finish); auditErr != nil {
			return targetManifestStatus{}, auditErr
		}
		if err != nil {
			return targetManifestStatus{}, err
		}
	}
	if req.Action == "register" {
		if err := h.store.registerObservedResource(ctx, auth, req.Cluster, req.Instance, req.Ref); err != nil {
			return targetManifestStatus{}, err
		}
		h.notifyResourceCatalogChange()
	}
	return h.store.readTargetManifest(ctx, req.Cluster, req.Instance)
}

func validManifestRef(ref string) bool {
	parts := strings.Split(ref, "/")
	if len(parts) < 2 || len(parts) > 3 || (!validObservationKind(parts[0]) && parts[0] != "system" && parts[0] != "credential" && parts[0] != "image") {
		return false
	}
	for _, part := range parts[1:] {
		if !validResourceMetadataToken(part) {
			return false
		}
	}
	return true
}
