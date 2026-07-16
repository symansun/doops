package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type CredentialMaterialization struct {
	CredentialID      string   `json:"credentialId"`
	VersionID         string   `json:"versionId"`
	ResourceName      string   `json:"resourceName,omitempty"`
	Namespace         string   `json:"namespace"`
	SecretType        string   `json:"secretType,omitempty"`
	Keys              []string `json:"keys,omitempty"`
	ResourceVersion   string   `json:"resourceVersion,omitempty"`
	Digest            string   `json:"digest,omitempty"`
	Fingerprint       string   `json:"fingerprint,omitempty"`
	ExpiresAt         string   `json:"expiresAt,omitempty"`
	Status            string   `json:"status"`
	ErrorCategory     string   `json:"errorCategory,omitempty"`
	PreviousVersionID string   `json:"previousVersionId,omitempty"`
}

type CredentialRun struct {
	APIVersion       string                      `json:"apiVersion"`
	Kind             string                      `json:"kind"`
	ID               string                      `json:"id"`
	Mode             string                      `json:"mode"`
	Cluster          string                      `json:"cluster"`
	Instance         string                      `json:"instance"`
	Template         string                      `json:"template"`
	Project          string                      `json:"project"`
	Environment      string                      `json:"environment"`
	MutationCount    int                         `json:"mutationCount"`
	Materializations []CredentialMaterialization `json:"materializations"`
	CreatedAt        time.Time                   `json:"createdAt"`
}

type credentialPrepareHTTPBody struct {
	Cluster         string `json:"cluster"`
	Instance        string `json:"instance"`
	SessionID       string `json:"session_id"`
	WorkflowPath    string `json:"workflow_path"`
	WorkspaceCommit string `json:"workspace_commit"`
	Mode            string `json:"mode"`
}

type credentialPrepareErrorResponse struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Category   string `json:"category"`
}

type preparedCredentialRun struct {
	UserID          string
	Cluster         string
	Instance        string
	SessionID       string
	WorkflowPath    string
	WorkspaceCommit string
	Mode            string
	RunDigest       string
	ExpiresAt       time.Time
}

type credentialPreparationRegistry struct {
	mu   sync.Mutex
	runs map[string]preparedCredentialRun
}

const credentialPreparationTTL = 15 * time.Minute

type credentialVerifyHTTPBody struct {
	credentialPrepareHTTPBody
	VersionID string `json:"version_id"`
}

type authorizedCredentialMaterialization struct {
	Reference  CredentialPlanReference
	Authorized AuthorizedCredentialUse
}

func (h *GatewayHub) HandleCredentialPrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeCredentialPrepareError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	auth, err := h.authenticateUser(r)
	if err != nil {
		if _, agentErr := h.store.VerifyAgentToken(bearerToken(r)); agentErr == nil {
			writeCredentialPrepareError(w, http.StatusForbidden, "forbidden")
		} else {
			writeCredentialPrepareError(w, http.StatusUnauthorized, "unauthorized")
		}
		return
	}
	var body credentialPrepareHTTPBody
	if err := decodeCredentialJSON(w, r, &body); err != nil {
		writeCredentialPrepareError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	body.Cluster = strings.TrimSpace(body.Cluster)
	body.Instance = strings.TrimSpace(body.Instance)
	body.SessionID = strings.TrimSpace(body.SessionID)
	body.WorkflowPath = strings.TrimSpace(body.WorkflowPath)
	body.WorkspaceCommit = strings.TrimSpace(body.WorkspaceCommit)
	body.Mode = strings.ToLower(strings.TrimSpace(body.Mode))
	if body.Cluster == "" || body.Instance == "" || body.SessionID == "" ||
		body.WorkflowPath == "" || !validCredentialWorkspaceCommit(body.WorkspaceCommit) ||
		(body.Mode != "dry-run" && body.Mode != "apply") {
		writeCredentialPrepareError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if !h.store.UserCan(auth.UserID, body.Cluster, body.Instance, ActionAsk) ||
		!h.store.UserCan(auth.UserID, body.Cluster, body.Instance, ActionCredentialUse) {
		writeCredentialPrepareError(w, http.StatusForbidden, "forbidden")
		return
	}
	if body.Mode == "apply" {
		h.credentialMutationMu.Lock()
		defer h.credentialMutationMu.Unlock()
	}

	auditID, _ := h.store.StartAudit(AuditEvent{
		UserID:         auth.UserID,
		TokenID:        auth.TokenID,
		Cluster:        body.Cluster,
		Instance:       body.Instance,
		Action:         ActionCredentialUse,
		Session:        body.SessionID,
		CommandSummary: "credential prepare " + body.WorkflowPath + " mode=" + body.Mode,
		StartedAt:      time.Now().UTC(),
	})
	finishAudit := func(status, category string) {
		_ = h.store.FinishAudit(auditID, AuditFinish{
			Status:  status,
			Error:   category,
			EndedAt: time.Now().UTC(),
		})
	}

	plan, err := h.loadCredentialPlan(r.Context(), body)
	if err != nil {
		category := err.Error()
		finishAudit("error", category)
		writeCredentialPrepareError(w, credentialPlanHTTPStatus(err), category)
		return
	}
	plan.CredentialRefs, err = h.resolveCredentialPlanReferences(auth.UserID, plan)
	if err != nil {
		category := credentialErrorCategory(err)
		finishAudit("error", category)
		writeCredentialPrepareError(w, credentialHTTPStatus(err), category)
		return
	}

	authorized := make([]authorizedCredentialMaterialization, 0, len(plan.CredentialRefs))
	for _, ref := range plan.CredentialRefs {
		resolved, err := h.store.AuthorizeCredentialUse(CredentialUseRequest{
			ActorID:      auth.UserID,
			CredentialID: ref.CredentialID,
			Credential:   ref.Name,
			Cluster:      plan.Cluster,
			Instance:     plan.Instance,
			Project:      plan.Project,
			Environment:  plan.Environment,
			Template:     plan.Template,
			Namespace:    ref.Namespace,
			Use:          ref.Use,
		})
		if err != nil {
			category := credentialErrorCategory(err)
			finishAudit("forbidden", category)
			writeCredentialPrepareError(w, credentialHTTPStatus(err), category)
			return
		}
		authorized = append(authorized, authorizedCredentialMaterialization{Reference: ref, Authorized: resolved})
	}

	run := CredentialRun{
		APIVersion:       "doops.sh/v2",
		Kind:             "CredentialRun",
		ID:               "credrun_" + randomHex(12),
		Mode:             body.Mode,
		Cluster:          plan.Cluster,
		Instance:         plan.Instance,
		Template:         plan.Template,
		Project:          plan.Project,
		Environment:      plan.Environment,
		Materializations: make([]CredentialMaterialization, 0, len(authorized)),
		CreatedAt:        time.Now().UTC(),
	}
	completed := make([]completedCredentialMaterialization, 0, len(authorized))
	for _, item := range authorized {
		materialization := CredentialMaterialization{
			CredentialID: item.Authorized.Resource.ID,
			VersionID:    item.Authorized.Version.ID,
			Namespace:    item.Reference.Namespace,
			Status:       "planned",
		}
		if body.Mode == "apply" {
			var request CredentialMaterializeRequest
			materialization, request, err = h.materializeCredentialVersion(
				r.Context(), body.Cluster, body.Instance, body.SessionID,
				item.Authorized.Resource, item.Authorized.Version, item.Authorized.Grant.ID, item.Reference,
			)
			if err != nil {
				category := "credential_materialization_failed"
				if rollbackErr := h.rollbackCredentialPrepare(r.Context(), completed); rollbackErr != nil {
					category = "credential_materialization_outcome_unknown"
				}
				finishAudit("error", category)
				writeCredentialPrepareError(w, http.StatusBadGateway, category)
				return
			}
			if err := h.store.RecordCredentialVerification(CredentialVerification{
				CredentialID: item.Authorized.Resource.ID,
				VersionID:    item.Authorized.Version.ID,
				GrantID:      item.Authorized.Grant.ID,
				Use:          item.Reference.Use,
				Request:      request,
				Evidence:     materialization,
			}); err != nil {
				completed = append(completed, completedCredentialMaterialization{
					Materialization: materialization,
					Item:            item,
					SessionID:       body.SessionID,
				})
				category := "credential_verification_persist_failed"
				if rollbackErr := h.rollbackCredentialPrepare(r.Context(), completed); rollbackErr != nil {
					category = "credential_materialization_outcome_unknown"
				}
				finishAudit("error", category)
				writeCredentialPrepareError(w, http.StatusInternalServerError, category)
				return
			}
			completed = append(completed, completedCredentialMaterialization{
				Materialization: materialization,
				Item:            item,
				SessionID:       body.SessionID,
			})
			run.MutationCount++
		}
		sort.Strings(materialization.Keys)
		materialization.PreviousVersionID = ""
		run.Materializations = append(run.Materializations, materialization)
	}
	if body.Mode == "apply" {
		if err := h.credentialRuns.record(auth.UserID, body, run); err != nil {
			finishAudit("error", "credential_prepare_binding_failed")
			writeCredentialPrepareError(w, http.StatusInternalServerError, "credential_prepare_binding_failed")
			return
		}
	}
	finishAudit("success", "")
	writeJSONHTTP(w, run)
}

type completedCredentialMaterialization struct {
	Materialization CredentialMaterialization
	Item            authorizedCredentialMaterialization
	SessionID       string
}

func (h *GatewayHub) rollbackCredentialPrepare(ctx context.Context, completed []completedCredentialMaterialization) error {
	var rollbackErr error
	for index := len(completed) - 1; index >= 0; index-- {
		entry := completed[index]
		materialization := entry.Materialization
		request := CredentialMaterializeRequest{
			SessionID:          entry.SessionID,
			Cluster:            entry.Item.Authorized.Grant.Cluster,
			Instance:           entry.Item.Authorized.Grant.Instance,
			CredentialID:       entry.Item.Authorized.Resource.ID,
			GrantID:            entry.Item.Authorized.Grant.ID,
			VersionID:          materialization.VersionID,
			CredentialType:     entry.Item.Authorized.Resource.Type,
			Use:                entry.Item.Reference.Use,
			Namespace:          entry.Item.Reference.Namespace,
			Workload:           entry.Item.Reference.Workload,
			RegistryRepository: entry.Item.Reference.RegistryRepository,
			RegistryReference:  entry.Item.Reference.RegistryReference,
			RequiredKeys:       entry.Item.Reference.RequiredKeys,
		}
		if materialization.PreviousVersionID == "" {
			if err := h.cleanupCredentialVersion(ctx, request); err != nil {
				rollbackErr = err
			}
			continue
		}
		previous, err := h.store.CredentialVersionByID(request.CredentialID, materialization.PreviousVersionID)
		if err != nil {
			rollbackErr = err
			continue
		}
		if _, _, err := h.materializeCredentialVersion(
			ctx,
			request.Cluster,
			request.Instance,
			request.SessionID,
			entry.Item.Authorized.Resource,
			previous,
			request.GrantID,
			entry.Item.Reference,
		); err != nil {
			rollbackErr = err
		}
	}
	return rollbackErr
}

func (h *GatewayHub) materializeCredentialVersion(
	ctx context.Context,
	cluster string,
	instance string,
	sessionID string,
	resource CredentialResource,
	version CredentialVersion,
	grantID string,
	ref CredentialPlanReference,
) (CredentialMaterialization, CredentialMaterializeRequest, error) {
	payload, err := h.store.ResolveCredentialPayload(resource.ID, version.ID)
	if err != nil {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, err
	}
	defer clear(payload)
	requiredKeys := append([]string(nil), ref.RequiredKeys...)
	if len(requiredKeys) == 0 {
		requiredKeys, err = credentialRequiredKeys(resource.Type, payload)
	}
	if err != nil {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, err
	}
	request := CredentialMaterializeRequest{
		SessionID:          sessionID,
		Cluster:            cluster,
		Instance:           instance,
		CredentialID:       resource.ID,
		GrantID:            grantID,
		VersionID:          version.ID,
		CredentialType:     resource.Type,
		Use:                ref.Use,
		Namespace:          ref.Namespace,
		Workload:           ref.Workload,
		RegistryRepository: ref.RegistryRepository,
		RegistryReference:  ref.RegistryReference,
		RequiredKeys:       requiredKeys,
		Payload:            json.RawMessage(payload),
	}
	toolArgs, err := credentialToolArguments(request)
	if err != nil {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, err
	}
	resultText, err := h.RunInternalToolCall(ctx, cluster, instance, "doops_credential_materialize", toolArgs)
	if err != nil {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, err
	}
	var materialization CredentialMaterialization
	if err := json.Unmarshal([]byte(strings.TrimSpace(resultText)), &materialization); err != nil {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, err
	}
	if materialization.CredentialID != resource.ID ||
		materialization.VersionID != version.ID ||
		materialization.Namespace != ref.Namespace ||
		materialization.Status != "verified" {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, ErrCredentialPayloadInvalid
	}
	request.Payload = nil
	return materialization, request, nil
}

func credentialToolArguments(request CredentialMaterializeRequest) (map[string]any, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var arguments map[string]any
	if err := json.Unmarshal(encoded, &arguments); err != nil {
		return nil, err
	}
	return arguments, nil
}

func (h *GatewayHub) handleCredentialVerify(
	w http.ResponseWriter,
	r *http.Request,
	auth TokenAuth,
	resource CredentialResource,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	auditID := h.startCredentialAudit(auth, ActionCredentialRotate, resource.ID, "credential verify")
	auditStatus := "error"
	auditCategory := "credential_verification_failed"
	defer func() {
		h.finishCredentialAudit(auditID, auditStatus, auditCategory)
	}()
	if !h.canRotateCredential(auth.UserID, resource) {
		auditStatus = "forbidden"
		auditCategory = "credential_forbidden"
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	h.credentialMutationMu.Lock()
	defer h.credentialMutationMu.Unlock()
	var body credentialVerifyHTTPBody
	if err := decodeCredentialJSON(w, r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.VersionID = strings.TrimSpace(body.VersionID)
	body.Cluster = strings.TrimSpace(body.Cluster)
	body.Instance = strings.TrimSpace(body.Instance)
	body.SessionID = strings.TrimSpace(body.SessionID)
	body.WorkflowPath = strings.TrimSpace(body.WorkflowPath)
	body.WorkspaceCommit = strings.TrimSpace(body.WorkspaceCommit)
	if body.VersionID == "" || body.Cluster == "" || body.Instance == "" || body.SessionID == "" ||
		body.WorkflowPath == "" || !validCredentialWorkspaceCommit(body.WorkspaceCommit) {
		http.Error(w, "invalid credential verify request", http.StatusBadRequest)
		return
	}
	if !h.store.UserCan(auth.UserID, body.Cluster, body.Instance, ActionAsk) ||
		!h.store.UserCan(auth.UserID, body.Cluster, body.Instance, ActionCredentialUse) {
		auditStatus = "forbidden"
		auditCategory = "credential_target_forbidden"
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	version, err := h.store.CredentialVersionByID(resource.ID, body.VersionID)
	if err != nil {
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	if version.State != CredentialVersionStaged {
		http.Error(w, "credential_version_not_staged", http.StatusConflict)
		return
	}
	plan, err := h.loadCredentialPlan(r.Context(), body.credentialPrepareHTTPBody)
	if err != nil {
		http.Error(w, err.Error(), credentialPlanHTTPStatus(err))
		return
	}
	plan.CredentialRefs, err = h.resolveCredentialPlanReferences(auth.UserID, plan)
	if err != nil {
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	var reference *CredentialPlanReference
	for index := range plan.CredentialRefs {
		if plan.CredentialRefs[index].CredentialID == resource.ID ||
			(plan.CredentialRefs[index].CredentialID == "" && plan.CredentialRefs[index].Name == resource.Name) {
			if reference != nil {
				http.Error(w, "credential_reference_ambiguous", http.StatusConflict)
				return
			}
			reference = &plan.CredentialRefs[index]
		}
	}
	if reference == nil {
		http.Error(w, "credential_reference_missing", http.StatusConflict)
		return
	}
	grants, err := h.store.CredentialGrantsForContext(resource.ID, *reference, plan)
	if err != nil {
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	active, activeErr := h.store.ActiveCredentialVersion(resource.ID)
	if activeErr != nil && !errors.Is(activeErr, ErrCredentialVersionNotFound) {
		http.Error(w, credentialErrorCategory(activeErr), credentialHTTPStatus(activeErr))
		return
	}
	evidence := make([]CredentialMaterialization, 0, len(grants))
	for _, grant := range grants {
		observed, request, err := h.materializeCredentialVersion(
			r.Context(), body.Cluster, body.Instance, body.SessionID,
			resource, version, grant.ID, *reference,
		)
		if err != nil {
			http.Error(w, "credential_verification_failed", http.StatusBadGateway)
			return
		}
		if activeErr == nil {
			if _, _, err := h.materializeCredentialVersion(
				r.Context(), body.Cluster, body.Instance, body.SessionID,
				resource, active, grant.ID, *reference,
			); err != nil {
				http.Error(w, "credential_verification_outcome_unknown", http.StatusBadGateway)
				return
			}
		} else if err := h.cleanupCredentialVersion(r.Context(), request); err != nil {
			http.Error(w, "credential_verification_outcome_unknown", http.StatusBadGateway)
			return
		}
		if err := h.store.RecordCredentialVerification(CredentialVerification{
			CredentialID: resource.ID,
			VersionID:    version.ID,
			GrantID:      grant.ID,
			Use:          reference.Use,
			Request:      request,
			Evidence:     observed,
		}); err != nil {
			http.Error(w, "credential_verification_persist_failed", http.StatusInternalServerError)
			return
		}
		evidence = append(evidence, observed)
	}
	writeJSONHTTP(w, map[string]any{
		"credential_id": resource.ID,
		"version_id":    version.ID,
		"verified":      true,
		"evidence":      evidence,
	})
	auditStatus = "success"
	auditCategory = ""
}

func (h *GatewayHub) loadCredentialPlan(ctx context.Context, body credentialPrepareHTTPBody) (CredentialPlan, error) {
	agent := h.registry.Get(body.Cluster, body.Instance)
	if agent == nil {
		return CredentialPlan{}, errors.New("credential_target_offline")
	}
	if agent.CredentialPlanProtocol != credentialPlanProtocolV2 {
		return CredentialPlan{}, errors.New("agent_protocol_incompatible")
	}
	planText, err := h.RunInternalToolCall(ctx, body.Cluster, body.Instance, "doops_credential_plan", map[string]any{
		"session_id":       body.SessionID,
		"workflow_path":    body.WorkflowPath,
		"workspace_commit": body.WorkspaceCommit,
	})
	if err != nil {
		return CredentialPlan{}, errors.New("credential_plan_failed")
	}
	var plan CredentialPlan
	if err := json.Unmarshal([]byte(strings.TrimSpace(planText)), &plan); err != nil {
		return CredentialPlan{}, errors.New("credential_plan_invalid")
	}
	if plan.Cluster != body.Cluster || plan.Instance != body.Instance {
		return CredentialPlan{}, errors.New("credential_target_mismatch")
	}
	return plan, nil
}

func credentialPlanHTTPStatus(err error) int {
	switch err.Error() {
	case "agent_protocol_incompatible":
		return http.StatusUpgradeRequired
	case "credential_target_offline":
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func writeCredentialPrepareError(w http.ResponseWriter, status int, category string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(credentialPrepareErrorResponse{
		APIVersion: "doops.sh/v2",
		Kind:       "CredentialPrepareError",
		Category:   category,
	})
}

func newCredentialPreparationRegistry() *credentialPreparationRegistry {
	return &credentialPreparationRegistry{runs: make(map[string]preparedCredentialRun)}
}

func (r *credentialPreparationRegistry) record(userID string, body credentialPrepareHTTPBody, run CredentialRun) error {
	runDigest, err := credentialInstructionRunDigest(run.ID, run.Materializations)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeExpiredLocked(now)
	r.runs[run.ID] = preparedCredentialRun{
		UserID:          userID,
		Cluster:         body.Cluster,
		Instance:        body.Instance,
		SessionID:       body.SessionID,
		WorkflowPath:    body.WorkflowPath,
		WorkspaceCommit: body.WorkspaceCommit,
		Mode:            body.Mode,
		RunDigest:       runDigest,
		ExpiresAt:       now.Add(credentialPreparationTTL),
	}
	return nil
}

func (r *credentialPreparationRegistry) consumeApply(
	userID, cluster, instance, sessionID string,
	envelope doopsCICDInstructionEnvelope,
) error {
	runDigest, err := credentialInstructionRunDigest(
		envelope.CredentialRun.ID,
		envelope.CredentialRun.Materializations,
	)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeExpiredLocked(now)
	prepared, ok := r.runs[envelope.CredentialRun.ID]
	if !ok ||
		prepared.UserID != userID ||
		prepared.Cluster != cluster ||
		prepared.Instance != instance ||
		prepared.SessionID != sessionID ||
		prepared.WorkflowPath != envelope.WorkflowPath ||
		prepared.WorkspaceCommit != envelope.WorkspaceCommit ||
		prepared.Mode != "apply" ||
		prepared.RunDigest != runDigest {
		return errors.New("credential_prepare_binding_invalid")
	}
	delete(r.runs, envelope.CredentialRun.ID)
	return nil
}

func (r *credentialPreparationRegistry) removeExpiredLocked(now time.Time) {
	for id, prepared := range r.runs {
		if !prepared.ExpiresAt.After(now) {
			delete(r.runs, id)
		}
	}
}

func credentialInstructionRunDigest(id string, materializations []CredentialMaterialization) (string, error) {
	if strings.TrimSpace(id) == "" || materializations == nil {
		return "", errors.New("credential_prepare_binding_invalid")
	}
	return digestJSON(struct {
		ID               string                      `json:"id"`
		Materializations []CredentialMaterialization `json:"materializations"`
	}{
		ID:               id,
		Materializations: materializations,
	})
}

func (h *GatewayHub) resolveCredentialPlanReferences(actorID string, plan CredentialPlan) ([]CredentialPlanReference, error) {
	references := append([]CredentialPlanReference(nil), plan.CredentialRefs...)
	for _, bundleName := range plan.BundleRefs {
		bundle, err := h.store.ResolveCredentialBundle(actorID, bundleName)
		if err != nil {
			return nil, err
		}
		for _, item := range bundle.Items {
			resource, err := h.store.CredentialByID(item.CredentialID)
			if err != nil {
				return nil, err
			}
			references = append(references, CredentialPlanReference{
				CredentialID:       resource.ID,
				Name:               resource.Name,
				Use:                item.Use,
				Namespace:          item.Namespace,
				Workload:           item.Workload,
				RegistryRepository: item.RegistryRepository,
				RegistryReference:  item.RegistryReference,
				RequiredKeys:       append([]string(nil), item.RequiredKeys...),
			})
		}
	}
	return references, nil
}

func (h *GatewayHub) cleanupCredentialVersion(ctx context.Context, request CredentialMaterializeRequest) error {
	arguments := map[string]any{
		"session_id":    request.SessionID,
		"credential_id": request.CredentialID,
		"grant_id":      request.GrantID,
		"version_id":    request.VersionID,
		"namespace":     request.Namespace,
		"workload":      request.Workload,
	}
	_, err := h.RunInternalToolCall(ctx, request.Cluster, request.Instance, "doops_credential_cleanup", arguments)
	return err
}

func (h *GatewayHub) cleanupCredentialGrantMaterializations(
	ctx context.Context,
	credentialID string,
	grantID string,
) error {
	active, err := h.store.ActiveCredentialVersion(credentialID)
	if errors.Is(err, ErrCredentialVersionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	verifications, err := h.store.CredentialVerifications(credentialID, active.ID)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for _, verification := range verifications {
		if verification.GrantID != grantID {
			continue
		}
		key := credentialMaterializationContextKey(verification.Request)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := h.cleanupCredentialVersion(ctx, verification.Request); err != nil {
			return err
		}
	}
	return nil
}

func credentialReferenceFromRequest(request CredentialMaterializeRequest) CredentialPlanReference {
	return CredentialPlanReference{
		CredentialID:       request.CredentialID,
		Use:                request.Use,
		Namespace:          request.Namespace,
		Workload:           request.Workload,
		RegistryRepository: request.RegistryRepository,
		RegistryReference:  request.RegistryReference,
	}
}

func credentialMaterializationContextKey(request CredentialMaterializeRequest) string {
	return strings.Join([]string{
		request.Cluster,
		request.Instance,
		request.GrantID,
		request.Namespace,
		string(request.Use),
		request.Workload.Kind,
		request.Workload.Name,
		request.RegistryRepository,
		request.RegistryReference,
	}, "\x00")
}

func (h *GatewayHub) rollbackCredentialPromotion(
	ctx context.Context,
	resource CredentialResource,
	previous CredentialVersion,
	previousErr error,
	applied []CredentialVerification,
) error {
	for index := len(applied) - 1; index >= 0; index-- {
		request := applied[index].Request
		if previousErr == nil {
			if _, _, err := h.materializeCredentialVersion(
				ctx,
				request.Cluster,
				request.Instance,
				request.SessionID,
				resource,
				previous,
				request.GrantID,
				credentialReferenceFromRequest(request),
			); err != nil {
				return err
			}
			continue
		}
		if err := h.cleanupCredentialVersion(ctx, request); err != nil {
			return err
		}
	}
	return nil
}

func credentialRequiredKeys(credentialType CredentialType, payload json.RawMessage) ([]string, error) {
	if credentialType != CredentialTypeOpaque {
		return nil, nil
	}
	var value struct {
		Data map[string]string `json:"data"`
	}
	if err := decodeStrictCredentialPayload(payload, &value); err != nil || len(value.Data) == 0 {
		return nil, ErrCredentialPayloadInvalid
	}
	return mapKeys(value.Data), nil
}

func validCredentialWorkspaceCommit(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
