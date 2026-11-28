package server

// Agent-side half of 断线续接 (INT-72).
//
// The Agent owns the execution, not the tunnel socket. A tools/call that carries
// an execution_id runs on a context detached from the connection
// (context.WithoutCancel) and bounded by the Agent's own tool limit
// (maxToolExecutionDuration, default 30m). A tunnel drop therefore ends the
// transport only: the command keeps running and its terminal reply is stored in
// agentExecutionStore. When the Gateway reconnects and re-sends the same
// execution_id, this side attaches to the stored execution and replays its
// terminal state; it never starts the command a second time.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/user/doops/agent/api"
)

const (
	// agentExecutionTerminalMissing is the reply when a detached execution
	// returned without writing any terminal response.
	agentExecutionTerminalMissing = "agent_execution_terminal_missing"
)

// agentBootUnknown is what the Gateway records when the owning Agent did not
// report a boot id (an Agent built before INT-82b). It is deliberately not an
// empty string: the record then says "the owner is not identifiable", which is a
// fact, instead of looking like a record that simply has not been bound yet.
const agentBootUnknown = "unknown"

// agentBootID identifies this Agent process for the whole of its lifetime. It is
// regenerated on every start, so the Gateway can tell a reattach to the process
// that owns an execution from a redelivery to a process that never ran it.
var agentBootID = newAgentBootID()

// newAgentBootID returns 128 random bits as hex. The fallback only fires when the
// system entropy source fails; it still changes per process, which is all the
// Gateway needs to refuse a redelivery after a restart.
func newAgentBootID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("%016x%016x", uint64(time.Now().UnixNano()), uint64(os.Getpid()))
}

// validAgentBootID accepts what applyInitializeResult will trust: lowercase hex,
// bounded length. Anything else is treated as "not reported".
func validAgentBootID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 16 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// agentExecution is one execution the Agent keeps addressable by execution ID.
type agentExecution struct {
	id        string
	tool      string
	createdAt time.Time

	done chan struct{}

	mu         sync.Mutex
	terminal   []byte
	finished   bool
	finishedAt time.Time
	// cancel stops the tool run this entry owns. It is nil for an entry that
	// was only attached to (the Gateway re-sent an ID after a reconnect): an
	// attaching connection must not be able to cancel someone else's run.
	cancel context.CancelFunc
	// cancelRequested records that the cancellation came from the Wire, so the
	// terminal reply can be labelled instead of looking like an internal error.
	cancelRequested bool
}

// setCancel installs the cancel func for the run that owns this entry.
func (e *agentExecution) setCancel(cancel context.CancelFunc) {
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()
}

// requestCancel cancels the owned run once and reports whether this entry was
// the owner (a non-owner has no cancel func and cannot stop the run).
func (e *agentExecution) requestCancel() (owned bool, finished bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished {
		return true, true
	}
	if e.cancel == nil {
		return false, false
	}
	e.cancelRequested = true
	e.cancel()
	return true, false
}

func (e *agentExecution) cancellationRequested() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cancelRequested
}

// cancelExecutionByID is the Agent-side entry for POST
// /v1/executions/<id>/cancel arriving over the tunnel: the Gateway asks, this
// process stops the tool. finished=true means the answer is already stored and
// there is nothing to stop (the cancel is still idempotent from the Gateway's
// point of view).
func (r *agentExecutionRegistry) cancelExecutionByID(id string) (found bool, finished bool) {
	r.mu.Lock()
	entry := r.entries[id]
	r.mu.Unlock()
	if entry == nil {
		return false, false
	}
	_, finished = entry.requestCancel()
	return true, finished
}

func (e *agentExecution) finish(raw []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished {
		return
	}
	e.finished = true
	e.terminal = raw
	e.finishedAt = time.Now()
	close(e.done)
}

// finishIfMissing stores a definite terminal reply so an attached caller can
// never wait forever on an execution that ended without one.
func (e *agentExecution) finishIfMissing(raw []byte) {
	e.mu.Lock()
	finished := e.finished
	e.mu.Unlock()
	if finished {
		return
	}
	e.finish(raw)
}

func (e *agentExecution) snapshot() ([]byte, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.finished {
		return nil, false
	}
	return e.terminal, true
}

func (e *agentExecution) isFinished() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.finished
}

func (e *agentExecution) finishedTime() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.finishedAt
}

// agentExecutionRegistry keeps every execution this Agent process owns.
// Entries are swept lazily: a finished entry stays readable for the TTL so a
// reconnect that lands after the terminal state still gets the result.
type agentExecutionRegistry struct {
	mu      sync.Mutex
	entries map[string]*agentExecution
	ttl     time.Duration
}

func newAgentExecutionRegistry(ttl time.Duration) *agentExecutionRegistry {
	if ttl <= 0 {
		ttl = agentExecutionTTL()
	}
	return &agentExecutionRegistry{entries: make(map[string]*agentExecution), ttl: ttl}
}

// claim returns the execution for id and whether this call owns it. A caller
// that does not own it must attach instead of running the tool.
func (r *agentExecutionRegistry) claim(id, tool string) (*agentExecution, bool) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, entry := range r.entries {
		if entry.isFinished() && now.Sub(entry.finishedTime()) > r.ttl {
			delete(r.entries, key)
		}
	}
	if existing := r.entries[id]; existing != nil {
		return existing, false
	}
	entry := &agentExecution{id: id, tool: tool, createdAt: now, done: make(chan struct{})}
	r.entries[id] = entry
	return entry, true
}

// agentExecutionStore is process-wide: one Agent process serves one Gateway
// target, so execution IDs only need to be unique within the process.
// The TTL is resolved from the timeout table at package initialisation. The table
// itself is validated in mustLoadTimeouts, so a typo stops the process instead of
// silently pinning a default here; tests replace this value explicitly.
var agentExecutionStore = newAgentExecutionRegistry(agentExecutionTTL())

// resumeAgentToolCall runs a tools/call detached from connectionCtx, or attaches
// to the execution that already exists under this ID.
func (gw *Gateway) resumeAgentToolCall(connectionCtx context.Context, executionID string, reqID interface{}, params api.ToolCallParams, run func(context.Context, func(interface{})), write func(interface{})) {
	entry, owner := agentExecutionStore.claim(executionID, params.Name)
	if !owner {
		// The Gateway re-sent this execution after a reconnect (or a replay).
		// Attach to the stored state and reply with its terminal.
		go replayAgentExecution(connectionCtx, entry, reqID, write)
		return
	}
	execCtx, cancel := context.WithTimeout(context.WithoutCancel(connectionCtx), maxToolExecutionDuration())
	entry.setCancel(cancel)
	go func() {
		defer cancel()
		capture := func(v interface{}) {
			if raw, err := json.Marshal(v); err == nil && terminalRPCResponse(raw) {
				entry.finish(raw)
			}
			write(v)
		}
		run(execCtx, capture)
		fallback, _ := json.Marshal(buildErrorResponse(reqID, -32603, agentExecutionTerminalMissing))
		entry.finishIfMissing(fallback)
	}()
}

// replayAgentExecution waits for the stored terminal state and writes it with
// the caller's request id. The wait is bounded by the connection lifetime and by
// the Agent's tool limit, so a dropped tunnel does not leak the goroutine.
func replayAgentExecution(connectionCtx context.Context, entry *agentExecution, reqID interface{}, write func(interface{})) {
	select {
	case <-entry.done:
	case <-connectionCtx.Done():
		return
	case <-time.After(maxToolExecutionDuration()):
		write(buildErrorResponse(reqID, -32009, "execution_result_unavailable: the Agent execution did not reach a terminal state"))
		return
	}
	raw, ok := entry.snapshot()
	if !ok {
		write(buildErrorResponse(reqID, -32009, "execution_result_unavailable: the Agent execution did not reach a terminal state"))
		return
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope) == 0 {
		write(buildErrorResponse(reqID, -32009, "execution_result_unavailable: the stored terminal state is unreadable"))
		return
	}
	envelope["id"] = reqID
	write(envelope)
}

// terminalRPCResponse reports whether encoded is a JSON-RPC response (it has an
// id and a result or an error) rather than progress or a notification.
func terminalRPCResponse(encoded []byte) bool {
	var envelope map[string]interface{}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return false
	}
	if _, ok := envelope["id"]; !ok {
		return false
	}
	if method, ok := envelope["method"].(string); ok && method != "" {
		return false
	}
	if _, ok := envelope["result"]; ok {
		return true
	}
	if rpcErr, ok := envelope["error"]; ok && rpcErr != nil {
		return true
	}
	return false
}
