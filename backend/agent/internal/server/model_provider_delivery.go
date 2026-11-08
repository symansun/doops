package server

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
)

const modelProviderDeliveryTimeout = 3 * time.Minute

// modelProviderBinding is one active modelProvider grant on a target.
type modelProviderBinding struct {
	Resource CredentialResource
	Grant    CredentialGrant
}

// ActiveModelProviderBindings lists the active modelProvider credentials that
// have an active grant on cluster/instance.
func (s *GatewayStore) ActiveModelProviderBindings(cluster, instance string) ([]modelProviderBinding, error) {
	rows, err := s.db.Query(`SELECT g.id, g.credential_id, g.grantee_id, g.scope_json, g.state, g.created_by, g.created_at, g.revoked_at
		FROM credentials c JOIN credential_grants g ON g.credential_id = c.id
		WHERE c.state = ? AND c.type = ? AND g.state = ? ORDER BY c.id, g.id`,
		CredentialStateActive, CredentialTypeModelProvider, CredentialStateActive)
	if err != nil {
		return nil, err
	}
	var grants []CredentialGrant
	for rows.Next() {
		grant, scanErr := scanCredentialGrant(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		scope := normalizeCredentialGrantScope(grant.Scope)
		if scope.Cluster == cluster && scope.Instance == instance {
			grants = append(grants, grant)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	bindings := make([]modelProviderBinding, 0, len(grants))
	for _, grant := range grants {
		resource, err := s.credentialByID(grant.CredentialID)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, modelProviderBinding{Resource: resource, Grant: grant})
	}
	return bindings, nil
}

// deliverModelProviders materializes the target's active modelProvider
// credential when its Agent connects, so a new or restarted Agent gets its
// model from the Gateway without a mounted settings file.
func (h *GatewayHub) deliverModelProviders(agent *GatewayAgent) {
	if agent == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelProviderDeliveryTimeout)
	defer cancel()
	category := h.deliverModelProvider(ctx, agent.Cluster, agent.Instance)
	if category != "" {
		log.Printf("[gateway] model provider delivery: %s category=%s", agent.Key, category)
	}
}

// deliverModelProvider returns "" on success or when the target has no model
// provider, and a stable error category otherwise.
func (h *GatewayHub) deliverModelProvider(ctx context.Context, cluster, instance string) string {
	bindings, err := h.store.ActiveModelProviderBindings(cluster, instance)
	if err != nil {
		return "model_provider_lookup_failed"
	}
	switch len(bindings) {
	case 0:
		return ""
	case 1:
	default:
		// One settings file holds one model provider; refuse to pick one.
		return "model_provider_ambiguous"
	}
	binding := bindings[0]
	unlock, err := h.acquireCredentialMutationLocks(ctx, []string{binding.Resource.ID})
	if err != nil {
		return "operation_canceled"
	}
	defer unlock()
	// The binding was read before the credential lock. A grant revoked while
	// this delivery waited for the lock has nothing to deliver any more.
	if err := h.store.CredentialGrantUsable(binding.Resource.ID, binding.Grant.ID); err != nil {
		return ""
	}
	version, err := h.store.ActiveCredentialVersion(binding.Resource.ID)
	if err != nil {
		// A credential that was never promoted has nothing to deliver.
		return ""
	}
	agent := h.registry.Get(cluster, instance)
	if agent != nil && agent.deliveredModelVersion() == version.ID {
		return ""
	}
	scope := normalizeCredentialGrantScope(binding.Grant.Scope)
	if scope.Use == "" {
		scope.Use = CredentialUseModelProvider
	}
	ref := CredentialPlanReference{Name: binding.Resource.Name, Scope: scope}
	sessionID := "model-delivery-" + randomHex(8)
	// The Agent may still hold a version of this grant that was superseded
	// while it was offline. Clear any version first so the delivery starts
	// from a known state and the previous-version check stays exact.
	if err := h.cleanupCredentialMaterializationContext(ctx, CredentialMaterializeRequest{
		SessionID: sessionID, Cluster: cluster, Instance: instance,
		CredentialID: binding.Resource.ID, GrantID: binding.Grant.ID, VersionID: version.ID,
		CredentialType: binding.Resource.Type, Scope: scope,
	}); err != nil {
		return credentialOperationErrorCategory(err)
	}
	if _, _, err := h.materializeCredentialVersion(
		ctx, cluster, instance, sessionID, binding.Resource, version, "", binding.Grant.ID, ref,
	); err != nil {
		return credentialOperationErrorCategory(err)
	}
	if agent = h.registry.Get(cluster, instance); agent != nil {
		agent.setDeliveredModelVersion(version.ID)
	}
	return ""
}

func (a *GatewayAgent) deliveredModelVersion() string {
	if a == nil {
		return ""
	}
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.modelVersionID
}

func (a *GatewayAgent) setDeliveredModelVersion(versionID string) {
	if a == nil {
		return
	}
	a.stateMu.Lock()
	a.modelVersionID = versionID
	a.stateMu.Unlock()
}

// modelProviderGrantConflicts reports whether another active modelProvider
// credential is already bound to the target of scope.
func (s *GatewayStore) modelProviderGrantConflicts(credentialID string, scope CredentialGrantScope) (bool, error) {
	scope = normalizeCredentialGrantScope(scope)
	bindings, err := s.ActiveModelProviderBindings(scope.Cluster, scope.Instance)
	if err != nil {
		return false, err
	}
	for _, binding := range bindings {
		if binding.Resource.ID != strings.TrimSpace(credentialID) {
			return true, nil
		}
	}
	return false, nil
}

// marshalModelProviderStatus is kept small so Agent metadata never carries keys.
func marshalModelProviderStatus(record modelProviderRecord, delivered bool) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"delivered": delivered, "credential_id": record.CredentialID, "version_id": record.VersionID,
	})
	return data
}
