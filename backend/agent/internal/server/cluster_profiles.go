package server

import (
	"database/sql"
	"net/http"
)

type clusterProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Contact     string `json:"contact"`
	Revision    int64  `json:"revision"`
}

// A pending profile is private. Once imported, its audience is derived only from
// the existing source grants. Editing a shared name requires access to every source.
const profileVisible = "((NOT EXISTS(SELECT 1 FROM cluster_inventory_sources s WHERE s.asset_id=m.asset_id) AND m.created_by=?) OR EXISTS(SELECT 1 FROM cluster_inventory_sources s JOIN permitted p ON p.cluster IN ('*',s.cluster) AND p.instance IN ('*',s.instance) WHERE s.asset_id=m.asset_id))"

func (h *GatewayHub) HandleClusterProfiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	ctx := r.Context()
	if r.Method == "GET" {
		rows, e := h.store.db.QueryContext(ctx, inventoryGrants+"SELECT m.asset_id,m.name,m.environment,m.contact,m.revision FROM inventory_cluster_profiles m WHERE "+profileVisible+" ORDER BY m.name,m.asset_id", a.UserID, a.TokenID, a.UserID)
		if e != nil {
			catalogHTTP(w, nil, e)
			return
		}
		defer rows.Close()
		out := []clusterProfile{}
		for rows.Next() {
			var p clusterProfile
			if e = rows.Scan(&p.ID, &p.Name, &p.Environment, &p.Contact, &p.Revision); e != nil {
				break
			}
			out = append(out, p)
		}
		if e == nil {
			e = rows.Err()
		}
		catalogHTTP(w, map[string]any{"profiles": out}, e)
		return
	}
	if r.Method != "POST" {
		catalogHTTP(w, nil, assetIssue(405, "method_not_allowed"))
		return
	}
	var req struct {
		clusterProfile
		Op       string `json:"op"`
		Cluster  string `json:"cluster"`
		Instance string `json:"instance"`
		Verified string `json:"verified_at"`
	}
	if !catalogDecode(w, r, &req) {
		catalogHTTP(w, nil, assetIssue(400, "invalid_record"))
		return
	}
	switch req.Op {
	case "create":
		if req.ID != "" || req.Revision != 0 {
			catalogHTTP(w, nil, assetIssue(400, "invalid_record"))
			return
		}
		req.ID = "k-" + randomHex(12)
	case "update", "delete", "unlink":
		if !monitorID(req.ID) || req.Revision < 0 {
			catalogHTTP(w, nil, assetIssue(400, "invalid_record"))
			return
		}
	default:
		catalogHTTP(w, nil, assetIssue(400, "invalid_record"))
		return
	}
	if (req.Op == "create" || req.Op == "update") && (!catalogText(req.Name, false) || !catalogText(req.Contact, true) || !catalogText(req.Environment, true)) {
		catalogHTTP(w, nil, assetIssue(400, "invalid_label"))
		return
	}
	err = h.store.catalogChange(ctx, a, "cluster_profile:"+req.Op, func(tx *sql.Tx) error {
		if req.Op == "create" {
			_, e := tx.Exec("INSERT INTO inventory_cluster_profiles VALUES(?,?,?,?,?,1)", req.ID, req.Name, req.Environment, req.Contact, a.UserID)
			return e
		}
		var count int
		var outside bool
		e := tx.QueryRowContext(ctx, inventoryGrants+"SELECT COUNT(*), EXISTS(SELECT 1 FROM cluster_inventory_sources s WHERE s.asset_id=? AND NOT EXISTS(SELECT 1 FROM permitted p WHERE p.cluster IN ('*',s.cluster) AND p.instance IN ('*',s.instance))) FROM cluster_inventory_sources WHERE asset_id=?", a.UserID, a.TokenID, req.ID, req.ID).Scan(&count, &outside)
		if e != nil {
			return e
		}
		if outside {
			return assetIssue(403, "inventory_forbidden")
		}
		var rev int64
		var owner string
		e = tx.QueryRow("SELECT revision,created_by FROM inventory_cluster_profiles WHERE asset_id=?", req.ID).Scan(&rev, &owner)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if rev != req.Revision || count == 0 && (owner == "" || owner != a.UserID) {
			return assetIssue(409, "record_conflict")
		}
		switch req.Op {
		case "update":
			_, e = tx.Exec("INSERT INTO inventory_cluster_profiles VALUES(?,?,?,?,?,1) ON CONFLICT(asset_id) DO UPDATE SET name=excluded.name,environment=excluded.environment,contact=excluded.contact,revision=revision+1", req.ID, req.Name, req.Environment, req.Contact, a.UserID)
			if e == nil {
				_, e = tx.Exec("UPDATE cluster_inventory_sources SET name=? WHERE asset_id=?", req.Name, req.ID)
			}
			return e
		case "delete":
			if count != 0 {
				return assetIssue(409, "asset_has_sources")
			}
			_, e = tx.Exec("DELETE FROM inventory_cluster_profiles WHERE asset_id=?", req.ID)
			return e
		case "unlink":
			if !monitorID(req.Cluster) || !monitorID(req.Instance) || req.Verified == "" {
				return assetIssue(400, "invalid_record")
			}
			if e = authorizeInventory(ctx, tx, a, inventoryRequest{Action: "import", Cluster: req.Cluster, Instance: req.Instance}); e != nil {
				if _, ok := e.(inventoryFailure); ok {
					return assetIssue(403, "inventory_forbidden")
				}
				return e
			}
			var name string
			if e = tx.QueryRow("SELECT name FROM cluster_inventory_sources WHERE asset_id=? AND cluster=? AND instance=? AND verified_at=?", req.ID, req.Cluster, req.Instance, req.Verified).Scan(&name); e == sql.ErrNoRows {
				return assetIssue(409, "record_conflict")
			} else if e != nil {
				return e
			}
			var used bool
			if e = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM inventory_node_links WHERE cluster=? AND instance=?) OR EXISTS(SELECT 1 FROM inventory_business_deployments WHERE cluster=? AND instance=?) OR EXISTS(SELECT 1 FROM inventory_agent_assets WHERE kind='cluster' AND asset_id=?)", req.Cluster, req.Instance, req.Cluster, req.Instance, req.ID).Scan(&used); e != nil {
				return e
			}
			if used {
				return assetIssue(409, "cluster_source_in_use")
			}
			if count == 1 && owner == "" {
				if _, e = tx.Exec("INSERT INTO inventory_cluster_profiles VALUES(?,?, '', '',?,1)", req.ID, name, a.UserID); e != nil {
					return e
				}
			}
			if _, e = tx.Exec("DELETE FROM cluster_inventory_namespaces WHERE cluster=? AND instance=?", req.Cluster, req.Instance); e != nil {
				return e
			}
			_, e = tx.Exec("DELETE FROM cluster_inventory_sources WHERE cluster=? AND instance=?", req.Cluster, req.Instance)
			return e
		}
		return assetIssue(400, "invalid_record")
	})
	catalogHTTP(w, map[string]string{"id": req.ID}, err)
}
