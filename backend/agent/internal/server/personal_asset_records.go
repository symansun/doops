package server

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// These rows are notes owned by the authenticated account. They are not
// deployable resources, target assignments, connections or credential bindings.
type personalAsset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Revision    int64  `json:"revision"`
}

var assetLabel = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._()（）/-]{0,79}$`)

func personalAssetKind(k string) bool {
	switch k {
	case "database", "website", "k8s", "vm", "image":
		return true
	}
	return false
}
func (h *GatewayHub) HandlePersonalAssetRecords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	var result any
	switch r.Method {
	case "GET":
		result, err = h.store.listPersonalAssets(r, auth)
	case "POST":
		result, err = h.store.writePersonalAsset(w, r, auth)
	default:
		err = machineInventoryProblem{405, "method_not_allowed", "method not allowed"}
	}
	if err != nil {
		p := publicInventoryProblem(err)
		if p.code == "machine_inventory_unavailable" {
			p = machineInventoryProblem{503, "personal_asset_records_unavailable", "个人登记暂不可用，请检查数据库与迁移状态。"}
		}
		writeDoopsError(w, p.status, "", doopsErrorResponse{Error: p.code, Code: p.code, Message: p.message})
		return
	}
	writeJSONHTTP(w, result)
}
func (s *GatewayStore) listPersonalAssets(r *http.Request, a TokenAuth) (any, error) {
	kind := r.URL.Query().Get("kind")
	if !personalAssetKind(kind) {
		return nil, machineBadRequest("invalid_kind", "请选择资产类型。")
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT asset_id,name,environment,revision FROM personal_asset_records WHERE owner=? AND kind=? ORDER BY name, asset_id", a.UserID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []personalAsset{}
	for rows.Next() {
		var item personalAsset
		if err := rows.Scan(&item.ID, &item.Name, &item.Environment, &item.Revision); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return map[string]any{"records": out}, rows.Err()
}
func (s *GatewayStore) writePersonalAsset(w http.ResponseWriter, r *http.Request, a TokenAuth) (any, error) {
	var req struct {
		personalAsset
		Kind string `json:"kind"`
		Op   string `json:"op"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF || !personalAssetKind(req.Kind) || !monitorID(req.ID) {
		return nil, machineBadRequest("invalid_record", "资产类型、编号或字段无效。")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Environment = strings.TrimSpace(req.Environment)
	if !inventoryMetadata(req.Name) || !inventoryMetadata(req.Environment) || req.Op != "delete" && (!assetLabel.MatchString(req.Name) || (req.Environment != "" && !assetLabel.MatchString(req.Environment))) {
		return nil, machineBadRequest("invalid_label", "请填写名称，环境可留空；仅接受基本信息，不接受连接串或凭据。")
	}
	var query string
	var args []any
	switch req.Op {
	case "create":
		if req.Revision != 0 {
			return nil, machineBadRequest("invalid_revision", "新登记不应指定版本。")
		}
		query = "INSERT INTO personal_asset_records(owner,kind,asset_id,name,environment,revision) VALUES(?,?,?,?,?,1) ON CONFLICT DO NOTHING"
		args = []any{a.UserID, req.Kind, req.ID, req.Name, req.Environment}
	case "update":
		query = "UPDATE personal_asset_records SET name=?,environment=?,revision=revision+1 WHERE owner=? AND kind=? AND asset_id=? AND revision=?"
		args = []any{req.Name, req.Environment, a.UserID, req.Kind, req.ID, req.Revision}
	case "delete":
		query = "DELETE FROM personal_asset_records WHERE owner=? AND kind=? AND asset_id=? AND revision=?"
		args = []any{a.UserID, req.Kind, req.ID, req.Revision}
	default:
		return nil, machineBadRequest("invalid_action", "未知操作。")
	}
	id, err := s.StartAudit(AuditEvent{UserID: a.UserID, TokenID: a.TokenID, Action: GatewayAction("asset:record:" + req.Op), Session: "personal-inventory"})
	if err != nil {
		return nil, machineInventoryProblem{503, "audit_unavailable", "audit unavailable"}
	}
	err = func() error {
		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var res sql.Result
		res, err = tx.Exec(query, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return machineInventoryProblem{409, "record_conflict", "编号已存在或记录已变更，请刷新。"}
		}
		if err = finishAuditWith(tx, id, AuditFinish{Status: "success", CommandSummary: req.Kind + " metadata"}); err != nil {
			return machineInventoryProblem{503, "audit_unavailable", "audit unavailable"}
		}
		return tx.Commit()
	}()
	if err != nil {
		if auditErr := s.FinishAudit(id, AuditFinish{Status: "error", Error: publicInventoryProblem(err).code}); auditErr != nil {
			return nil, machineInventoryProblem{503, "audit_unavailable", "audit unavailable"}
		}
		return nil, err
	}
	return map[string]bool{"saved": true}, nil
}
