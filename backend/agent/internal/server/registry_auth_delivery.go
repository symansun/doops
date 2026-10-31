package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
)

const registryAuthDeliveryTimeout = 3 * time.Minute

// registryAuthBinding is one active registry grant on a target.
type registryAuthBinding struct {
	Resource CredentialResource
	Grant    CredentialGrant
}

// ActiveRegistryBindings lists the active registry credentials that have an
// active grant on cluster/instance. Unlike the model provider, a target may
// hold several registry credentials: each one authenticates a different
// registry, and the Agent's authfile holds all of them at once.
func (s *GatewayStore) ActiveRegistryBindings(cluster, instance string) ([]registryAuthBinding, error) {
	rows, err := s.db.Query(`SELECT g.id, g.credential_id, g.grantee_id, g.scope_json, g.state, g.created_by, g.created_at, g.revoked_at
		FROM credentials c JOIN credential_grants g ON g.credential_id = c.id
		WHERE c.state = ? AND c.type = ? AND g.state = ? ORDER BY c.id, g.id`,
		CredentialStateActive, CredentialTypeRegistry, CredentialStateActive)
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
	bindings := make([]registryAuthBinding, 0, len(grants))
	for _, grant := range grants {
		resource, err := s.credentialByID(grant.CredentialID)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, registryAuthBinding{Resource: resource, Grant: grant})
	}
	return bindings, nil
}

// deliverRegistryAuthFiles materializes the target's active registry
// credentials when its Agent connects, so a new or restarted Agent gets its
// registries back from the Gateway. Without this, the authfile an OCI tool
// reads disappears with the Pod and the operator has to write it again.
//
// Only the authfile is delivered here. The Kubernetes Secret is the kubelet's
// pull credential and stays tied to the workload it was granted for, so this
// path neither creates a Secret nor patches a workload's imagePullSecrets.
func (h *GatewayHub) deliverRegistryAuthFiles(agent *GatewayAgent) {
	if agent == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), registryAuthDeliveryTimeout)
	defer cancel()
	category := h.deliverRegistryAuthFilesTo(ctx, agent.Cluster, agent.Instance)
	if category != "" {
		log.Printf("[gateway] registry auth delivery: %s category=%s", agent.Key, category)
	}
}

// deliverRegistryAuthFilesTo returns "" on success or when the target has no
// registry credential, and a stable error category otherwise.
func (h *GatewayHub) deliverRegistryAuthFilesTo(ctx context.Context, cluster, instance string) string {
	bindings, err := h.store.ActiveRegistryBindings(cluster, instance)
	if err != nil {
		return "registry_auth_lookup_failed"
	}
	if len(bindings) == 0 {
		return ""
	}
	for _, binding := range bindings {
		unlock, err := h.acquireCredentialMutationLocks(ctx, []string{binding.Resource.ID})
		if err != nil {
			return "operation_canceled"
		}
		category := h.deliverRegistryAuthFileTo(ctx, cluster, instance, binding)
		unlock()
		if category != "" {
			return category
		}
	}
	return ""
}

func (h *GatewayHub) deliverRegistryAuthFileTo(
	ctx context.Context, cluster, instance string, binding registryAuthBinding,
) string {
	version, err := h.store.ActiveCredentialVersion(binding.Resource.ID)
	if err != nil {
		// A credential that was never promoted has nothing to deliver.
		return ""
	}
	scope := normalizeCredentialGrantScope(binding.Grant.Scope)
	if scope.Use == "" {
		scope.Use = CredentialUseImagePull
	}
	// The authfile is delivered on its own: the Secret belongs to the workload
	// the grant names, and a delivery that only restores the file must not
	// create one or touch an imagePullSecret list.
	scope.Namespace = scope.Namespace
	ref := CredentialPlanReference{Name: binding.Resource.Name, Scope: scope}
	sessionID := "registry-auth-delivery-" + randomHex(8)
	if _, _, err := h.materializeCredentialVersion(
		ctx, cluster, instance, sessionID, binding.Resource, version, "", binding.Grant.ID, ref,
	); err != nil {
		return credentialOperationErrorCategory(err)
	}
	return ""
}

// marshalRegistryAuthStatus is kept small so Agent metadata never carries keys.
func marshalRegistryAuthStatus(record registryAuthRecord) json.RawMessage {
	servers := make([]string, 0, len(record.Entries))
	for _, entry := range record.Entries {
		servers = append(servers, entry.Server)
	}
	data, _ := json.Marshal(map[string]any{
		"delivered": len(record.Entries) > 0, "servers": servers,
	})
	return data
}

// registryAuthDeliveryError reports a delivery failure as a stable category.
func registryAuthDeliveryError(err error) string {
	if err == nil {
		return ""
	}
	if strings.TrimSpace(err.Error()) == "" {
		return "registry_auth_delivery_failed"
	}
	return "registry_auth_delivery_failed"
}

// errRegistryAuthDeliveryUnavailable marks a delivery that could not write.
var errRegistryAuthDeliveryUnavailable = errors.New("registry_authfile_unavailable")
