package server

import (
	"context"
	"errors"
	"github.com/user/doops/agent/api"
	"strings"
	"time"
)

type authorizedCredentialMaterialization struct {
	Reference  CredentialPlanReference
	Authorized AuthorizedCredentialUse
}

type completedCredentialMaterialization struct {
	Materialization CredentialMaterialization
	Item            authorizedCredentialMaterialization
	SessionID       string
}

type credentialApplyPreparation struct {
	Bindings  []api.AgentCredentialBinding
	completed []completedCredentialMaterialization
}

func (h *GatewayHub) resolveExplicitCredentials(
	auth TokenAuth,
	cluster string,
	instance string,
	instruction string,
	names []string,
	action GatewayAction,
	skill string,
	credentialMode string,
	sqlCredential string,
) ([]authorizedCredentialMaterialization, error) {
	autoDatabase := skill == "dosql" && strings.TrimSpace(credentialMode) == "auto"
	autoHelm := !autoDatabase && automaticHelmCredentialEligible(action, skill)
	if len(names) == 0 && !autoDatabase && !autoHelm {
		return nil, nil
	}
	if !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
		return nil, ErrCredentialForbidden
	}
	// A change task pulls its Chart with the Helm credential already bound to
	// the target, so the operator does not name again what authorization has
	// already settled. Naming a registry, TLS or opaque credential does not
	// withdraw that: those write a Secret into the namespace and so must be
	// asked for by name, while the Chart download keeps the target's own Helm
	// credential. Discovery runs before the checks below, because a target with
	// no Helm credential must keep downloading a public Chart anonymously.
	if autoHelm {
		entries, err := h.store.ListCredentialAccess(auth.UserID, cluster, instance)
		if err != nil {
			return nil, err
		}
		// Explicit Helm references narrow the candidates. Otherwise the Agent
		// selects among all target bindings once the actual Chart origin is known.
		if !credentialReferencesIncludeHelm(names, entries) {
			names = append(names, helmCredentialIDs(entries)...)
			if len(names) == 0 {
				return nil, nil
			}
		}
	}
	if credentialScalarContainsInlineMaterial(instruction) {
		return nil, ErrInlineCredentialMaterial
	}
	// DoSql binds the target's database credentials by stable ID, so same-named
	// resources owned by different users cannot make the selection ambiguous.
	// A named selection (doops sql --credential) narrows that set to one
	// credential, e.g. a migration account beside the application account.
	if autoDatabase {
		entries, err := h.store.ListCredentialAccess(auth.UserID, cluster, instance)
		if err != nil {
			return nil, err
		}
		if selected := strings.TrimSpace(sqlCredential); selected != "" {
			names = databaseCredentialIDs(credentialEntriesNamed(entries, selected))
			switch len(names) {
			case 0:
				return nil, ErrDoSQLCredentialNotBound
			case 1:
			default:
				// Two owners use the same name; the credential ID tells them apart.
				return nil, ErrCredentialAmbiguous
			}
		} else {
			names = databaseCredentialIDs(entries)
		}
		if len(names) == 0 {
			// Discovery only lists promoted credentials; report an unpromoted
			// database binding as a lifecycle gap, not a missing binding.
			unpromoted, err := h.store.HasUnpromotedDatabaseBinding(cluster, instance)
			if err != nil {
				return nil, err
			}
			if unpromoted {
				return nil, ErrCredentialVersionNotFound
			}
			return nil, ErrDoSQLDatabaseBindingMissing
		}
	}
	names = uniqueCredentialReferences(names)
	authorized := make([]authorizedCredentialMaterialization, 0, len(names))
	seenHelmIDs := make(map[string]struct{}, len(names))
	for _, name := range names {
		request := CredentialUseRequest{
			ActorID: auth.UserID,
			Scope:   CredentialGrantScope{Cluster: cluster, Instance: instance},
		}
		if isCredentialResourceIDReference(name) {
			request.CredentialID = name
		} else {
			request.Credential = name
		}
		resolved, err := h.store.AuthorizeCredentialUse(request)
		if err != nil {
			return nil, err
		}
		if resolved.Resource.Type == CredentialTypeHelmRepository {
			if _, seen := seenHelmIDs[resolved.Resource.ID]; seen {
				continue
			}
			seenHelmIDs[resolved.Resource.ID] = struct{}{}
		}
		use := credentialUseForType(resolved.Resource.Type)
		if skill == "dosql" {
			use = CredentialUseDatabase
		}
		if err := credentialTypeAllowsUse(resolved.Resource.Type, use); err != nil {
			return nil, err
		}
		effectiveScope := normalizeCredentialGrantScope(resolved.Grant.Scope)
		effectiveScope.Use = use
		authorized = append(authorized, authorizedCredentialMaterialization{
			Reference:  CredentialPlanReference{Name: resolved.Resource.Name, Scope: effectiveScope},
			Authorized: resolved,
		})
	}
	return authorized, nil
}

func uniqueCredentialReferences(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, name)
	}
	return unique
}

// automaticHelmCredentialEligible reports whether this operation may use the
// target's bound Helm credential without the caller naming it.
//
// Only a change task qualifies (ActionExec is what an apply prompt resolves
// to), and only the deployment form: dosql owns its own automatic database
// selection, and doweb reaches apply for website changes that pull no Chart.
// The automatic set is limited to helmPull because materializing one opens a
// process-local broker socket and touches no cluster object, while a registry,
// TLS or opaque credential writes a Secret into the namespace; creating one of
// those still requires the operator to ask for it by name.
func automaticHelmCredentialEligible(action GatewayAction, skill string) bool {
	switch strings.TrimSpace(skill) {
	case "dosql", "doweb":
		return false
	}
	return action == ActionExec
}

// helmCredentialIDs returns the stable IDs of the active Helm repository
// credentials among entries, so two resources sharing a name cannot make the
// automatic selection ambiguous.
func helmCredentialIDs(entries []CredentialListEntry) []string {
	seen := make(map[string]struct{}, len(entries))
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != CredentialTypeHelmRepository || entry.State != CredentialStateActive {
			continue
		}
		for _, binding := range entry.Grants {
			use := binding.Scope.Use
			if use == "" {
				use = credentialUseForType(entry.Type)
			}
			if use != CredentialUseHelmPull {
				continue
			}
			if _, exists := seen[entry.ID]; !exists {
				ids = append(ids, entry.ID)
				seen[entry.ID] = struct{}{}
			}
			break
		}
	}
	return ids
}

// credentialReferencesIncludeHelm reports whether names already pick a Helm
// repository credential, by stable ID or by name. A task that names its own
// Helm credential has settled which repository the Chart comes from, so
// discovery must neither add a second one nor report ambiguity.
func credentialReferencesIncludeHelm(names []string, entries []CredentialListEntry) bool {
	helm := make(map[string]struct{}, len(entries))
	for _, id := range helmCredentialIDs(entries) {
		helm[id] = struct{}{}
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for _, entry := range credentialEntriesNamed(entries, name) {
			if _, ok := helm[entry.ID]; ok {
				return true
			}
		}
	}
	return false
}

func databaseCredentialIDs(entries []CredentialListEntry) []string {
	seen := make(map[string]struct{}, len(entries))
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != CredentialTypeOpaque || entry.State != CredentialStateActive {
			continue
		}
		for _, binding := range entry.Grants {
			use := binding.Scope.Use
			if use == "" {
				use = credentialUseForType(entry.Type)
			}
			if use != CredentialUseDatabase {
				continue
			}
			if _, exists := seen[entry.ID]; !exists {
				ids = append(ids, entry.ID)
				seen[entry.ID] = struct{}{}
			}
			break
		}
	}
	return ids
}

// credentialEntriesNamed keeps the entries whose resource name or ID is reference.
func credentialEntriesNamed(entries []CredentialListEntry, reference string) []CredentialListEntry {
	var matches []CredentialListEntry
	for _, entry := range entries {
		if entry.ID == reference || entry.Name == reference {
			matches = append(matches, entry)
		}
	}
	return matches
}

func isCredentialResourceIDReference(value string) bool {
	const prefix = "cred_"
	return strings.HasPrefix(value, prefix) && validLowerHex(strings.TrimPrefix(value, prefix), 24)
}

func (h *GatewayHub) materializeExplicitCredentialApply(
	ctx context.Context,
	auth TokenAuth,
	sessionID string,
	authorized []authorizedCredentialMaterialization,
) (credentialApplyPreparation, error) {
	preparation := credentialApplyPreparation{
		Bindings:  make([]api.AgentCredentialBinding, 0, len(authorized)),
		completed: make([]completedCredentialMaterialization, 0, len(authorized)),
	}
	for _, item := range authorized {
		cluster := item.Reference.Scope.Cluster
		instance := item.Reference.Scope.Instance
		// The audit carries only validated identifiers: a scope whose
		// cluster/instance is request text keeps them and its scope JSON out.
		target := credentialTargetAudit(item.Reference.Scope, item.Authorized.Grant.ID)
		auditID, err := h.store.StartAudit(AuditEvent{
			UserID: auth.UserID, TokenID: auth.TokenID, Cluster: target.Cluster, Instance: target.Instance,
			CredentialID: item.Authorized.Resource.ID, VersionID: item.Authorized.Version.ID,
			GrantID: item.Authorized.Grant.ID, Scope: target.Scope,
			CorrelationID: "credapply_" + randomHex(12), Action: ActionCredentialUse,
			Session: sessionID, CommandSummary: "credential materialize use=" + string(item.Reference.Scope.Use),
			StartedAt: time.Now().UTC(),
		})
		if err != nil {
			_ = h.rollbackCredentialPrepare(ctx, preparation.completed)
			return credentialApplyPreparation{}, ErrCredentialAuditUnavailable
		}
		materialization, request, err := h.materializeCredentialVersion(
			ctx, cluster, instance, sessionID, item.Authorized.Resource, item.Authorized.Version,
			item.Authorized.Version.ID, item.Authorized.Grant.ID, item.Reference,
		)
		if err != nil {
			_ = h.store.FinishAudit(auditID, AuditFinish{
				Status: "error", Error: credentialOperationErrorCategory(err), EndedAt: time.Now().UTC(),
			})
			_ = h.rollbackCredentialPrepare(ctx, preparation.completed)
			return credentialApplyPreparation{}, err
		}
		completed := completedCredentialMaterialization{
			Materialization: materialization, Item: item, SessionID: sessionID,
		}
		preparation.completed = append(preparation.completed, completed)
		if err := h.store.RecordCredentialVerification(CredentialVerification{
			CredentialID: item.Authorized.Resource.ID, VersionID: item.Authorized.Version.ID,
			GrantID: item.Authorized.Grant.ID, Use: item.Reference.Scope.Use,
			Request: request, Evidence: materialization,
		}); err != nil {
			_ = h.rollbackCredentialPrepare(ctx, preparation.completed)
			return credentialApplyPreparation{}, err
		}
		if err := h.store.FinishAudit(auditID, AuditFinish{
			Status: "success", Scope: target.Scope, ResourceVersion: materialization.ResourceVersion,
			Digest: materialization.Digest, EndedAt: time.Now().UTC(),
		}); err != nil {
			_ = h.rollbackCredentialPrepare(ctx, preparation.completed)
			return credentialApplyPreparation{}, ErrCredentialAuditUnavailable
		}
		preparation.Bindings = append(preparation.Bindings, api.AgentCredentialBinding{
			Name: referenceName(item.Reference), Use: string(item.Reference.Scope.Use),
			Namespace: item.Reference.Scope.Namespace, ResourceName: materialization.ResourceName,
			DatabaseAssetID: item.Reference.Scope.DatabaseAssetID, EnvironmentID: item.Reference.Scope.EnvironmentID,
			DatabaseEngine: item.Reference.Scope.DatabaseEngine, DatabaseName: item.Reference.Scope.DatabaseName,
			SecretType: materialization.SecretType, Keys: append([]string(nil), materialization.Keys...),
			ResourceVersion: materialization.ResourceVersion, Digest: materialization.Digest,
			CredentialID: item.Authorized.Resource.ID, GrantID: item.Authorized.Grant.ID,
			VersionID: item.Authorized.Version.ID, HelmRelease: item.Reference.Scope.HelmRelease,
			HelmChart:   item.Reference.Scope.HelmChart,
			ContextKind: materialization.ContextKind, ContextSocket: materialization.ContextSocket,
		})
	}
	return preparation, nil
}

func referenceName(reference CredentialPlanReference) string {
	return strings.TrimSpace(reference.Name)
}

func credentialTypeAllowsUse(credentialType CredentialType, use CredentialUse) error {
	valid := (credentialType == CredentialTypeRegistry && use == CredentialUseImagePull) ||
		(credentialType == CredentialTypeTLS && use == CredentialUseTLS) ||
		(credentialType == CredentialTypeOpaque && (use == CredentialUseOpaqueSecret || use == CredentialUseDatabase)) ||
		(credentialType == CredentialTypeHelmRepository && use == CredentialUseHelmPull) ||
		(credentialType == CredentialTypeGitToken && use == CredentialUseGitCheckout) ||
		(credentialType == CredentialTypeModelProvider && use == CredentialUseModelProvider)
	if !valid {
		return errors.New("credential_use_mismatch")
	}
	return nil
}

func credentialUseForType(credentialType CredentialType) CredentialUse {
	switch credentialType {
	case CredentialTypeRegistry:
		return CredentialUseImagePull
	case CredentialTypeTLS:
		return CredentialUseTLS
	case CredentialTypeHelmRepository:
		return CredentialUseHelmPull
	case CredentialTypeGitToken:
		return CredentialUseGitCheckout
	case CredentialTypeOpaque:
		return CredentialUseOpaqueSecret
	case CredentialTypeModelProvider:
		return CredentialUseModelProvider
	default:
		return ""
	}
}

func (h *GatewayHub) rollbackCredentialPrepare(ctx context.Context, completed []completedCredentialMaterialization) error {
	var rollbackErr error
	for index := len(completed) - 1; index >= 0; index-- {
		entry := completed[index]
		request := credentialMaterializeRequest(entry.SessionID, entry.Item.Authorized, entry.Item.Reference)
		request.VersionID = entry.Materialization.VersionID
		if entry.Materialization.PreviousVersionID == "" {
			if err := h.cleanupCredentialVersion(ctx, request); err != nil {
				rollbackErr = err
			}
			continue
		}
		previous, err := h.store.CredentialVersionByID(request.CredentialID, entry.Materialization.PreviousVersionID)
		if err != nil {
			rollbackErr = err
			continue
		}
		if _, _, err := h.materializeCredentialVersion(
			ctx, request.Cluster, request.Instance, request.SessionID,
			entry.Item.Authorized.Resource, previous, request.VersionID,
			request.GrantID, entry.Item.Reference,
		); err != nil {
			rollbackErr = err
		}
	}
	return rollbackErr
}
