package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
)

func (h *GatewayHub) cleanupCredentialVersion(ctx context.Context, request CredentialMaterializeRequest) error {
	arguments := map[string]any{
		"session_id": request.SessionID, "credential_id": request.CredentialID,
		"grant_id": request.GrantID, "version_id": request.VersionID,
		"namespace": request.Scope.Namespace, "workload": request.Scope.Workload,
		"credential_type": request.CredentialType,
	}
	if request.AuthfileOnly {
		// The restore owns only the Agent's authfile. The Kubernetes Secret
		// belongs to the workload pulling with it, so a failed restore must
		// not remove it.
		arguments["authfile_only"] = true
	}
	if _, err := h.RunInternalToolCall(withoutReconnectWait(ctx), request.Cluster, request.Instance, "doops_credential_cleanup", arguments); err != nil {
		return err
	}
	return h.store.MarkCredentialMaterializationCleaned(request)
}

func (h *GatewayHub) cleanupCredentialMaterializationContext(
	ctx context.Context,
	request CredentialMaterializeRequest,
) error {
	arguments := map[string]any{
		"session_id": request.SessionID, "credential_id": request.CredentialID,
		"grant_id": request.GrantID, "version_id": request.VersionID, "any_version": true,
		"namespace": request.Scope.Namespace, "workload": request.Scope.Workload,
		"credential_type": request.CredentialType,
	}
	if _, err := h.RunInternalToolCall(withoutReconnectWait(ctx), request.Cluster, request.Instance, "doops_credential_cleanup", arguments); err != nil {
		return err
	}
	return h.store.MarkCredentialMaterializationContextCleaned(request)
}

func (h *GatewayHub) cleanupCredentialGrantMaterializations(
	ctx context.Context,
	credentialID string,
	grantID string,
) error {
	materializations, err := h.store.CredentialMaterializations(credentialID, "", grantID)
	if err != nil {
		return err
	}
	_, err = h.cleanupCredentialMaterializationContexts(ctx, materializations)
	return err
}

// cleanupCredentialMaterializationContexts cleans every context in records,
// once each. A failed context does not stop the others, so one unreachable
// target does not leave Secrets behind on the reachable ones. It returns the
// sorted cluster/instance of every target that still holds an uncleaned context.
func (h *GatewayHub) cleanupCredentialMaterializationContexts(
	ctx context.Context,
	records []CredentialMaterializationRecord,
) ([]string, error) {
	var failures []error
	var unfinished []string
	for _, request := range pendingCredentialCleanups(records) {
		if err := h.cleanupCredentialMaterializationContext(ctx, request); err != nil {
			failures = append(failures, err)
			if target := request.Cluster + "/" + request.Instance; !containsString(unfinished, target) {
				unfinished = append(unfinished, target)
			}
		}
	}
	sort.Strings(unfinished)
	return unfinished, errors.Join(failures...)
}

// uncleanedCredentialTargets lists, sorted, the cluster/instance of every
// target that still holds a context of the credential needing a cleanup. It is
// best effort: a failed read returns no targets and the caller still fails.
func (h *GatewayHub) uncleanedCredentialTargets(credentialID string) []string {
	records, err := h.store.CredentialMaterializations(credentialID, "", "")
	if err != nil {
		return nil
	}
	var targets []string
	for _, record := range records {
		if target := record.Request.Cluster + "/" + record.Request.Instance; !containsString(targets, target) {
			targets = append(targets, target)
		}
	}
	sort.Strings(targets)
	return targets
}

// pendingCredentialCleanups keeps one request per materialization context.
// The records come from CredentialMaterializations, which leaves out contexts
// that provably own nothing, so an offline target whose attempts all left it
// unchanged gets no cleanup call and does not block revocation.
func pendingCredentialCleanups(records []CredentialMaterializationRecord) []CredentialMaterializeRequest {
	var pending []CredentialMaterializeRequest
	seen := make(map[string]struct{})
	for _, record := range records {
		key := credentialMaterializationContextKey(record.Request)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		pending = append(pending, record.Request)
	}
	return pending
}

// credentialCleanupFailure names why a cleanup did not finish. A target that
// never received the call is reported as offline, busy or queued; anything that
// may have reached the Agent keeps the caller's outcome-unknown category, and
// one such failure among several decides the result.
func credentialCleanupFailure(err error, unknown string) (string, int) {
	failures := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		failures = joined.Unwrap()
	}
	category, status := "", 0
	for _, failure := range failures {
		next, nextStatus := credentialCleanupFailureCategory(failure, unknown)
		if next == unknown {
			return unknown, nextStatus
		}
		if category == "" {
			category, status = next, nextStatus
		}
	}
	if category == "" {
		return unknown, http.StatusBadGateway
	}
	return category, status
}

func credentialCleanupFailureCategory(err error, unknown string) (string, int) {
	if errors.Is(err, errExecutionOutcomeUnknown) {
		return unknown, http.StatusBadGateway
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.HasPrefix(message, "target offline"):
		return "target_offline", http.StatusServiceUnavailable
	case strings.HasPrefix(message, "target busy"):
		return "target_busy", http.StatusConflict
	case strings.HasPrefix(message, "target queue full"):
		return "target_queue_full", http.StatusConflict
	case strings.HasPrefix(message, "target queue timeout"):
		return "target_queue_timeout", http.StatusConflict
	}
	return unknown, http.StatusBadGateway
}
