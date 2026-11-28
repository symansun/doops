package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestForceOfflineGrantRevocationGuardsAndSuccess(t *testing.T) {
	t.Run("non-admin personal owner forbidden", func(t *testing.T) {
		f := newRevocationFixture(t)
		owner, err := f.store.CreateUser("personal-owner")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.GrantUser(owner.ID, ScopeGrant{Cluster: "dev", Instance: renamedInstance}); err != nil {
			t.Fatal(err)
		}
		ownerTok, err := f.store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: owner.ID, Name: "owner-tok"})
		if err != nil {
			t.Fatal(err)
		}
		res, err := f.store.CreateCredential(CredentialCreateRequest{
			Name: "personal-force-offline-test", Scope: CredentialScopePersonal, Type: CredentialTypeOpaque,
			OwnerID: owner.ID, CreatedBy: owner.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		grant, err := f.store.CreateCredentialGrant(CredentialGrantCreateRequest{
			CredentialID: res.ID, CreatedBy: owner.ID,
			Scope: CredentialGrantScope{Cluster: "dev", Instance: renamedInstance, Namespace: "apps", Use: CredentialUseOpaqueSecret},
		})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{
			"grant_id": grant.ID, "force_offline": true, "reason": "cluster decommissioned",
		})
		resp := f.wait(t, f.sendAs(t, ownerTok, http.MethodDelete, "/v1/credentials/"+res.ID+"/grants", string(body)))
		if resp.code != http.StatusForbidden || strings.TrimSpace(resp.body) != "credential_force_offline_forbidden" {
			t.Fatalf("non-admin force-offline = HTTP %d %q, want 403 credential_force_offline_forbidden", resp.code, resp.body)
		}
		grants, err := f.store.CredentialGrants(res.ID)
		if err != nil || len(grants) != 1 || grants[0].State != CredentialStateActive {
			t.Fatalf("grant mutated on forbidden force-offline: %#v, %v", grants, err)
		}
	})

	t.Run("invalid reason rejected", func(t *testing.T) {
		f := newRevocationFixture(t)
		renamed := f.renamedGrant(t)
		cases := []struct {
			name string
			body map[string]any
			want string
		}{
			{"missing reason", map[string]any{"grant_id": renamed.ID, "force_offline": true}, "credential_force_offline_reason_required"},
			{"blank reason", map[string]any{"grant_id": renamed.ID, "force_offline": true, "reason": "   "}, "credential_force_offline_reason_required"},
			{"control char", map[string]any{"grant_id": renamed.ID, "force_offline": true, "reason": "line1\nline2"}, "credential_force_offline_reason_required"},
			{"too long", map[string]any{"grant_id": renamed.ID, "force_offline": true, "reason": strings.Repeat("a", 257)}, "credential_force_offline_reason_required"},
			{"reason without flag", map[string]any{"grant_id": renamed.ID, "reason": "cluster gone"}, "credential_force_offline_reason_unexpected"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				raw, _ := json.Marshal(tc.body)
				resp := f.wait(t, f.sendAs(t, f.admin, http.MethodDelete, "/v1/credentials/"+f.resource.ID+"/grants", string(raw)))
				if resp.code != http.StatusBadRequest || strings.TrimSpace(resp.body) != tc.want {
					t.Fatalf("got HTTP %d %q, want 400 %s", resp.code, resp.body, tc.want)
				}
			})
		}
	})

	t.Run("rejects online target holding uncleaned materialization", func(t *testing.T) {
		f := newRevocationFixture(t)
		f.record(t, "api", "verified", "")
		_ = f.agent(t, "force-offline-online-grant")
		raw, _ := json.Marshal(map[string]any{
			"grant_id": f.grant.ID, "force_offline": true, "reason": "should fail while online",
		})
		resp := f.wait(t, f.sendAs(t, f.admin, http.MethodDelete, "/v1/credentials/"+f.resource.ID+"/grants", string(raw)))
		if resp.code != http.StatusConflict || strings.TrimSpace(resp.body) != "credential_force_offline_target_online: dev/local" {
			t.Fatalf("online force-offline = HTTP %d %q, want 409 credential_force_offline_target_online: dev/local", resp.code, resp.body)
		}
		if got := f.rowsByTarget(t); got != "local=verified/" {
			t.Fatalf("online target materialization was altered: %s", got)
		}
		if got := f.lifecycleStates(t); got != "resource=active local=revoking" {
			t.Fatalf("lifecycle = %s, want resource=active local=revoking", got)
		}
	})

	t.Run("finalizes offline target grant with audit and no payload leakage", func(t *testing.T) {
		f := newRevocationFixture(t)
		renamed := f.renamedGrant(t)
		f.recordRequest(t, f.requestFor(renamed), "verified", "")

		raw, _ := json.Marshal(map[string]any{
			"grant_id": renamed.ID, "force_offline": true, "reason": "decommissioned cluster 2026-11",
		})
		resp := f.wait(t, f.sendAs(t, f.admin, http.MethodDelete, "/v1/credentials/"+f.resource.ID+"/grants", string(raw)))
		if resp.code != http.StatusOK {
			t.Fatalf("force-offline grant revoke = HTTP %d %q", resp.code, resp.body)
		}
		var revokedGrant CredentialGrant
		if err := json.Unmarshal([]byte(resp.body), &revokedGrant); err != nil {
			t.Fatal(err)
		}
		if revokedGrant.State != CredentialStateRevoked || revokedGrant.RevokedAt == nil {
			t.Fatalf("grant = %#v, want revoked", revokedGrant)
		}
		if got := f.rowsByTarget(t); got != "renamed=abandoned_offline/target_decommissioned_force_offline" {
			t.Fatalf("rows = %s", got)
		}
		latest := f.lastRevokeAudit(t)
		if latest.Status != "success" || !strings.Contains(latest.Tail, "force_offline=true") ||
			!strings.Contains(latest.Tail, "dev/renamed") ||
			!strings.Contains(latest.Tail, "decommissioned cluster 2026-11") {
			t.Fatalf("audit missing force-offline metadata: %+v", latest)
		}
	})
}

func TestForceOfflineResourceRevocationMixedAndAuditFailClosed(t *testing.T) {
	t.Run("online target blocks force-offline until cleaned, then offline target abandons cleanly", func(t *testing.T) {
		f := newRevocationFixture(t)
		renamed := f.renamedGrant(t)
		f.record(t, "api", "verified", "")
		f.recordRequest(t, f.requestFor(renamed), "verified", "")
		agent := f.agent(t, "force-offline-mixed")

		// 1) Attempting force-offline while dev/local is still online must fail 409.
		forceBody, _ := json.Marshal(map[string]any{
			"force_offline": true, "reason": "renamed cluster retired",
		})
		blocked := f.wait(t, f.sendAs(t, f.admin, http.MethodPost, "/v1/credentials/"+f.resource.ID+"/revoke", string(forceBody)))
		if blocked.code != http.StatusConflict || strings.TrimSpace(blocked.body) != "credential_force_offline_target_online: dev/local" {
			t.Fatalf("mixed online force-offline = HTTP %d %q, want 409 dev/local", blocked.code, blocked.body)
		}
		if got := f.rowsByTarget(t); got != "local=verified/ renamed=verified/" {
			t.Fatalf("rows changed on rejected force-offline: %s", got)
		}

		// 2) Normal revoke cleans online dev/local and stops on offline dev/renamed.
		done := f.revokeResource(t)
		call := agent.call(t, "doops_credential_cleanup", revocationSession)
		tokenLifecycleReplyText(t, agent, call, `{"status":"removed"}`, false)
		normalResp := f.wait(t, done)
		if normalResp.code != http.StatusServiceUnavailable || strings.TrimSpace(normalResp.body) != "target_offline: dev/renamed" {
			t.Fatalf("normal revoke = HTTP %d %q", normalResp.code, normalResp.body)
		}
		if got := f.rowsByTarget(t); got != "local=cleaned/ renamed=verified/" {
			t.Fatalf("rows after normal revoke = %s", got)
		}

		// 3) Now only offline dev/renamed remains uncleaned; force-offline finishes cleanly.
		forced := f.wait(t, f.sendAs(t, f.admin, http.MethodPost, "/v1/credentials/"+f.resource.ID+"/revoke", string(forceBody)))
		if forced.code != http.StatusOK {
			t.Fatalf("force-offline resource revoke = HTTP %d %q", forced.code, forced.body)
		}
		var payload struct {
			CredentialID     string   `json:"credential_id"`
			Revoked          bool     `json:"revoked"`
			ForceOffline     bool     `json:"force_offline"`
			AbandonedTargets []string `json:"abandoned_targets"`
		}
		if err := json.Unmarshal([]byte(forced.body), &payload); err != nil {
			t.Fatal(err)
		}
		if !payload.Revoked || !payload.ForceOffline || len(payload.AbandonedTargets) != 1 || payload.AbandonedTargets[0] != "dev/renamed" {
			t.Fatalf("force-offline payload = %+v", payload)
		}
		if got := f.lifecycleStates(t); got != "resource=revoked local=revoked renamed=revoked" {
			t.Fatalf("lifecycle = %s", got)
		}
		if got := f.rowsByTarget(t); got != "local=cleaned/ renamed=abandoned_offline/target_decommissioned_force_offline" {
			t.Fatalf("rows = %s", got)
		}
		audits := f.resourceRevokeAudits(t)
		if len(audits) == 0 || audits[0].Status != "success" ||
			!strings.Contains(audits[0].Tail, "force_offline=true") ||
			!strings.Contains(audits[0].Tail, "dev/renamed") {
			t.Fatalf("resource force-offline audit = %+v", audits)
		}

		// 4) Idempotent retry succeeds.
		retry := f.wait(t, f.sendAs(t, f.admin, http.MethodPost, "/v1/credentials/"+f.resource.ID+"/revoke", string(forceBody)))
		if retry.code != http.StatusOK {
			t.Fatalf("idempotent retry = HTTP %d %q", retry.code, retry.body)
		}
	})

	t.Run("invalid json body rejected", func(t *testing.T) {
		f := newRevocationFixture(t)
		resp := f.wait(t, f.sendAs(t, f.admin, http.MethodPost, "/v1/credentials/"+f.resource.ID+"/revoke", `{"unknown":true}`))
		if resp.code != http.StatusBadRequest || strings.TrimSpace(resp.body) != "invalid credential revoke request" {
			t.Fatalf("unknown field = HTTP %d %q", resp.code, resp.body)
		}
	})

	t.Run("audit failure fails closed", func(t *testing.T) {
		f := newRevocationFixture(t)
		renamed := f.renamedGrant(t)
		f.recordRequest(t, f.requestFor(renamed), "verified", "")
		f.failCredentialAudit(t)
		forceBody, _ := json.Marshal(map[string]any{
			"force_offline": true, "reason": "audit closed test",
		})
		resp := f.wait(t, f.sendAs(t, f.admin, http.MethodPost, "/v1/credentials/"+f.resource.ID+"/revoke", string(forceBody)))
		if resp.code != http.StatusInternalServerError || strings.TrimSpace(resp.body) != "credential_audit_unavailable" {
			t.Fatalf("audit unavailable = HTTP %d %q, want 500 credential_audit_unavailable", resp.code, resp.body)
		}
		if got := f.lifecycleStates(t); got != "resource=active local=active renamed=active" {
			t.Fatalf("state changed despite audit failure: %s", got)
		}
	})
}
