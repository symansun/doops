package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Only fixed metadata fields cross the Agent tunnel. Every list has a completion marker.
const runtimeFields = `jsonpath={"runtime-begin\n"}{range .items[*]}{.metadata.namespace}{"\t"}{.metadata.name}{"\t"}{.metadata.uid}{"\t"}{range .metadata.ownerReferences[?(@.controller==true)]}{.apiVersion}{"/"}{.kind}{"/"}{.name}{"/"}{.uid}{end}{"\t"}`
const runtimeEnd = `{"\n"}{end}{"runtime-end\n"}`

var runtimeKinds = []struct{ kind, resource string }{{"Deployment", "deployments"}, {"ReplicaSet", "replicasets"}, {"StatefulSet", "statefulsets"}, {"DaemonSet", "daemonsets"}, {"CronJob", "cronjobs"}, {"Job", "jobs"}, {"Pod", "pods"}}

type runtimeObject struct {
	UID   string `json:"uid"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Owner string `json:"owner,omitempty"`
	Node  string `json:"node,omitempty"`
}
type runtimeKey struct {
	AssetID   string `json:"cluster_id"`
	Cluster   string `json:"cluster"`
	Instance  string `json:"instance"`
	Namespace string `json:"namespace"`
}
type runtimeRequest struct {
	runtimeKey
	Action      string `json:"action"`
	Application string `json:"application,omitempty"`
	Workload    string `json:"workload_uid,omitempty"`
	Revision    string `json:"revision,omitempty"`
}
type runtimeBinding struct {
	Application string `json:"application"`
	UID         string `json:"uid"`
	At          string `json:"at"`
}
type runtimeScope struct {
	runtimeKey
	Status       string           `json:"status"`
	Revision     string           `json:"revision"`
	At           string           `json:"observed_at"`
	Applications []string         `json:"applications"`
	Workloads    []runtimeObject  `json:"workloads"`
	Bindings     []runtimeBinding `json:"bindings"`
}

type runtimeStamp struct {
	UID, Verified, SourceVerified, Revision string
	Generation                              uint64
	Status                                  string
}

func currentRuntime(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, auth TokenAuth, k runtimeKey) (runtimeStamp, error) {
	var v runtimeStamp
	actor, err := resolveInventoryActor(ctx, db, auth)
	if err != nil {
		return v, err
	}
	if !actor.active {
		return v, inventoryFailure{404, "runtime_scope_unavailable"}
	}
	pred, predArgs := actor.targetPredicate("n.cluster", "n.instance")
	args := append([]any{k.AssetID, k.Cluster, k.Instance, k.Namespace}, predArgs...)
	err = db.QueryRowContext(ctx, `SELECT n.uid,n.verified_at,s.verified_at,COALESCE(x.revision,''),COALESCE(a.generation,0),COALESCE(a.status,'offline') FROM cluster_inventory_namespaces n JOIN cluster_inventory_sources s USING(cluster,instance) LEFT JOIN agent_status a USING(cluster,instance) LEFT JOIN asset_runtime_snapshots x ON (x.cluster,x.instance,x.namespace,x.asset_id)=(n.cluster,n.instance,n.name,s.asset_id) WHERE s.asset_id=? AND n.cluster=? AND n.instance=? AND n.name=? AND n.verified_at<>'' AND `+pred, args...).Scan(&v.UID, &v.Verified, &v.SourceVerified, &v.Revision, &v.Generation, &v.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return v, inventoryFailure{404, "runtime_scope_unavailable"}
	}
	return v, err
}
func parseRuntime(output, kind, namespace string, seen map[string]runtimeObject) error {
	bad := inventoryFailure{502, "invalid_runtime_projection"}
	if len(output) > 1024*1024 {
		return bad
	}
	text, ok := strings.CutSuffix(output, "\nKubernetes operation complete.")
	if !ok {
		return bad
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for len(lines) > 0 && strings.HasPrefix(lines[0], "Warning: ") {
		lines = lines[1:]
	}
	if len(lines) < 2 || lines[0] != "runtime-begin" || lines[len(lines)-1] != "runtime-end" {
		return bad
	}
	for _, line := range lines[1 : len(lines)-1] {
		f := strings.Split(line, "\t")
		if len(f) != 5 || f[0] != namespace || len(f[1]) > 253 || !inventoryNodeName.MatchString(f[1]) || !safeMachineID(f[2]) {
			return bad
		}
		if f[4] != "" && (kind != "Pod" || len(f[4]) > 253 || !inventoryNodeName.MatchString(f[4])) {
			return bad
		}
		obj := runtimeObject{UID: f[2], Kind: kind, Name: f[1], Owner: f[3], Node: f[4]}
		if old, exists := seen[obj.UID]; exists && old != obj {
			return bad
		}
		seen[obj.UID] = obj
		if len(seen) > 4096 {
			return inventoryFailure{413, "runtime_scope_too_large"}
		}
	}
	return nil
}
func (h *GatewayHub) collectRuntime(ctx context.Context, auth TokenAuth, k runtimeKey, start runtimeStamp, session string) (map[string]runtimeObject, error) {
	agent := h.getAgent(k.Cluster, k.Instance)
	if agent == nil || start.Status != "online" || agent.Generation != start.Generation {
		return nil, inventoryFailure{503, "runtime_source_offline"}
	}
	ctx, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 45*time.Second)
	defer cancel()
	objects := map[string]runtimeObject{}
	for _, kind := range runtimeKinds {
		stamp, err := currentRuntime(ctx, h.store.db, auth, k)
		if err != nil {
			return nil, err
		}
		if stamp != start {
			return nil, inventoryFailure{409, "runtime_scope_changed"}
		}
		projection := runtimeFields
		if kind.kind == "Pod" {
			projection += `{.spec.nodeName}`
		}
		projection += runtimeEnd
		data, err := h.RunInternalToolCall(ctx, k.Cluster, k.Instance, "doops_k8s", map[string]any{"operation": "get", "session_id": session, "kind": kind.resource, "namespace": k.Namespace, "output": projection})
		if err != nil {
			if strings.Contains(data, "(Forbidden)") || strings.Contains(data, "(Unauthorized)") {
				return nil, inventoryFailure{403, "runtime_kubernetes_denied"}
			}
			if ctx.Err() != nil {
				return nil, inventoryFailure{504, "runtime_read_timeout"}
			}
			return nil, inventoryFailure{502, "runtime_agent_read_failed"}
		}
		if err = parseRuntime(data, kind.kind, k.Namespace, objects); err != nil {
			return nil, err
		}
	}
	return objects, nil
}
func (h *GatewayHub) handleRuntimeTopology(w http.ResponseWriter, r *http.Request, auth TokenAuth) {
	var req runtimeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF || r.URL.RawQuery != "" || !inventoryKeyPattern.MatchString(req.AssetID) || !inventoryKeyPattern.MatchString(req.Cluster) || !inventoryKeyPattern.MatchString(req.Instance) || !inventoryNamespacePattern.MatchString(req.Namespace) || !inventoryMetadata(req.AssetID+" "+req.Cluster+" "+req.Instance) || !req.validAction() {
		inventoryHTTPError(w, inventoryFailure{400, "invalid_runtime_request"})
		return
	}
	session := "runtime-" + randomHex(12)
	auditID, err := h.store.StartAudit(AuditEvent{UserID: auth.UserID, TokenID: auth.TokenID, Cluster: req.Cluster, Instance: req.Instance, Action: GatewayAction("asset_topology:" + req.Action), Session: session, CommandSummary: req.AssetID + "/" + req.Namespace + " " + req.Application + " " + req.Workload})
	if err == nil {
		err = h.applyRuntime(r.Context(), auth, req, session, auditID)
		if err != nil {
			code := "runtime_store_unavailable"
			var failure inventoryFailure
			if errors.As(err, &failure) {
				code = failure.code
			}
			if finishErr := h.store.FinishAudit(auditID, AuditFinish{Status: "error", Error: code}); finishErr != nil {
				err = finishErr
			}
		}
	}
	if err != nil {
		var failure inventoryFailure
		if !errors.As(err, &failure) {
			failure = inventoryFailure{503, "runtime_store_unavailable"}
		}
		inventoryHTTPError(w, failure)
		return
	}
	writeJSONHTTP(w, map[string]string{"action": req.Action, "status": "success"})
}
func (r runtimeRequest) validAction() bool {
	switch r.Action {
	case "collect":
		return r.Application == "" && r.Workload == "" && r.Revision == ""
	case "bind", "unbind":
		return safeMachineID(r.Application) && safeMachineID(r.Workload) && safeMachineID(r.Revision)
	}
	return false
}
func (h *GatewayHub) applyRuntime(ctx context.Context, auth TokenAuth, r runtimeRequest, session string, auditID int64) error {
	start, err := currentRuntime(ctx, h.store.db, auth, r.runtimeKey)
	if err != nil {
		return err
	}
	var objects map[string]runtimeObject
	if r.Action == "collect" {
		objects, err = h.collectRuntime(ctx, auth, r.runtimeKey, start, session)
		if err != nil {
			return err
		}
	}
	tx, err := h.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Reserve the writer, then recheck identity, grants, managed namespace, Agent generation and previous snapshot.
	if err = gatewayExecExactlyOne(tx, `UPDATE audit_events SET status='executing' WHERE id=? AND session=? AND status='running'`, auditID, session); err != nil {
		return err
	}
	current, err := currentRuntime(ctx, tx, auth, r.runtimeKey)
	if err != nil {
		return err
	}
	if start != current {
		return inventoryFailure{409, "runtime_scope_changed"}
	}
	if r.Action == "collect" {
		raw, err := json.Marshal(objects)
		if err != nil {
			return err
		}
		_, err = tx.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO asset_runtime_snapshots(cluster,instance,namespace,asset_id,revision,observed_at,objects) VALUES(?,?,?,?,?,?,?)", target: []string{"cluster", "instance", "namespace"}, set: []string{"asset_id=excluded.asset_id", "revision=excluded.revision", "observed_at=excluded.observed_at", "objects=excluded.objects"}}), r.Cluster, r.Instance, r.Namespace, r.AssetID, session, formatTime(time.Now()), string(raw))
		if err != nil {
			return err
		}
		if len(objects) == 0 {
			_, err = tx.Exec(`DELETE FROM asset_runtime_bindings WHERE cluster=? AND instance=? AND namespace=?`, r.Cluster, r.Instance, r.Namespace)
		} else {
			keys := make([]string, 0, len(objects))
			for k := range objects {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			delArgs := make([]any, 0, 3+len(keys))
			delArgs = append(delArgs, r.Cluster, r.Instance, r.Namespace)
			for _, k := range keys {
				delArgs = append(delArgs, k)
			}
			_, err = tx.Exec(`DELETE FROM asset_runtime_bindings WHERE cluster=? AND instance=? AND namespace=? AND workload_uid NOT IN (`+gatewayInPlaceholders(len(keys))+`)`, delArgs...)
		}
		if err != nil {
			return err
		}
	} else {
		if err = requireInventoryAdmin(tx, auth); err != nil {
			var p machineInventoryProblem
			if errors.As(err, &p) {
				return inventoryFailure{p.status, p.code}
			}
			return err
		}
		if current.Revision != r.Revision {
			return inventoryFailure{409, "runtime_snapshot_changed"}
		}
		if r.Action == "bind" {
			if current.Status != "online" {
				return inventoryFailure{503, "runtime_source_offline"}
			}
			var depValid bool
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_business_deployments WHERE cluster=? AND instance=? AND namespace=? AND application=? AND verified_at<>'')`, r.Cluster, r.Instance, r.Namespace, r.Application).Scan(&depValid)
			if err != nil {
				return err
			}
			valid := false
			if depValid {
				var snapRaw string
				err = tx.QueryRow(`SELECT objects FROM asset_runtime_snapshots WHERE cluster=? AND instance=? AND namespace=? AND asset_id=?`, r.Cluster, r.Instance, r.Namespace, r.AssetID).Scan(&snapRaw)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if err == nil {
					var snapObjects map[string]runtimeObject
					if uerr := json.Unmarshal([]byte(snapRaw), &snapObjects); uerr != nil {
						return uerr
					}
					if obj, ok := snapObjects[r.Workload]; ok && obj.Kind != "Pod" {
						valid = true
					}
				}
				err = nil
			}
			if !valid {
				return inventoryFailure{409, "runtime_binding_subject_changed"}
			}
			_, err = tx.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO asset_runtime_bindings(cluster,instance,namespace,application,workload_uid,verified_at) VALUES(?,?,?,?,?,?)", doNothing: true}), r.Cluster, r.Instance, r.Namespace, r.Application, r.Workload, formatTime(time.Now()))
		} else {
			_, err = tx.Exec(`DELETE FROM asset_runtime_bindings WHERE cluster=? AND instance=? AND namespace=? AND application=? AND workload_uid=?`, r.Cluster, r.Instance, r.Namespace, r.Application, r.Workload)
		}
		if err != nil {
			return err
		}
	}
	if err = finishAuditWith(tx, auditID, AuditFinish{Status: "success"}); err != nil {
		return err
	}
	return tx.Commit()
}
