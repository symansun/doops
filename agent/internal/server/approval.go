package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/symansun/doops/agent/api"
)

// approvalTTL 是审批单的有效期；超时自动置为 expired。
const approvalTTL = 24 * time.Hour

// approvalRequired 判断该调用是否命中审批策略：
// 仅变更类 action（exec/write/push）需要审批；目标匹配 opts.ApprovalTargets
// 中任一 glob（"cluster/instance"，支持 * 通配，如 "prod/*"、"*"）。
func (h *GatewayHub) approvalRequired(cluster, instance string, action GatewayAction) bool {
	switch action {
	case ActionExec, ActionWrite, ActionPush:
	default:
		return false
	}
	target := cluster + "/" + instance
	for _, p := range h.opts.ApprovalTargets {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if ok, _ := path.Match(p, target); ok {
			return true
		}
	}
	return false
}

// splitApprovalID 从工具参数中拆出 approval_id，返回（去掉 approval_id 的参数， approval_id）。
// 参数不是 JSON object 时原样返回。
func splitApprovalID(args json.RawMessage) (json.RawMessage, string) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil || m == nil {
		return args, ""
	}
	raw, ok := m["approval_id"]
	if !ok {
		return args, ""
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil {
		return args, ""
	}
	delete(m, "approval_id")
	cleaned, err := json.Marshal(m)
	if err != nil {
		return args, ""
	}
	return cleaned, id
}

// hashApprovalArgs 对去掉 approval_id 的参数做规范化 hash（map 序列化 key 有序）。
func hashApprovalArgs(args json.RawMessage) string {
	cleaned, _ := splitApprovalID(args)
	sum := sha256.Sum256(cleaned)
	return hex.EncodeToString(sum[:])
}

// gateApproval 在授权通过后、转发前执行审批闸：
// 未携带 approval_id 时登记 pending 审批单并返回引导错误；
// 携带 approval_id 时校验单据与本调用完全一致（发起人/目标/action/参数 hash）后一次性消费。
// 返回 true 表示放行，false 表示已写回错误响应、调用方应直接 return。
func (h *GatewayHub) gateApproval(auth TokenAuth, cluster, instance string, action GatewayAction, params *api.ToolCallParams, finishAudit func(status, errMsg, tail string, bytesOut int64), writeJSON func(interface{}) error, reqID interface{}) bool {
	if !h.approvalRequired(cluster, instance, action) {
		return true
	}
	cleanArgs, approvalID := splitApprovalID(params.Arguments)
	argsHash := hashApprovalArgs(params.Arguments)
	summary := summarizeToolCall(params.Name, params.Arguments)

	if approvalID == "" {
		apr := Approval{
			ID:             "apr_" + randomHex(12),
			UserID:         auth.UserID,
			Cluster:        cluster,
			Instance:       instance,
			Action:         action,
			ArgsHash:       argsHash,
			CommandSummary: summary,
			CreatedAt:      time.Now().UTC(),
			ExpiresAt:      time.Now().UTC().Add(approvalTTL),
		}
		if err := h.store.CreateApproval(apr); err != nil {
			finishAudit("error", "create approval failed", "", 0)
			writeJSON(buildErrorResponse(reqID, -32603, "create approval failed"))
			return false
		}
		errMsg := fmt.Sprintf("approval required: %s（%s %s/%s: %s）— 审批人执行 doops admin approve --id %s 后，重发同一命令并加 --approval %s",
			apr.ID, action, cluster, instance, summary, apr.ID, apr.ID)
		finishAudit("pending_approval", errMsg, "", 0)
		h.notifyAlert("approval_required", auth.UserID, cluster, instance, string(action), summary)
		writeJSON(buildErrorResponse(reqID, -32010, errMsg))
		return false
	}

	apr, err := h.store.GetApproval(approvalID)
	if err != nil {
		errMsg := fmt.Sprintf("approval not found: %s", approvalID)
		finishAudit("approval_invalid", errMsg, "", 0)
		writeJSON(buildErrorResponse(reqID, -32011, errMsg))
		return false
	}
	mismatch := func(field string) bool {
		errMsg := fmt.Sprintf("approval %s mismatch: %s", approvalID, field)
		finishAudit("approval_invalid", errMsg, "", 0)
		writeJSON(buildErrorResponse(reqID, -32011, errMsg))
		return false
	}
	if apr.UserID != auth.UserID {
		return mismatch("requester")
	}
	if apr.Cluster != cluster || apr.Instance != instance {
		return mismatch("target")
	}
	if apr.Action != action {
		return mismatch("action")
	}
	if apr.ArgsHash != argsHash {
		return mismatch("arguments")
	}
	if _, err := h.store.ConsumeApproval(approvalID); err != nil {
		finishAudit("approval_invalid", err.Error(), "", 0)
		writeJSON(buildErrorResponse(reqID, -32011, err.Error()))
		return false
	}
	// 放行：剥掉 approval_id 再转发，避免 agent 侧 schema 见到未知字段
	params.Arguments = cleanArgs
	return true
}

type approvalDecisionRequest struct {
	ID       string `json:"id"`
	Decision string `json:"decision"` // approve / reject
}

// HandleAdminApprovals GET 列表（?status=pending），POST {id, decision} 审批/驳回。
func (h *GatewayHub) HandleAdminApprovals(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	if !h.store.UserHasAction(auth.UserID, ActionAdmin) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		list, err := h.store.ListApprovals(status, 200)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"approvals": list})
	case http.MethodPost:
		var req approvalDecisionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "invalid approval request", http.StatusBadRequest)
			return
		}
		var approve bool
		switch strings.TrimSpace(req.Decision) {
		case "approve":
			approve = true
		case "reject":
			approve = false
		default:
			http.Error(w, "decision must be approve or reject", http.StatusBadRequest)
			return
		}
		auditID, _ := h.store.StartAudit(AuditEvent{
			UserID:         auth.UserID,
			TokenID:        auth.TokenID,
			Cluster:        "*",
			Instance:       "*",
			Action:         ActionAdmin,
			Session:        "admin-approval-decide",
			CommandSummary: fmt.Sprintf("approval %s %s", req.Decision, req.ID),
			StartedAt:      time.Now().UTC(),
		})
		if err := h.store.DecideApproval(req.ID, auth.UserID, approve); err != nil {
			_ = h.store.FinishAudit(auditID, AuditFinish{Status: "error", Error: err.Error(), EndedAt: time.Now().UTC()})
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = h.store.FinishAudit(auditID, AuditFinish{Status: "success", EndedAt: time.Now().UTC()})
		if apr, err := h.store.GetApproval(req.ID); err == nil {
			h.notifyAlert("approval_"+strings.TrimSpace(req.Decision), auth.UserID, apr.Cluster, apr.Instance, string(apr.Action), apr.CommandSummary)
		}
		writeJSONHTTP(w, map[string]string{"id": strings.TrimSpace(req.ID), "decision": req.Decision})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
