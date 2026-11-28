package server

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
)

// Catalog assignments expose inventory metadata to an exact target workspace.
// Diagnostic claims, cluster sources, user grants and secret bindings have their own lifecycles.
// Written as a derived table (no WITH CTE) so SQLite, MySQL 5.7, and MySQL 8.0/8.4 execute it identically.
const workspaceCatalog = `(
 SELECT c.*, n.id IS NOT NULL AS linked, a.asset_id IS NOT NULL AS assigned FROM (
  SELECT 'machine' AS kind,asset_id AS id,name,address FROM inventory_machines UNION ALL
  SELECT 'cluster',asset_id,min(name),'' FROM cluster_inventory_sources GROUP BY asset_id UNION ALL
  SELECT 'system',asset_id,name,'' FROM inventory_business_systems
 ) AS c
 LEFT JOIN (
  SELECT 'machine' AS kind,asset_id AS id FROM inventory_machines WHERE cluster=? AND instance=? UNION
  SELECT 'cluster',asset_id FROM cluster_inventory_sources WHERE cluster=? AND instance=? UNION
  SELECT 'system',asset_id FROM inventory_business_deployments WHERE cluster=? AND instance=?
 ) AS n ON n.kind=c.kind AND n.id=c.id
 LEFT JOIN inventory_agent_assets a ON a.cluster=? AND a.instance=? AND a.kind=c.kind AND a.asset_id=c.id
) AS assets`

// targetPairArgs builds the bind list for catalog statements that repeat the
// workspace target once per placeholder. Both dialects bind plain ? in text
// order, so the order produced here IS the binding: a call site that assembles
// the pairs by hand can silently swap cluster and instance without any SQL
// error, which is exactly the failure mode the ?N to ? rewrite introduced.
// Every catalog statement therefore goes through this one constructor, and
// gateway_mysql_binding_order_test.go pins both its order and its call sites.
func targetPairArgs(cluster, instance string, pairs int, extra ...any) []any {
	args := make([]any, 0, pairs*2+len(extra))
	for i := 0; i < pairs; i++ {
		args = append(args, cluster, instance)
	}
	return append(args, extra...)
}

type workspaceAsset struct {
	Kind     string `json:"kind"`
	ID       string `json:"asset_id"`
	Name     string `json:"name"`
	Address  string `json:"address,omitempty"`
	Linked   bool   `json:"linked"`
	Assigned bool   `json:"assigned"`
}
type workspaceTarget struct {
	Cluster  string `json:"cluster"`
	Instance string `json:"instance"`
}
type scopeChange struct {
	workspaceTarget
	Kind     string `json:"kind"`
	ID       string `json:"asset_id"`
	Assigned *bool  `json:"assigned"`
}

func (h *GatewayHub) HandleAgentAssetScope(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	var result any
	switch r.Method {
	case http.MethodGet:
		result, err = h.store.readWorkspaceAssets(r, auth)
	case http.MethodPost:
		result, err = h.store.changeWorkspaceAsset(w, r, auth)
	default:
		err = machineInventoryProblem{405, "method_not_allowed", "method not allowed"}
	}
	if err != nil {
		p := publicInventoryProblem(err)
		writeDoopsError(w, p.status, "", doopsErrorResponse{Error: p.code, Code: p.code, Message: p.message})
		return
	}
	writeJSONHTTP(w, result)
}

func validateWorkspaceTarget(tx *sql.Tx, t workspaceTarget) error {
	if !monitorID(t.Cluster) || !monitorID(t.Instance) {
		return machineBadRequest("invalid_target", "请选择具体的 Agent。")
	}
	var found bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agent_status WHERE cluster=? AND instance=? AND archived_at='')`, t.Cluster, t.Instance).Scan(&found); err != nil {
		return err
	}
	if !found {
		return machineInventoryProblem{404, "target_not_registered", "目标未登记或已归档。"}
	}
	return nil
}
func (s *GatewayStore) readWorkspaceAssets(r *http.Request, auth TokenAuth) (any, error) {
	tx, err := s.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := r.URL.Query()
	view := q.Get("view")
	t := workspaceTarget{q.Get("cluster"), q.Get("instance")}
	switch view {
	case "directory":
		// The directory query below applies the caller’s current grants.
	case "targets", "manage":
		if err = requireInventoryAdmin(tx, auth); err != nil {
			return nil, err
		}
	case "":
		actor, err := resolveInventoryActor(r.Context(), tx, auth)
		if err != nil {
			return nil, err
		}
		allowed, err := actor.canUseTarget(r.Context(), tx, t.Cluster, t.Instance)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, machineInventoryProblem{403, "inventory_forbidden", "当前账号无权使用该目标。"}
		}
	default:
		return nil, machineBadRequest("invalid_view", "未知的资产视图。")
	}
	if view == "targets" || view == "directory" {
		actor, err := resolveInventoryActor(r.Context(), tx, auth)
		if err != nil {
			return nil, err
		}
		pred, predArgs := actor.targetPredicate("a.cluster", "a.instance")
		rows, err := tx.Query(`SELECT a.cluster,a.instance,a.mode,a.kubernetes_available FROM agent_status a WHERE a.archived_at='' AND `+pred+` ORDER BY a.cluster,a.instance`, predArgs...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []targetManifestTarget{}
		for rows.Next() {
			var v targetManifestTarget
			var available sql.NullBool
			if err = rows.Scan(&v.Cluster, &v.Instance, &v.Mode, &available); err != nil {
				return nil, err
			}
			if available.Valid {
				value := available.Bool
				v.KubernetesAvailable = &value
			}
			out = append(out, v)
		}
		return map[string]any{"targets": out}, rows.Err()
	}
	if err = validateWorkspaceTarget(tx, t); err != nil {
		return nil, err
	}
	// The catalog repeats the target pair once per placeholder, in text order;
	// both dialects bind plain ? in that same order. targetPairArgs owns that
	// order so the call site cannot swap cluster and instance by accident.
	rows, err := tx.Query(`SELECT kind,id,name,address,linked,assigned FROM `+workspaceCatalog+` WHERE linked OR assigned OR ? ORDER BY kind,id`,
		targetPairArgs(t.Cluster, t.Instance, 4, view == "manage")...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workspaceAsset{}
	for rows.Next() {
		var a workspaceAsset
		if err = rows.Scan(&a.Kind, &a.ID, &a.Name, &a.Address, &a.Linked, &a.Assigned); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return map[string]any{"assets": out}, rows.Err()
}

func (s *GatewayStore) changeWorkspaceAsset(w http.ResponseWriter, r *http.Request, auth TokenAuth) (any, error) {
	id, err := s.StartAudit(AuditEvent{UserID: auth.UserID, TokenID: auth.TokenID, Action: GatewayAction("inventory:asset:assign"), Session: "asset-scope"})
	if err != nil {
		return nil, machineInventoryProblem{503, "audit_unavailable", "audit unavailable"}
	}
	err = s.applyWorkspaceChange(w, r, auth, id)
	if err != nil {
		p := publicInventoryProblem(err)
		status := "error"
		if p.status == 403 {
			status = "forbidden"
		}
		if auditErr := s.FinishAudit(id, AuditFinish{Status: status, Error: p.code}); auditErr != nil {
			return nil, machineInventoryProblem{503, "audit_unavailable", "audit unavailable"}
		}
		return nil, err
	}
	return map[string]bool{"saved": true}, nil
}
func (s *GatewayStore) applyWorkspaceChange(w http.ResponseWriter, r *http.Request, auth TokenAuth, auditID int64) error {
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE audit_events SET status='executing' WHERE id=? AND status='running'`, auditID); err != nil {
		return err
	}
	if err = requireInventoryAdmin(tx, auth); err != nil {
		return err
	}
	var req scopeChange
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF || req.Assigned == nil || !monitorID(req.ID) || (req.Kind != "machine" && req.Kind != "system" && req.Kind != "cluster") {
		return machineBadRequest("invalid_assignment", "资产或分配参数无效。")
	}
	if err = validateWorkspaceTarget(tx, req.workspaceTarget); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+workspaceCatalog+` WHERE kind=? AND id=?)`,
		targetPairArgs(req.Cluster, req.Instance, 4, req.Kind, req.ID)...).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return machineInventoryProblem{404, "asset_not_found", "资产已不在台账中。"}
	}
	query := `DELETE FROM inventory_agent_assets WHERE cluster=? AND instance=? AND kind=? AND asset_id=?`
	if *req.Assigned {
		query = gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO inventory_agent_assets(cluster,instance,kind,asset_id) VALUES(?,?,?,?)", doNothing: true})
	}
	if _, err = tx.Exec(query, req.Cluster, req.Instance, req.Kind, req.ID); err != nil {
		return err
	}
	verb := "remove"
	if *req.Assigned {
		verb = "add"
	}
	if err = finishAuditWith(tx, auditID, AuditFinish{Status: "success", Cluster: req.Cluster, Instance: req.Instance, CommandSummary: verb + " " + req.Kind + " asset_id=" + req.ID}); err != nil {
		return err
	}
	return tx.Commit()
}
