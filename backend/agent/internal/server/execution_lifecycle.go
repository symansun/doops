package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const executionCancellationTimeout = 30 * time.Second

var errExecutionOutcomeUnknown = errors.New("execution_outcome_unknown")

// requireExecutionLifecycle is intentionally narrower than the execution
// outcome guard. Foreground calls use terminal evidence and can run on legacy
// Agents; background calls require the Agent's lifetime/cancellation contract.
func (a *GatewayAgent) requireExecutionLifecycle(tool string) error {
	if tool == "doops_bg" && !a.ExecutionLifecycle {
		return fmt.Errorf("target Agent does not advertise protocol %q; upgrade the Agent before using background execution", executionLifecycleProtocol)
	}
	return nil
}

type executionOwnerKey struct{}

type executionOwner struct {
	client   *ClientService
	agent    *GatewayAgent
	resource string
	id       string
}

func (s *ClientService) withExecutionOwner(ctx context.Context, agent *GatewayAgent, resource, id string) context.Context {
	return context.WithValue(ctx, executionOwnerKey{}, executionOwner{s, agent, resource, id})
}

func (s *ClientService) inheritsExecutionOwner(ctx context.Context, agent *GatewayAgent, resource string) bool {
	owner, ok := ctx.Value(executionOwnerKey{}).(executionOwner)
	if !ok || owner.client != s || owner.agent != agent || owner.resource != resource || resource == "" {
		return false
	}
	s.activeOpsMu.Lock()
	defer s.activeOpsMu.Unlock()
	return s.activeOps[owner.id] != nil
}

func requiresExecutionOutcomeGuard(action GatewayAction, tool string, args json.RawMessage, resourceKey string) bool {
	if tool == "doops_agent_prompt" && extractStringArg(args, "operation") != "apply" {
		return false
	}
	return !isConcurrentReadOnlyAction(action) && !isConcurrentQuery(action, resourceKey)
}

func interruptionNeedsReconciliation(tool string, args json.RawMessage) bool {
	switch tool {
	case "doops_shell", "doops_ssh", "doops_bg", "doops_agent_upgrade",
		"doops_credential_materialize", "doops_credential_cleanup":
		return true
	case "doops_agent_prompt":
		return extractStringArg(args, "operation") == "apply"
	case "doops_k8s":
		return !gatewayK8SReadOnly(extractStringArg(args, "operation"))
	default:
		return false
	}
}

// Local workspace mutations can finish safely after cancellation, but losing
// their terminal reply is not evidence that they stopped changing the workspace.
func missingTerminalNeedsReconciliation(tool string, args json.RawMessage) bool {
	switch tool {
	case "doops_file_write", "doops_clean_workspace", "doops_git_clone",
		"doops_workspace_begin", "doops_workspace_chunk", "doops_workspace_commit":
		return true
	default:
		return interruptionNeedsReconciliation(tool, args)
	}
}

func executionResponseCanceled(response map[string]interface{}) bool {
	rpcError, _ := response["error"].(map[string]interface{})
	code, ok := numericID(rpcError["code"])
	return ok && code == -32007
}

func remoteMutationFailed(tool string, args json.RawMessage, response map[string]interface{}) bool {
	if tool != "doops_ssh" && tool != "doops_agent_prompt" && tool != "doops_k8s" &&
		tool != "doops_credential_materialize" && tool != "doops_credential_cleanup" {
		return false
	}
	if !interruptionNeedsReconciliation(tool, args) {
		return false
	}
	if _, ok := response["result"].(map[string]interface{}); ok {
		// A structured result, including an isError tool result, proves that the
		// remote tool reached a terminal state. Only a missing terminal result
		// can leave a mutation outcome unknown.
		if tool == "doops_ssh" {
			result, _ := response["result"].(map[string]interface{})
			isError, _ := result["isError"].(bool)
			if !isError {
				return false
			}
			exitCode, ok := numericID(result["sshExitCode"])
			return ok && exitCode == 255
		}
		return false
	}
	rpcError, ok := response["error"].(map[string]interface{})
	if !ok {
		return false
	}
	code, _ := numericID(rpcError["code"])
	// Invalid parameters, unknown tools and denied admission never start work.
	return code != -32602 && code != -32601 && code != -32003
}

func executionResponseUnknown(response map[string]interface{}) bool {
	rpcError, ok := response["error"].(map[string]interface{})
	if !ok {
		return false
	}
	message, _ := rpcError["message"].(string)
	return message == errExecutionOutcomeUnknown.Error() ||
		strings.HasPrefix(message, errExecutionOutcomeUnknown.Error()+":")
}

func (q *gatewayMessageQueue) observeTerminal(msg gatewayWSMessage) {
	if _, notification := msg.Parsed["method"]; notification {
		return
	}
	if result, ok := msg.Parsed["result"].(map[string]interface{}); ok {
		// A cancel acknowledgement is not the original operation's terminal reply.
		if _, acknowledged := result["canceled"]; acknowledged {
			return
		}
	} else if _, failed := msg.Parsed["error"]; !failed {
		return
	}
	q.terminalOnce.Do(func() {
		q.terminalMessage = msg
		if executionResponseUnknown(msg.Parsed) {
			q.terminalErr = errExecutionOutcomeUnknown
		}
		close(q.terminal)
	})
}

func (s *ClientService) unresolvedExecutions(cluster, instance string) ([]AuditEvent, error) {
	s.registry.mu.RLock()
	defer s.registry.mu.RUnlock()
	return s.unresolvedExecutionsLocked(cluster, instance)
}

// The registry read lock prevents a replacement connection from inheriting
// ownership of an execution admitted on the old connection.
func (s *ClientService) unresolvedExecutionsLocked(cluster, instance string) ([]AuditEvent, error) {
	events, err := s.store.UnresolvedExecutions(cluster, instance)
	if err != nil {
		return nil, err
	}
	s.activeOpsMu.Lock()
	live := make(map[int64]bool, len(s.activeOps))
	for _, op := range s.activeOps {
		if op.agent != nil && s.registry.sessions[op.agent.Key] == op.agent {
			select {
			case <-op.agent.closed:
			default:
				live[op.AuditID] = true
			}
		}
	}
	s.activeOpsMu.Unlock()
	unresolved := events[:0]
	for _, event := range events {
		if event.Status == "executing" && live[event.ID] {
			continue
		}
		if event.Status == "executing" {
			if err := s.store.MarkExecutionUncertain(event.ID); err != nil {
				if !errors.Is(err, sql.ErrNoRows) {
					return nil, fmt.Errorf("persist execution outcome unknown for audit-%d: %w", event.ID, err)
				}
				var status string
				if readErr := s.store.db.QueryRow("SELECT status FROM audit_events WHERE id = ?", event.ID).Scan(&status); readErr != nil {
					return nil, fmt.Errorf("read concurrent execution outcome for audit-%d: %w", event.ID, readErr)
				}
				if status == "executing" {
					return nil, fmt.Errorf("persist execution outcome unknown for audit-%d: %w", event.ID, err)
				}
				if status != "outcome_unknown" {
					continue
				}
			}
			event.Status = "outcome_unknown"
		}
		unresolved = append(unresolved, event)
	}
	return unresolved, nil
}

func (s *ClientService) beginExecution(agent *GatewayAgent, operationID string, auditID int64) error {
	s.registry.mu.RLock()
	defer s.registry.mu.RUnlock()
	if s.registry.sessions[agent.Key] != agent {
		return errGatewayAgentDisconnected
	}
	select {
	case <-agent.closed:
		return errGatewayAgentDisconnected
	default:
	}
	events, err := s.unresolvedExecutionsLocked(agent.Cluster, agent.Instance)
	if err != nil {
		return fmt.Errorf("execution_guard_unavailable: %w", err)
	}
	if len(events) != 0 {
		return fmt.Errorf("%w: audit-%d requires administrator reconciliation", errExecutionOutcomeUnknown, events[0].ID)
	}
	s.activeOpsMu.Lock()
	defer s.activeOpsMu.Unlock()
	op := s.activeOps[operationID]
	if op == nil || op.State == "cancel_requested" {
		return context.Canceled
	}
	if err := s.store.MarkExecutionDispatched(auditID); err != nil {
		return fmt.Errorf("execution_audit_unavailable: %w", err)
	}
	op.agent = agent
	return nil
}

func (s *ClientService) checkExecutionGuard(cluster, instance string) error {
	events, err := s.unresolvedExecutions(cluster, instance)
	if err != nil {
		return fmt.Errorf("execution_guard_unavailable: %w", err)
	}
	if len(events) != 0 {
		return fmt.Errorf("%w: audit-%d requires administrator reconciliation", errExecutionOutcomeUnknown, events[0].ID)
	}
	return nil
}

func (h *GatewayHub) handleExecutionReconciliation(w http.ResponseWriter, r *http.Request, auth TokenAuth, cluster, instance string) {
	if !h.store.UserIsPlatformAdmin(auth.UserID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	var request struct {
		ID             string `json:"id"`
		ConfirmStopped bool   `json:"confirm_stopped"`
		Reason         string `json:"reason"`
		Evidence       string `json:"evidence"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || !request.ConfirmStopped || !strings.HasPrefix(request.ID, "audit-") {
		http.Error(w, "id, confirm_stopped, reason and evidence are required for manual reconciliation", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid trailing request data", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(request.ID, "audit-"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid audit operation id", http.StatusBadRequest)
		return
	}
	for _, op := range h.listActiveOperations() {
		if op.AuditID == id {
			http.Error(w, "operation is still active; wait for terminal evidence", http.StatusConflict)
			return
		}
	}
	events, err := h.store.UnresolvedExecutions(cluster, instance)
	if err != nil {
		http.Error(w, "execution_guard_unavailable", http.StatusServiceUnavailable)
		return
	}
	found := false
	for _, event := range events {
		if event.ID == id {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "operation not found on target", http.StatusNotFound)
		return
	}
	if err := h.store.MarkExecutionUncertain(id); err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "execution_guard_unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := h.store.ResolveExecutionOutcome(id, auth.UserID, request.Reason, request.Evidence); err != nil {
		http.Error(w, sanitizeSecretText(err.Error()), http.StatusConflict)
		return
	}
	writeJSONHTTP(w, map[string]interface{}{"id": request.ID, "state": "resolved_manually"})
}
