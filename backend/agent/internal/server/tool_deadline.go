package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
)

// agentToolTimeoutMessage prefixes the reply when the Agent's own deadline ends
// a tool. The Gateway classifies it as a timeout, never as a cancellation or an
// unknown outcome.
const agentToolTimeoutMessage = "agent_tool_timeout"

// AgentToolTimeoutCategory is the stable error category reported in error.data.
const AgentToolTimeoutCategory = "agent_tool_timeout"

// toolDeadlineMargin ends the Agent's own limit slightly before the Gateway's,
// so the Gateway receives a definite timeout reply instead of giving up the
// request while the Agent is still running the tool.
const toolDeadlineMargin = 5 * time.Second

// toolDeadlineFloor is the smallest derived limit for a very short Gateway timeout.
const toolDeadlineFloor = time.Second

// toolDeadline derives the execution deadline for one tool call from the
// Agent-side limit (ToolTimeoutEnv, default 30m) and the Gateway's advertised
// timeout. Without a Gateway timeout the Agent-side limit applies.
func toolDeadline(parent context.Context, gatewayTimeoutMS int64) (context.Context, context.CancelFunc, time.Duration) {
	base := maxToolExecutionDuration()
	effective := base
	if gatewayTimeoutMS > 0 {
		derived := time.Duration(gatewayTimeoutMS)*time.Millisecond - toolDeadlineMargin
		if derived < toolDeadlineFloor {
			derived = toolDeadlineFloor
		}
		// An explicitly configured DOOPS_TOOL_TIMEOUT is a hard cap.
		if strings.TrimSpace(os.Getenv(ToolTimeoutEnv)) != "" && derived > base {
			effective = base
		} else {
			effective = derived
		}
	}
	execCtx, cancel := context.WithTimeout(parent, effective)
	return execCtx, cancel, effective
}

// releaseToolDeadline is deferred right after toolDeadline. It releases the
// timer on every path, but only cancels the context when this call did not
// already end on its own deadline: cancelling then would replace
// context.DeadlineExceeded with context.Canceled and hide the Agent timeout.
func releaseToolDeadline(execCtx context.Context, cancel context.CancelFunc) {
	if !errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		cancel()
	}
}

// replyToolTimeout writes the timeout reply when the Agent deadline, not the
// caller, ended the tool. It reports whether it wrote the terminal response.
func replyToolTimeout(reqID interface{}, parent, execCtx context.Context, limit time.Duration, cmdErr error, writeJSON func(interface{})) bool {
	if parent.Err() != nil || !errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return false
	}
	writeJSON(api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      reqID,
		Error: &api.RPCError{
			Code: -32603,
			Message: fmt.Sprintf("%s: the Agent stopped the tool after its %s limit and terminated the process; "+
				"the command did not finish, so its remaining work did not run", agentToolTimeoutMessage, limit),
			Data: map[string]interface{}{
				"category": AgentToolTimeoutCategory,
				"limit_ms": limit.Milliseconds(),
			},
		},
	})
	if cmdErr != nil {
		log.Printf("tool deadline %s reached: %v", limit, cmdErr)
	}
	return true
}

// currentCommandExit returns the leader's exit status for audit. Residual
// same-group processes were terminated after a known exit, so the audit records
// that exit code instead of inventing a new failure.
func currentCommandExit(err error) error {
	var residual *residualProcessesTerminated
	if errors.As(err, &residual) {
		return residual.leaderErr
	}
	return err
}
