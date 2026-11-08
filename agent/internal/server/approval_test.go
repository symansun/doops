package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/symansun/doops/agent/api"
)

func openApprovalTestStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestApprovalStoreLifecycle(t *testing.T) {
	store := openApprovalTestStore(t)

	apr := Approval{
		ID: "apr_t1",
		UserID: "usr_1", Cluster: "prod", Instance: "web-1",
		Action: ActionExec, ArgsHash: "abc", CommandSummary: "echo hi",
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := store.CreateApproval(apr); err != nil {
		t.Fatalf("create: %v", err)
	}
	if apr.ID != "apr_t1" {
		t.Fatalf("unexpected id %s", apr.ID)
	}

	got, err := store.GetApproval(apr.ID)
	if err != nil || got.Status != "pending" {
		t.Fatalf("get: %v status=%s", err, got.Status)
	}

	// 未审批前不能消费
	if _, err := store.ConsumeApproval(apr.ID); err == nil {
		t.Fatal("pending approval must not be consumable")
	}
	// approve -> consume -> 二次消费失败
	if err := store.DecideApproval(apr.ID, "usr_admin", true); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := store.ConsumeApproval(apr.ID); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if _, err := store.ConsumeApproval(apr.ID); err == nil {
		t.Fatal("consumed approval must not be consumable again")
	}

	// 驳回路径
	apr2 := Approval{ID: "apr_t2", UserID: "usr_1", Cluster: "prod", Instance: "web-1", Action: ActionExec, ArgsHash: "def", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.CreateApproval(apr2); err != nil {
		t.Fatalf("create2: %v", err)
	}
	if err := store.DecideApproval(apr2.ID, "usr_admin", false); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := store.DecideApproval(apr2.ID, "usr_admin", true); err == nil {
		t.Fatal("rejected approval must not be decidable again")
	}

	// 过期路径
	apr3 := Approval{ID: "apr_t3", UserID: "usr_1", Cluster: "prod", Instance: "web-1", Action: ActionExec, ArgsHash: "ghi", CreatedAt: time.Now().UTC().Add(-2 * time.Hour), ExpiresAt: time.Now().UTC().Add(-time.Hour)}
	if err := store.CreateApproval(apr3); err != nil {
		t.Fatalf("create3: %v", err)
	}
	if err := store.DecideApproval(apr3.ID, "usr_admin", true); err == nil {
		t.Fatal("expired approval must not be decidable")
	}
	got3, _ := store.GetApproval(apr3.ID)
	if got3.Status != "expired" {
		t.Fatalf("expected expired status, got %s", got3.Status)
	}

	// 列表过滤
	pendingList, err := store.ListApprovals("pending", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pendingList) != 0 { // 全部被处理过
		t.Fatalf("expected no pending approvals, got %d", len(pendingList))
	}
	all, _ := store.ListApprovals("", 0)
	if len(all) != 3 {
		t.Fatalf("expected 3 approvals, got %d", len(all))
	}
}

func TestApprovalRequiredMatching(t *testing.T) {
	hub := NewGatewayHub(nil, GatewayHubOptions{ApprovalTargets: []string{"prod/*", "staging/db-1"}})
	cases := []struct {
		cluster, instance string
		action            GatewayAction
		want              bool
	}{
		{"prod", "web-1", ActionExec, true},
		{"prod", "web-1", ActionWrite, true},
		{"prod", "web-1", ActionPush, true},
		{"prod", "web-1", ActionRead, false},
		{"prod", "web-1", ActionAsk, false},
		{"staging", "db-1", ActionExec, true},
		{"staging", "db-1", ActionRead, false},
		{"dev", "web-1", ActionExec, false},
	}
	for _, c := range cases {
		if got := hub.approvalRequired(c.cluster, c.instance, c.action); got != c.want {
			t.Fatalf("approvalRequired(%s/%s,%s)=%v want %v", c.cluster, c.instance, c.action, got, c.want)
		}
	}
	hub2 := NewGatewayHub(nil, GatewayHubOptions{})
	if hub2.approvalRequired("prod", "web-1", ActionExec) {
		t.Fatal("no approval targets configured -> never required")
	}
}

func TestGateApprovalFlow(t *testing.T) {
	store := openApprovalTestStore(t)
	hub := NewGatewayHub(store, GatewayHubOptions{ApprovalTargets: []string{"prod/*"}})
	auth := TokenAuth{UserID: "usr_1", TokenID: "tok_1", Kind: TokenKindUser}

	newCall := func(cmd string) *api.ToolCallParams {
		args, _ := json.Marshal(map[string]string{"command": cmd})
		return &api.ToolCallParams{Name: "doops_shell", Arguments: args}
	}

	var lastErr string
	writeJSON := func(v interface{}) error {
		b, _ := json.Marshal(v)
		lastErr = string(b)
		return nil
	}
	var audits []string
	finishAudit := func(status, errMsg, tail string, bytesOut int64) { audits = append(audits, status+":"+errMsg) }

	// 1. 首次调用：登记 pending 审批单并拒绝执行
	if hub.gateApproval(auth, "prod", "web-1", ActionExec, newCall("echo hi"), finishAudit, writeJSON, 1) {
		t.Fatal("first call must be gated")
	}
	if !strings.Contains(lastErr, "approval required: apr_") {
		t.Fatalf("expected approval-required error, got %s", lastErr)
	}
	pendingList, err := store.ListApprovals("pending", 1)
	if err != nil || len(pendingList) != 1 {
		t.Fatalf("expected 1 pending approval, got %d (%v)", len(pendingList), err)
	}
	approvalID := pendingList[0].ID

	// 2. 未审批直接重放 -> approval_invalid
	p2 := newCall("echo hi")
	p2.Arguments, _ = json.Marshal(map[string]string{"command": "echo hi", "approval_id": approvalID})
	if hub.gateApproval(auth, "prod", "web-1", ActionExec, p2, finishAudit, writeJSON, 2) {
		t.Fatal("unapproved replay must be rejected")
	}
	if !strings.Contains(lastErr, "is pending") && !strings.Contains(lastErr, "not found") {
		t.Fatalf("unexpected error: %s", lastErr)
	}

	// 3. 审批通过 -> 重放放行，approval_id 被剥离
	if err := store.DecideApproval(approvalID, "usr_admin", true); err != nil {
		t.Fatalf("approve: %v", err)
	}
	p3 := newCall("echo hi")
	p3.Arguments, _ = json.Marshal(map[string]string{"command": "echo hi", "approval_id": approvalID})
	if !hub.gateApproval(auth, "prod", "web-1", ActionExec, p3, finishAudit, writeJSON, 3) {
		t.Fatalf("approved replay must pass, last err: %s", lastErr)
	}
	if strings.Contains(string(p3.Arguments), "approval_id") {
		t.Fatalf("approval_id must be stripped before forwarding: %s", p3.Arguments)
	}

	// 4. 审批单一次性：再次重放被拒
	p4 := newCall("echo hi")
	p4.Arguments, _ = json.Marshal(map[string]string{"command": "echo hi", "approval_id": approvalID})
	if hub.gateApproval(auth, "prod", "web-1", ActionExec, p4, finishAudit, writeJSON, 4) {
		t.Fatal("consumed approval must not pass again")
	}

	// 5. 参数篡改检测：同一审批单换命令 -> mismatch
	aprB := Approval{ID: "apr_b1", UserID: "usr_1", Cluster: "prod", Instance: "web-1", Action: ActionExec,
		ArgsHash: hashApprovalArgs(json.RawMessage(`{"command":"echo a"}`)),
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.CreateApproval(aprB); err != nil {
		t.Fatalf("create B: %v", err)
	}
	if err := store.DecideApproval(aprB.ID, "usr_admin", true); err != nil {
		t.Fatalf("approve B: %v", err)
	}
	p5 := newCall("rm -rf /tmp/x")
	p5.Arguments, _ = json.Marshal(map[string]string{"command": "rm -rf /tmp/x", "approval_id": aprB.ID})
	if hub.gateApproval(auth, "prod", "web-1", ActionExec, p5, finishAudit, writeJSON, 5) {
		t.Fatal("tampered arguments must be rejected")
	}
	if !strings.Contains(lastErr, "mismatch") {
		t.Fatalf("expected mismatch error, got %s", lastErr)
	}

	// 6. 非审批目标直接放行
	if !hub.gateApproval(auth, "dev", "web-1", ActionExec, &api.ToolCallParams{Name: "doops_shell", Arguments: json.RawMessage(`{"command":"x"}`)}, finishAudit, writeJSON, 6) {
		t.Fatal("non-approval target must pass through")
	}
}

func TestHandleAdminApprovalsHTTP(t *testing.T) {
	store := openApprovalTestStore(t)
	user, err := store.CreateUser("admin")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionAdmin}}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	tok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "admin"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})

	if err := store.CreateApproval(Approval{UserID: user.ID, Cluster: "prod", Instance: "web-1", Action: ActionExec, ArgsHash: "h", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
		t.Fatalf("seed approval: %v", err)
	}
	pendings, _ := store.ListApprovals("pending", 0)
	if len(pendings) != 1 {
		t.Fatalf("seed pending: %d", len(pendings))
	}

	// 无 token -> 401
	rec := httptest.NewRecorder()
	hub.HandleAdminApprovals(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/approvals", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	// list pending
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/approvals?status=pending", nil)
	req.Header.Set("Authorization", "Bearer "+tok.Plaintext)
	rec = httptest.NewRecorder()
	hub.HandleAdminApprovals(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "apr_") {
		t.Fatalf("list failed: %d %s", rec.Code, rec.Body.String())
	}

	// approve
	body := strings.NewReader(fmt.Sprintf(`{"id":%q,"decision":"approve"}`, pendings[0].ID))
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/approvals", body)
	req.Header.Set("Authorization", "Bearer "+tok.Plaintext)
	rec = httptest.NewRecorder()
	hub.HandleAdminApprovals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve failed: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := store.GetApproval(pendings[0].ID)
	if got.Status != "approved" {
		t.Fatalf("expected approved, got %s", got.Status)
	}

	// 非法 decision
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/approvals", strings.NewReader(`{"id":"apr_x","decision":"maybe"}`))
	req.Header.Set("Authorization", "Bearer "+tok.Plaintext)
	rec = httptest.NewRecorder()
	hub.HandleAdminApprovals(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad decision, got %d", rec.Code)
	}
}
