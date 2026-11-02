package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
)

func (s *GatewayStore) loadCOSSecret(actorID, name, cluster, instance string) (cosSecret, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(actorID) == "" {
		return cosSecret{}, cosFail(cosErrCredentialMissing)
	}
	authorized, err := s.AuthorizeCredentialUse(CredentialUseRequest{
		ActorID:    actorID,
		Credential: name,
		Scope:      CredentialGrantScope{Cluster: cluster, Instance: instance},
	})
	if err != nil {
		if errors.Is(err, ErrCredentialGrantDenied) || errors.Is(err, ErrCredentialForbidden) || errors.Is(err, ErrCredentialAmbiguous) {
			return cosSecret{}, cosFail(cosErrCredentialMissing)
		}
		return cosSecret{}, cosFail(cosErrCredentialMissing)
	}
	if authorized.Resource.Type != CredentialTypeOpaque {
		return cosSecret{}, cosFail(cosErrCredentialInvalid)
	}
	payload, err := s.ResolveCredentialPayload(authorized.Resource.ID, authorized.Version.ID)
	if err != nil {
		return cosSecret{}, cosFail(cosErrCredentialInvalid)
	}
	secret, err := parseCOSSecret(payload)
	for i := range payload {
		payload[i] = 0
	}
	return secret, err
}

func (h *ClientService) handleCOSToolCall(ctx context.Context, auth TokenAuth, cluster, instance string, req api.JSONRPCRequest, params api.ToolCallParams, writeJSON func(interface{}) error) error {
	if err := h.store.validateUserTokenAuth(auth); err != nil {
		return writeJSON(buildErrorResponse(req.ID, -32001, "gateway_auth_failed"))
	}
	if !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
		return writeJSON(buildErrorResponse(req.ID, -32003, "forbidden"))
	}
	var args struct {
		SessionID  string `json:"session_id"`
		Credential string `json:"credential"`
		Operation  string `json:"operation"`
		Kind       string `json:"kind"`
		Path       string `json:"path"`
		Size       int64  `json:"size"`
		ContentB64 string `json:"content_b64"`
	}
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return writeJSON(buildErrorResponse(req.ID, -32602, cosErrCredentialInvalid))
	}
	body, err := decodeCOSBody(args.ContentB64)
	if err != nil {
		return writeJSON(buildErrorResponse(req.ID, -32602, formatCOSError(err)))
	}
	summary := "doops_cos operation=" + strings.TrimSpace(args.Operation)
	if path := strings.TrimSpace(args.Path); path != "" {
		summary += " path=" + path
	}
	auditID, auditErr := h.store.StartAudit(AuditEvent{
		UserID:         auth.UserID,
		TokenID:        auth.TokenID,
		Cluster:        cluster,
		Instance:       instance,
		Action:         cosAuditAction(args.Operation),
		Session:        strings.TrimSpace(args.SessionID),
		CommandSummary: summary,
		StartedAt:      time.Now().UTC(),
	})
	if auditErr != nil {
		return writeJSON(buildErrorResponse(req.ID, -32603, "audit_unavailable"))
	}
	service := cosService{
		load: h.store.loadCOSSecret,
		open: openCOSBucket,
		sync: func(ctx context.Context, cluster, instance, session string, files []cosSyncFile) (string, error) {
			if h.registry.Get(cluster, instance) == nil {
				return "", cosFail(cosErrWorkspaceNotSync)
			}
			_, err := h.RunInternalToolCall(ctx, cluster, instance, "doops_cos_sync", map[string]interface{}{
				"session_id": session,
				"files":      files,
			})
			return "synced", err
		},
	}
	result, callErr := service.Handle(ctx, cosCall{
		ActorID:    auth.UserID,
		Cluster:    cluster,
		Instance:   instance,
		SessionID:  args.SessionID,
		Credential: args.Credential,
		Operation:  args.Operation,
		Kind:       args.Kind,
		Path:       args.Path,
		Size:       args.Size,
		Body:       body,
	})
	status, errMsg := "success", ""
	if callErr != nil {
		status = "error"
		errMsg = formatCOSError(callErr)
	}
	if err := h.store.FinishAudit(auditID, AuditFinish{
		Status:  status,
		Error:   errMsg,
		EndedAt: time.Now().UTC(),
	}); err != nil {
		return writeJSON(buildErrorResponse(req.ID, -32603, "audit_unavailable"))
	}
	if callErr != nil {
		return writeJSON(buildErrorResponse(req.ID, -32603, errMsg))
	}
	fields, err := cosResultFields(result)
	if err != nil {
		return writeJSON(buildErrorResponse(req.ID, -32603, cosErrPutFailed))
	}
	text := "cos " + strings.TrimSpace(args.Operation)
	if result.Key != "" {
		text += " " + result.Key
	}
	if result.WorkspaceStatus != "" {
		text += " workspace=" + result.WorkspaceStatus
	}
	return writeJSON(buildStructuredSuccessResponse(req.ID, text, fields))
}

func cosAuditAction(operation string) GatewayAction {
	switch strings.TrimSpace(operation) {
	case "presign_get", "get", "probe":
		return ActionRead
	case "sync":
		return ActionPush
	default:
		return ActionWrite
	}
}

func cosToolName(name string) bool {
	return name == "doops_cos"
}
