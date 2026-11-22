package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// detachedExitError means the CLI released a detached task intentionally:
// exit 130 on SIGINT/SIGTERM, exit 1 when the connection was lost after
// dispatch. The task itself keeps running on the target under the Gateway.
type detachedExitError struct {
	code int
}

func (e *detachedExitError) Error() string {
	return fmt.Sprintf("detached background task released (exit %d)", e.code)
}

// RequireGatewayProtocol fails closed when the connected Gateway does not
// advertise the named gateway-owned protocol.
func (c *MCPClient) RequireGatewayProtocol(protocol string) error {
	if err := c.connectWithVerification("", ""); err != nil {
		return err
	}
	if c.capabilities == nil {
		return fmt.Errorf("initialize verification failed: missing capabilities")
	}
	gatewayProtocols, _ := c.capabilities["gatewayProtocols"].(map[string]interface{})
	capability, present := gatewayProtocols[protocol].(map[string]interface{})
	if !present || capability["schemaVersion"] != "doops.sh/detached-background/v1" {
		return fmt.Errorf("Gateway does not support detached background tasks (%s); upgrade the Gateway", protocol)
	}
	return nil
}

// ExecBg runs doops_bg detached: the Gateway keeps the task to its true
// terminal even if this CLI disconnects. Ctrl-C releases the stream without
// sending tools/cancel.
func (c *MCPClient) ExecBg(command string, target ...string) error {
	targetName := ""
	if len(target) != 0 {
		targetName = target[0]
	}
	if err := c.RequireProtocol("executionLifecycle"); err != nil {
		return err
	}
	if err := c.RequireGatewayProtocol("detachedBackground"); err != nil {
		return err
	}
	return c.callDetached("doops_bg", map[string]interface{}{
		"command": command,
		"detach":  true,
	}, targetName)
}

func (c *MCPClient) detachedFollowUp(target string, auditID int64) {
	if auditID <= 0 {
		fmt.Fprintf(os.Stderr, "Detached before the Gateway confirmed the background task; it may not have started.\n")
		fmt.Fprintf(os.Stderr, "doops -session %s task list --target %s\n", c.SessionName, target)
		return
	}
	fmt.Fprintf(os.Stderr, "Detached; the task keeps running on %s.\n", target)
	fmt.Fprintf(os.Stderr, "doops -session %s task status --target %s --id %d\n", c.SessionName, target, auditID)
	fmt.Fprintf(os.Stderr, "doops -session %s task stop --target %s --id %d\n", c.SessionName, target, auditID)
}

// callDetached streams one detached tools/call. It never sends tools/cancel:
// releasing the terminal just ends the local stream.
func (c *MCPClient) callDetached(toolName string, arguments map[string]interface{}, target string) error {
	if err := c.connect(); err != nil {
		return err
	}
	if c.SessionName != "" {
		arguments["session_id"] = c.SessionName
	}
	reqID := c.nextReqID()
	conn, ch := c.registerCall(reqID)
	defer c.unregisterPending(reqID)

	deadline := time.Now().Add(c.callTimeout())
	if err := c.writeRequest(conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      reqID,
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": arguments,
		},
	}, deadline); err != nil {
		return fmt.Errorf("tool call failed: %w", err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	var auditID int64
	streamedAny := false
	streamedAnswer := false
	endsWithNewline := true
	for {
		select {
		case <-signals:
			// The detached task is Gateway-hosted: releasing is local only.
			c.closeConnection(conn)
			c.detachedFollowUp(target, auditID)
			return &detachedExitError{code: 130}
		case evt := <-ch:
			if evt.IsError {
				c.detachedFollowUp(target, auditID)
				return &detachedExitError{code: 1}
			}
			msg := evt.Parsed
			if method, ok := msg["method"].(string); ok && method == "notifications/doops/background_task" {
				if params, ok := msg["params"].(map[string]interface{}); ok {
					if id, ok := numericIDValue(params["audit_id"]); ok {
						auditID = id
						fmt.Fprintf(os.Stderr, "Background task: audit %d\n", auditID)
					}
				}
				continue
			}
			if method, ok := msg["method"].(string); ok && method == "notifications/message" {
				if params, ok := msg["params"].(map[string]interface{}); ok {
					if chunk, ok := params["data"].(string); ok && chunk != "" {
						kind, _ := params["kind"].(string)
						if kind == "tool" {
							if !endsWithNewline {
								fmt.Println()
							}
							fmt.Println(chunk)
							endsWithNewline = true
							continue
						}
						fmt.Print(chunk)
						endsWithNewline = strings.HasSuffix(chunk, "\n")
						streamedAny = true
						if kind == "assistant_delta" {
							streamedAnswer = true
						}
					}
				}
				continue
			}
			if id, ok := msg["id"].(float64); ok && int64(id) == reqID {
				if result, ok := msg["result"].(map[string]interface{}); ok {
					contentList, hasContent := result["content"].([]interface{})
					_, hasStructured := result["structuredContent"].(map[string]interface{})
					if !hasContent && !hasStructured && !toolResultIsError(result) {
						return executionOutcomeUnknownError()
					}
					for _, item := range contentList {
						m, ok := item.(map[string]interface{})
						text, textOK := m["text"].(string)
						if ok && m["type"] == "text" && textOK {
							if printFinalResult(toolName, streamedAny, streamedAnswer) {
								if !endsWithNewline {
									fmt.Println()
								}
								fmt.Println(text)
								endsWithNewline = true
							} else if !endsWithNewline {
								fmt.Println()
								endsWithNewline = true
							}
						}
					}
					if isErr, ok := result["isError"]; ok && fmt.Sprintf("%v", isErr) == "true" {
						errMsg := "tool returned an error (see output above)"
						for _, item := range contentList {
							if m, ok := item.(map[string]interface{}); ok && m["type"] == "text" {
								if text, ok := m["text"].(string); ok && text != "" {
									errMsg = text
									break
								}
							}
						}
						return errors.New(errMsg)
					}
				} else if rpcErr, ok := msg["error"].(map[string]interface{}); ok {
					code, _ := rpcErr["code"].(float64)
					data, _ := rpcErr["data"].(map[string]interface{})
					return &gatewayRPCError{Code: code, Message: fmt.Sprint(rpcErr["message"]), Data: data}
				} else {
					return executionOutcomeUnknownError()
				}
				return nil
			}
		}
	}
}

func numericIDValue(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case float64:
		if v != math.Trunc(v) || v < math.MinInt64 || v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case int:
		return int64(v), true
	case int64:
		return v, true
	default:
		return 0, false
	}
}
