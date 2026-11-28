package server

// 断线续接 (disconnect/resume), INT-72.
//
// A tools/call may carry a client-chosen "execution_id" next to its arguments.
// That ID turns the call into an addressable execution:
//
//   - The Gateway records the execution's status (running / succeeded / failed)
//     and its terminal JSON-RPC response, and keeps the record for
//     ExecutionResumeTTL after the terminal state.
//   - When the Agent tunnel drops mid-flight, the Gateway does not fail the
//     caller. Resume waits (bounded by ExecutionResumeGrace) for the Agent to
//     reconnect and re-sends the same execution ID, which makes the Agent attach
//     to its existing execution instead of starting a second one.
//   - GET /v1/executions/<id> returns the stored terminal state, so a client
//     that lost its own connection can read the result back without
//     re-executing anything.
//
// The wire contract is documented in backend/docs/DOOPS_GATEWAY_TUNNEL.md.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/user/doops/agent/api"
)

const (
	executionResumeIDMaxLength = 128

	executionStatusRunning   = "running"
	executionStatusSucceeded = "succeeded"
	executionStatusFailed    = "failed"
	executionStatusCanceled  = "canceled"
	executionStatusExpired   = "expired"

	// executionCancelDispatchTimeout bounds how long the Gateway waits for the
	// Agent to acknowledge a cancel it just forwarded. It is short: the cancel
	// either reaches the Agent now or the client is told to retry (HTTP 409).
	executionCancelDispatchTimeout = 10 * time.Second
)

var (
	// errExecutionResultUnavailable is the bounded failure: the execution was
	// accepted, its transport died, and no Agent came back within the grace.
	errExecutionResultUnavailable = errors.New("execution_result_unavailable")
	errExecutionNotFound          = errors.New("execution_not_found")
	errExecutionExpired           = errors.New("execution_expired")
	// errExecutionAgentOffline is the retryable cancel failure: the record is
	// still known but there is no tunnel to carry the cancel right now.
	errExecutionAgentOffline = errors.New("execution_agent_offline")
	// errExecutionAgentRestarted is the definite refusal behind INT-82b: the
	// record was running under a different Agent process, and the Gateway cannot
	// tell whether that process executed the command before it died.
	errExecutionAgentRestarted = errors.New("execution_agent_restarted")
)

// executionAgentRestartedCode is a JSON-RPC code of its own: -32010 already
// means gateway_draining on this surface, and a client that retries a drain must
// not retry a restarted owner. -32011 is the next free code after the execution
// family (-32008 outcome unknown, -32009 execution state). The pair is
// registered in execution_error_code_contract_test.go, which refuses a code that
// carries two prefixes or a prefix that carries two codes.
const executionAgentRestartedCode = -32011

// agentRestartedDetail is the operator-facing half of the refusal. It has to say
// that both outcomes are possible: the Gateway refuses to guess.
const agentRestartedDetail = "the Agent process that owned this execution restarted before a terminal reply; the command may have run and may not have finished. Check its side effects, then send the work again with a new execution_id"

// executionIDFromToolCall reads the optional execution_id that a client puts
// beside the tools/call arguments. api.ToolCallParams intentionally does not
// carry the field: it is a Gateway-side resume key, not tool input.
func executionIDFromToolCall(rawParams json.RawMessage) (string, error) {
	if len(rawParams) == 0 {
		return "", nil
	}
	var envelope struct {
		ExecutionID string `json:"execution_id"`
	}
	if err := json.Unmarshal(rawParams, &envelope); err != nil {
		return "", nil
	}
	id := strings.TrimSpace(envelope.ExecutionID)
	if id == "" {
		return "", nil
	}
	if len(id) > executionResumeIDMaxLength {
		return "", fmt.Errorf("execution_id is longer than %d characters", executionResumeIDMaxLength)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':':
		default:
			return "", errors.New("execution_id may only contain letters, digits, '-', '_', '.' and ':'")
		}
	}
	return id, nil
}

// executionRecord is one addressable execution on the Gateway.
type executionRecord struct {
	id       string
	tool     string
	cluster  string
	instance string
	ownerID  string

	// agentBootID is the Agent process that owned this execution. Empty means the
	// owner has not been bound yet; agentBootUnknown means it never reported one.
	agentBootID string
	status      string
	errText     string
	terminal    json.RawMessage
	// terminalTruncated/terminalBytes survive a restart: the stored terminal is
	// a prefix of a larger payload, and the caller must be told so instead of
	// receiving half a JSON document.
	terminalTruncated bool
	terminalBytes     int64
	createdAt         time.Time
	updatedAt         time.Time
	done              chan struct{}
	finishedAt        time.Time
	// localCancel stops the Gateway-side relay that is waiting for this
	// execution's terminal state. A tunnel cancel makes the Agent stop the tool,
	// but its terminal reply goes to a socket the client already closed, so the
	// Gateway has to release its own waiter or the active operation never ends.
	localCancel context.CancelFunc
}

// executionSnapshot is an immutable copy handed to callers, so a concurrent
// terminal update cannot be observed half-written.
type executionSnapshot struct {
	id          string
	tool        string
	cluster     string
	instance    string
	ownerID     string
	agentBootID string
	status      string
	errText     string
	terminal    json.RawMessage
	// terminalTruncated means terminal is only the stored prefix of a larger
	// payload whose size is terminalBytes.
	terminalTruncated bool
	terminalBytes     int64
	createdAt         time.Time
	updatedAt         time.Time
}

func snapshotOf(record *executionRecord) executionSnapshot {
	terminal := make([]byte, len(record.terminal))
	copy(terminal, record.terminal)
	return executionSnapshot{
		id: record.id, tool: record.tool, cluster: record.cluster, instance: record.instance,
		ownerID: record.ownerID, agentBootID: record.agentBootID,
		status: record.status, errText: record.errText,
		terminal: terminal, terminalTruncated: record.terminalTruncated,
		terminalBytes: record.terminalBytes,
		createdAt:     record.createdAt, updatedAt: record.updatedAt,
	}
}

func (s executionSnapshot) view() map[string]interface{} {
	out := map[string]interface{}{
		"execution_id": s.id,
		"tool":         s.tool,
		"cluster":      s.cluster,
		"instance":     s.instance,
		"status":       s.status,
		"created_at":   s.createdAt.UTC().Format(time.RFC3339Nano),
		"updated_at":   s.updatedAt.UTC().Format(time.RFC3339Nano),
	}
	if s.errText != "" {
		out["error"] = s.errText
	}
	if s.terminalTruncated {
		// The stored bytes are a prefix, not a JSON document. Wrap them in a
		// shape the caller can always parse, so "the result was huge" is a fact
		// in the payload instead of a parse error at the far end.
		out["result"] = truncatedTerminalView(s.terminalBytes, s.terminal)
		return out
	}
	if len(s.terminal) != 0 {
		var envelope map[string]interface{}
		if err := json.Unmarshal(s.terminal, &envelope); err == nil {
			if result, ok := envelope["result"]; ok {
				out["result"] = result
			}
			if rpcErr, ok := envelope["error"]; ok {
				out["error_detail"] = rpcErr
			}
		}
	}
	return out
}

// executionRegistry keeps execution records addressable by ID.
//
// Since INT-82a the record is also written through to the Gateway store
// (execution_records), so a Gateway restart can still answer GET by execution
// ID. A record that was running when the process died is restored as a definite
// failure: the Gateway cannot tell "the Agent crashed" from "the Agent has not
// reconnected yet" (that distinction is INT-82b), and answering "failed" beats
// answering "running" forever.
type executionRegistry struct {
	mu        sync.Mutex
	records   map[string]*executionRecord
	ttl       time.Duration
	store     *GatewayStore
	lastPurge time.Time
}

func newExecutionRegistry(ttl time.Duration) *executionRegistry {
	if ttl <= 0 {
		ttl = executionResumeTTL()
	}
	return &executionRegistry{records: make(map[string]*executionRecord), ttl: ttl}
}

// persistLocked writes the record through to the store. A store failure is
// logged, never fatal: the in-memory record still serves the live Gateway, and
// losing durability must not fail a client's execution.
func (r *executionRegistry) persistLocked(record *executionRecord) {
	if r.store == nil || record == nil {
		return
	}
	if err := r.store.saveExecutionRecord(record, r.ttl); err != nil {
		log.Printf("[gateway] could not persist execution %s: %v", record.id, err)
	}
}

// purgeLocked deletes rows past their expiry at most once per minute. Lazy
// eviction, not a new scheduler: the Gateway keeps exactly one scheduler (the
// monitor engine) per AGENTS.md.
func (r *executionRegistry) purgeLocked(now time.Time) {
	if r.store == nil || now.Sub(r.lastPurge) < time.Minute {
		return
	}
	r.lastPurge = now
	if err := r.store.purgeExecutionRecords(now); err != nil {
		log.Printf("[gateway] could not purge expired execution records: %v", err)
	}
}

// restore loads persisted records into memory. It runs once, when the hub is
// built, before any request can be served.
func (r *executionRegistry) restore(store *GatewayStore, now time.Time) error {
	if store == nil {
		return nil
	}
	rows, err := store.loadExecutionRecords(now)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = store
	for _, row := range rows {
		record := &executionRecord{
			id: row.id, tool: row.tool, cluster: row.cluster, instance: row.instance,
			ownerID: row.ownerID, agentBootID: row.agentBootID,
			status: row.status, errText: row.errText,
			terminal: executionRowTerminal(row), createdAt: row.createdAt, updatedAt: row.updatedAt,
			terminalTruncated: row.terminalTruncated, terminalBytes: row.terminalBytes,
			done: make(chan struct{}),
		}
		if !row.finishedAt.IsZero() {
			record.finishedAt = row.finishedAt
		}
		if record.status == executionStatusRunning {
			record.status = executionStatusFailed
			record.errText = "execution_result_unavailable: the Gateway restarted while the execution was running"
			record.terminal = nil
			record.terminalTruncated = false
			record.terminalBytes = 0
			record.updatedAt = now
			record.finishedAt = now
			r.persistLocked(record)
		}
		if record.status != executionStatusRunning {
			close(record.done)
		}
		r.records[record.id] = record
	}
	return nil
}

// sweepLocked marks terminal records older than the TTL as expired. The record
// itself stays until twice the TTL so GET can answer execution_expired instead
// of execution_not_found.
func (r *executionRegistry) sweepLocked(now time.Time) {
	for id, record := range r.records {
		if record.status == executionStatusExpired {
			if now.Sub(record.updatedAt) > r.ttl {
				delete(r.records, id)
			}
			continue
		}
		if record.status == executionStatusRunning || record.finishedAt.IsZero() {
			continue
		}
		if now.Sub(record.finishedAt) > r.ttl {
			record.status = executionStatusExpired
			record.terminal = nil
			record.terminalTruncated = false
			record.terminalBytes = 0
			record.errText = ""
			record.updatedAt = now
			r.persistLocked(record)
		}
	}
	r.purgeLocked(now)
}

func (r *executionRegistry) get(id string) (executionSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(time.Now())
	record := r.records[id]
	if record == nil {
		return executionSnapshot{}, false
	}
	return snapshotOf(record), true
}

// begin creates the running record, or returns the existing one with
// created=false. The caller never starts a second execution for a known ID.
func (r *executionRegistry) begin(id, tool, cluster, instance, ownerID string) (*executionRecord, bool) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(now)
	if existing := r.records[id]; existing != nil {
		return existing, false
	}
	record := &executionRecord{
		id: id, tool: tool, cluster: cluster, instance: instance, ownerID: ownerID,
		status: executionStatusRunning, createdAt: now, updatedAt: now,
		done: make(chan struct{}),
	}
	r.records[id] = record
	r.persistLocked(record)
	return record, true
}

// bindAgent records which Agent process owns a running execution. It is written
// once, by the Gateway path that just resolved the Agent for this call; later
// reattachments never rewrite it, because that is exactly the fact a restart is
// detected against.
func (r *executionRegistry) bindAgent(id, bootID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.records[id]
	if record == nil || record.status != executionStatusRunning || record.agentBootID != "" {
		return
	}
	if bootID == "" {
		record.agentBootID = agentBootUnknown
	} else {
		record.agentBootID = bootID
	}
	r.persistLocked(record)
}

func (r *executionRegistry) finish(id, status, errText string, terminal json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.records[id]
	if record == nil || record.status != executionStatusRunning {
		return
	}
	record.status = status
	record.errText = errText
	record.terminal = terminal
	record.updatedAt = time.Now()
	record.finishedAt = record.updatedAt
	r.persistLocked(record)
	close(record.done)
}

// finishIfRunning guarantees that a record the Gateway accepted never stays
// "running" forever after the caller returns without a terminal state.
// setLocalCancel records how to release the Gateway-side relay waiter for an
// execution. Nil-safe so the call sites stay linear.
func (r *executionRegistry) setLocalCancel(id string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if record := r.records[id]; record != nil {
		record.localCancel = cancel
	}
}

// cancelLocal releases the Gateway-side relay waiter, if one is registered.
func (r *executionRegistry) cancelLocal(id string) {
	r.mu.Lock()
	record := r.records[id]
	r.mu.Unlock()
	if record != nil && record.localCancel != nil {
		record.localCancel()
	}
}

func (r *executionRegistry) finishIfRunning(id, status, errText string) {
	r.finish(id, status, errText, nil)
}

// wait returns the terminal record once it exists, or the still-running record
// when the bound expires. The bound is always positive.
func (r *executionRegistry) wait(ctx context.Context, id string, timeout time.Duration) (executionSnapshot, bool) {
	snapshot, ok := r.get(id)
	if !ok || snapshot.status != executionStatusRunning || timeout <= 0 {
		return snapshot, ok
	}
	r.mu.Lock()
	record := r.records[id]
	r.mu.Unlock()
	if record == nil {
		return r.get(id)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-record.done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return r.get(id)
}

// truncatedTerminalView is the parseable wrapper for a truncated terminal
// payload: truncated=true, bytes=the original size, terminal=the stored prefix.
// The wrapper is ordinary JSON, so a caller can unmarshal it even though the
// payload it stands for was cut at executionRecordTerminalLimit.
func truncatedTerminalView(bytes int64, terminal json.RawMessage) map[string]interface{} {
	view := map[string]interface{}{
		"truncated": true,
		"bytes":     bytes,
		"terminal":  nil,
	}
	if len(terminal) > 0 {
		view["terminal"] = string(terminal)
	}
	return view
}

// writeExecutionReplay writes the stored terminal response with the caller's
// request id, so a repeated execution ID returns the same result. A truncated
// terminal is replayed as the wrapper above, never as the half-written bytes.
func writeExecutionReplay(writeRaw func([]byte) error, writeJSON func(interface{}) error, id interface{}, terminal json.RawMessage, truncated bool, bytes int64) error {
	if truncated {
		raw, err := json.Marshal(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  truncatedTerminalView(bytes, terminal),
		})
		if err != nil {
			return writeJSON(buildErrorResponse(id, -32009, "execution_result_unavailable: the stored terminal state is unreadable"))
		}
		return writeRaw(raw)
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(terminal, &envelope); err != nil || len(envelope) == 0 {
		return writeJSON(buildErrorResponse(id, -32009, "execution_result_unavailable: the stored terminal state is unreadable"))
	}
	envelope["id"] = id
	raw, err := json.Marshal(envelope)
	if err != nil {
		return writeJSON(buildErrorResponse(id, -32009, "execution_result_unavailable: the stored terminal state is unreadable"))
	}
	return writeRaw(raw)
}

// ExecutionResumeCall is one relay that may need to survive a tunnel drop.
type ExecutionResumeCall struct {
	ExecutionID string
	Cluster     string
	Instance    string
	Agent       *GatewayAgent
	Params      api.ToolCallParams
	Timeout     time.Duration
	Forward     func(gatewayWSMessage) error
}

// Resume relays a tools/call and reattaches it after a transport interruption.
//
// Who calls it: the Gateway client path (ClientService.handleGatewayToolCall)
// for every tools/call that carries a client execution_id. Legacy calls without
// an execution ID keep the old fail-fast relay.
//
// Idempotency: Resume never starts an execution twice. Every retry carries the
// same execution_id, and the Agent attaches to the execution it already owns; a
// retry that lands after the terminal state replays that terminal instead of
// running anything. A client that re-sends a finished execution ID is answered
// from the Gateway record before Resume is reached.
//
// Bounded: the whole wait is bounded by ExecutionResumeGrace (default 60s,
// DOOPS_EXECUTION_RESUME_GRACE). When it expires Resume returns
// errExecutionResultUnavailable, which the caller reports as -32009 and records
// as a failed execution; it never blocks on a tunnel that will not come back.
func (h *GatewayHub) Resume(ctx context.Context, call ExecutionResumeCall) error {
	if call.Agent == nil {
		return fmt.Errorf("%w: no Agent accepted the execution", errExecutionResultUnavailable)
	}
	if call.ExecutionID == "" {
		return call.Agent.relayToolCall(ctx, call.Params, call.Timeout, call.Forward)
	}
	grace := h.client.opts.ExecutionResumeGrace
	if grace <= 0 {
		grace = executionResumeGrace()
	}
	deadline := time.Now().Add(grace)
	agent := call.Agent
	for {
		// INT-82b: a reconnected Agent that is a different process cannot attach
		// to the execution, because its claim table died with the old process.
		// Redelivering would run the command a second time, so refuse instead.
		if h.executionAgentRestarted(call.ExecutionID, agent) {
			return fmt.Errorf("%w: %s", errExecutionAgentRestarted, agentRestartedDetail)
		}
		err := agent.relayToolCallWithExecution(ctx, call.Params, call.Timeout, call.ExecutionID, call.Forward)
		if err == nil || !resumableTransportError(err) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("%w: no Agent reconnected within %s: %w", errExecutionResultUnavailable, grace, err)
		}
		waitCtx, cancel := context.WithTimeout(ctx, remaining)
		next := h.client.registry.Wait(waitCtx, call.Cluster, call.Instance, remaining)
		cancel()
		if next == nil {
			return fmt.Errorf("%w: no Agent reconnected within %s: %w", errExecutionResultUnavailable, grace, err)
		}
		agent = next
	}
}

// executionAgentRestarted decides whether a redelivery to agent would run a
// command that the Gateway cannot account for. It only ever fails a running
// record whose owner is known and provably different:
//   - a terminal record is replayed, never re-run (handled before Resume);
//   - an unbound or unknown owner (an Agent without a boot id) is left on the
//     old reattach path: the Gateway has no fact to refuse on, and inventing one
//     would break Agents that predate INT-82b;
//   - a reconnecting Agent that reports no boot id is equally unjudgeable.
//
// When it refuses it also lands the record as failed, so a later GET or a
// repeated execution ID gets the same definite answer instead of "running".
func (h *GatewayHub) executionAgentRestarted(executionID string, agent *GatewayAgent) bool {
	if executionID == "" || agent == nil || h.client == nil {
		return false
	}
	record, ok := h.client.executions.get(executionID)
	if !ok || record.status != executionStatusRunning {
		return false
	}
	if record.agentBootID == "" || record.agentBootID == agentBootUnknown {
		return false
	}
	if agent.BootID == "" || agent.BootID == record.agentBootID {
		return false
	}
	h.client.executions.finish(executionID, executionStatusFailed,
		errExecutionAgentRestarted.Error()+": "+agentRestartedDetail, nil)
	return true
}

// resumableTransportError reports whether the failure was the tunnel, not the
// execution. A canceled caller, a tool timeout, and a tool-level error are all
// terminal and must not be retried.
func resumableTransportError(err error) bool {
	return errors.Is(err, errGatewayAgentDisconnected) || errors.Is(err, errExecutionOutcomeUnknown)
}

func executionStatusFor(finalStatus string) string {
	switch finalStatus {
	case "success":
		return executionStatusSucceeded
	case "canceled":
		return executionStatusCanceled
	}
	return executionStatusFailed
}

// replayExecutionRecord answers a repeated execution ID from the stored record,
// waiting at most ExecutionResumeGrace for an execution that is still running.
func (h *ClientService) replayExecutionRecord(ctx context.Context, reqID interface{}, auth TokenAuth, record executionSnapshot, writeRaw func([]byte) error, writeJSON func(interface{}) error) error {
	if record.ownerID != auth.UserID || !h.store.UserCanUseTarget(auth.UserID, record.cluster, record.instance) {
		// Invisible, exactly like a missing background task: no existence leak.
		return writeJSON(buildErrorResponse(reqID, -32003, errExecutionNotFound.Error()))
	}
	if record.status == executionStatusRunning {
		// INT-82b: if the owner process is already known to be gone, fail the
		// record now instead of waiting out the grace for an execution that can
		// never reattach, then answer with the definite refusal below.
		if agent := h.registry.Get(record.cluster, record.instance); agent != nil && h.hub != nil {
			h.hub.executionAgentRestarted(record.id, agent)
		}
		if waited, ok := h.executions.wait(ctx, record.id, h.opts.ExecutionResumeGrace); ok {
			record = waited
		}
	}
	if record.status == executionStatusFailed && strings.HasPrefix(record.errText, errExecutionAgentRestarted.Error()) {
		return writeJSON(buildErrorResponse(reqID, executionAgentRestartedCode, record.errText))
	}
	switch record.status {
	case executionStatusRunning:
		return writeJSON(buildErrorResponse(reqID, -32009, "execution_in_progress: the execution is still running; query GET /v1/executions/"+record.id))
	case executionStatusExpired:
		return writeJSON(buildErrorResponse(reqID, -32009, errExecutionExpired.Error()+": the terminal state is no longer retained"))
	}
	if len(record.terminal) == 0 && record.errText != "" {
		// INT-100 O1: the record reached a terminal state without stored result
		// bytes because the Gateway itself ended it (a restart converged a
		// running execution to failed). GET reports record.errText as the error,
		// so replay must say the same thing instead of calling the state
		// unreadable, which implies corruption we can see through. Bytes that
		// exist but do not parse still take the unreadable path below.
		return writeJSON(buildErrorResponse(reqID, -32009, record.errText))
	}
	return writeExecutionReplay(writeRaw, writeJSON, reqID, record.terminal, record.terminalTruncated, record.terminalBytes)
}

// executionCanceledTerminal is the terminal envelope a canceled execution
// replays to a client that re-sends its ID afterwards: the same -32007 code the
// client tools/cancel path produces, so both cancel paths look identical.
func executionCanceledTerminal(id string) json.RawMessage {
	raw, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    -32007,
			"message": "execution canceled by request",
		},
	})
	if err != nil {
		return nil
	}
	return raw
}

// handleExecutionCancel serves POST /v1/executions/<id>/cancel.
//
// Who calls it: the user that started the execution, from any device, after its
// own client connection or its session is gone. Authorization is the same as
// GET: the owner plus a live grant on the target; a foreign execution is 403
// (the caller already authenticated, so there is no existence to hide).
//
// Idempotency: cancel only transitions running -> canceled. A second cancel, a
// client tools/cancel on the same call, and a cancel that arrives after the
// tool's own terminal state all return the stored terminal unchanged; the
// registry's finish() refuses to overwrite a non-running record.
//
// Bounded: the Agent dispatch has executionCancelDispatchTimeout. When there is
// no tunnel at all the answer is 409 execution_agent_offline with retryable
// true, never a silent success.
func (h *GatewayHub) handleExecutionCancel(w http.ResponseWriter, r *http.Request, auth TokenAuth, id string) {
	if id == "" || strings.Contains(id, "/") {
		writeExecutionHTTPError(w, http.StatusNotFound, errExecutionNotFound.Error())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	record, ok := h.client.executions.get(id)
	if !ok {
		writeExecutionHTTPError(w, http.StatusNotFound, errExecutionNotFound.Error())
		return
	}
	if record.ownerID != auth.UserID || !h.store.UserCanUseTarget(auth.UserID, record.cluster, record.instance) {
		// INT-100 S1: invisible, byte-for-byte identical to a missing execution.
		// A 403 here would let any authenticated caller probe whether a
		// client-chosen execution_id ever existed (GET already answers 404), so
		// "not yours" must not be distinguishable from "never existed".
		writeExecutionHTTPError(w, http.StatusNotFound, errExecutionNotFound.Error())
		return
	}
	if record.status == executionStatusExpired {
		writeExecutionHTTPError(w, http.StatusGone, errExecutionExpired.Error())
		return
	}
	if record.status != executionStatusRunning {
		// Already terminal: return what is stored, change nothing.
		writeJSONHTTP(w, map[string]interface{}{"execution": record.view(), "canceled": record.status == executionStatusCanceled})
		return
	}
	agent := h.registry.Get(record.cluster, record.instance)
	if agent == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprintf(w, `{"error":%q,"retryable":true,"hint":"the Agent is offline; retry while the execution is inside its grace window"}`, errExecutionAgentOffline.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), executionCancelDispatchTimeout)
	defer cancel()
	if _, err := agent.cancelExecution(ctx, id, executionCancelDispatchTimeout); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprintf(w, `{"error":%q,"retryable":true,"detail":%q}`, errExecutionAgentOffline.Error(), sanitizeSecretText(err.Error()))
		return
	}
	// The Agent acknowledged: nothing runs under this ID any more. finish() only
	// moves running -> terminal, so a racing tool terminal that landed first wins
	// and this call reports that state instead of overwriting it.
	h.client.executions.finish(id, executionStatusCanceled, "execution canceled by request", executionCanceledTerminal(id))
	// Release the relay that was waiting on the same execution: the Agent stopped
	// the tool, so that relay would otherwise wait for a terminal reply that its
	// own socket can no longer receive.
	h.client.executions.cancelLocal(id)
	record, _ = h.client.executions.get(id)
	writeJSONHTTP(w, map[string]interface{}{"execution": record.view(), "canceled": record.status == executionStatusCanceled})
}

// HandleExecution serves GET /v1/executions/<id>[?wait_ms=N].
func (h *GatewayHub) HandleExecution(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/executions/"), "/")
	if strings.HasSuffix(id, "/cancel") {
		h.handleExecutionCancel(w, r, auth, strings.TrimSuffix(id, "/cancel"))
		return
	}
	if id == "" || strings.Contains(id, "/") {
		writeExecutionHTTPError(w, http.StatusNotFound, errExecutionNotFound.Error())
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	budget := executionWaitBudget(r.URL.Query().Get("wait_ms"))
	record, ok := h.client.executions.get(id)
	if !ok {
		writeExecutionHTTPError(w, http.StatusNotFound, errExecutionNotFound.Error())
		return
	}
	// Ownership follows the same rule as background tasks: the executing user,
	// and the target grant must still be in place. Anything else is invisible.
	if record.ownerID != auth.UserID || !h.store.UserCanUseTarget(auth.UserID, record.cluster, record.instance) {
		writeExecutionHTTPError(w, http.StatusNotFound, errExecutionNotFound.Error())
		return
	}
	if record.status == executionStatusExpired {
		writeExecutionHTTPError(w, http.StatusGone, errExecutionExpired.Error())
		return
	}
	if budget > 0 && record.status == executionStatusRunning {
		if waited, ok := h.client.executions.wait(r.Context(), id, budget); ok {
			record = waited
		}
	}
	writeJSONHTTP(w, map[string]interface{}{"execution": record.view()})
}

// executionWaitBudget turns the ?wait_ms query value into the time one request
// may block. A missing, malformed or negative value means "do not block at all";
// anything longer than DOOPS_EXECUTION_RESUME_WAIT_MAX is capped there. This is
// the client's patience, not the record's lifetime: the record only stays
// readable for DOOPS_EXECUTION_RESUME_TTL and only waits DOOPS_EXECUTION_RESUME_GRACE
// for a reconnecting Agent.
func executionWaitBudget(raw string) time.Duration {
	wait, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || wait < 0 {
		return 0
	}
	budget := time.Duration(wait) * time.Millisecond
	if ceiling := executionResumeWaitBudget(); budget > ceiling {
		budget = ceiling
	}
	return budget
}

func writeExecutionHTTPError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, code)
}
