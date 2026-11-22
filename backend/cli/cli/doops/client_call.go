package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	executionCancellationTimeout = 30 * time.Second
)

var shellProgressHeartbeatPattern = regexp.MustCompile(`^\r\x1b\[K\[agent\] ⏳ 命令后台执行中\.\.\. \(耗时 [1-9][0-9]*s\)$`)

type gatewayRPCError struct {
	Code    float64
	Message string
	Data    map[string]interface{}
}

func (e *gatewayRPCError) Error() string {
	message := fmt.Sprintf("remote error: %s", e.Message)
	report, ok := e.Data["structuredContent"].(map[string]interface{})
	if !ok || report["kind"] != "doops.verified-mutation" {
		return message
	}
	// This report is public Gateway evidence; other RPC data may contain secrets.
	encoded, err := json.Marshal(report)
	if err != nil {
		return fmt.Sprintf("%s\nencode doops.verified-mutation receipt: %v", message, err)
	}
	return message + "\n" + string(encoded)
}

func executionOutcomeUnknownError() error {
	return &gatewayRPCError{Code: -32008, Message: "execution_outcome_unknown"}
}

// Call 在持久连接上执行 MCP tool call，流式输出到 stdout。
func (c *MCPClient) Call(toolName string, arguments map[string]interface{}) error {
	// 确保连接已建立
	if err := c.connect(); err != nil {
		return err
	}

	// 注入 session_id
	if c.SessionName != "" {
		arguments["session_id"] = c.SessionName
	}

	// 分配请求 ID 并注册接收 channel
	reqID := c.nextReqID()
	conn, ch := c.registerCall(reqID)
	defer c.unregisterPending(reqID)

	// 发送 tool call
	c.log("CLIENT:SEND_TOOL_CALL", fmt.Sprintf("Invoking tool %s (reqID=%d)", toolName, reqID))
	// WebSocket 发送
	deadline := time.Now().Add(c.callTimeout())
	err := c.writeRequest(conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      reqID,
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": arguments,
		},
	}, deadline)
	if err != nil {
		return fmt.Errorf("tool call failed: %w", err)
	}

	// 监听事件：流式输出 + 最终结果
	streamedAny := false
	streamedAnswer := false // assistant text already streamed
	endsWithNewline := true
	timeout := time.NewTimer(time.Until(deadline))
	defer timeout.Stop()
	timedOut := false
	for {
		select {
		case evt := <-ch:
			if evt.IsError {
				if timedOut {
					return executionOutcomeUnknownError()
				}
				return fmt.Errorf("connection lost: %s", evt.Raw)
			}

			msg := evt.Parsed

			// Notification data is output, not a content-sniffed event protocol.
			if method, ok := msg["method"].(string); ok && method == "notifications/message" {
				if params, ok := msg["params"].(map[string]interface{}); ok {
					if chunk, ok := params["data"].(string); ok && chunk != "" {
						kind, _ := params["kind"].(string)
						if kind == "tool" {
							// Tool markers are progress, not the answer; keep them on their own line.
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

			// Handle final result
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
						contentList, _ := result["content"].([]interface{})
						for _, item := range contentList {
							if m, ok := item.(map[string]interface{}); ok && m["type"] == "text" {
								if text, ok := m["text"].(string); ok && text != "" {
									errMsg = text
									break
								}
							}
						}
						return fmt.Errorf("%s", errMsg)
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
		case <-timeout.C:
			if timedOut {
				return executionOutcomeUnknownError()
			}
			timedOut = true
			cancelDeadline := time.Now().Add(executionCancellationTimeout)
			if err := c.requestCancellation(conn, reqID, cancelDeadline); err != nil {
				return executionOutcomeUnknownError()
			}
			timeout.Reset(time.Until(cancelDeadline))
		}
	}
}

// CallAndCapture 在持久连接上执行 MCP tool call，捕获结果文本而非打印。
func (c *MCPClient) CallAndCapture(toolName string, arguments map[string]interface{}) (string, error) {
	text, _, err := c.callAndCaptureResult(toolName, arguments)
	return text, err
}

func (c *MCPClient) callAndCaptureResult(toolName string, arguments map[string]interface{}) (output string, result map[string]interface{}, err error) {
	defer func() {
		if c.UpgradeOperationID == "" {
			return
		}
		output = redactUpgradeText(output, c.Token)
		result, _ = redactUpgradeValue(result, c.Token).(map[string]interface{})
		var rpcErr *gatewayRPCError
		if errors.As(err, &rpcErr) {
			data, _ := redactUpgradeValue(rpcErr.Data, c.Token).(map[string]interface{})
			err = &gatewayRPCError{Code: rpcErr.Code, Message: redactUpgradeText(rpcErr.Message, c.Token), Data: data}
		} else if err != nil {
			err = errors.New(redactUpgradeText(err.Error(), c.Token))
		}
	}()
	// 确保连接已建立
	if err := c.connect(); err != nil {
		return "", nil, err
	}

	// 注入 session_id
	if c.UpgradeOperationID != "" {
		arguments["session_id"] = c.SessionName
		arguments["upgrade_operation_id"] = c.UpgradeOperationID
	} else if sid := c.SessionStore.Get(c.Target.Name, c.SessionName); sid != "" {
		arguments["session_id"] = sid
	} else {
		arguments["session_id"] = c.SessionName
	}

	// 分配请求 ID 并注册接收 channel
	reqID := c.nextReqID()
	conn, ch := c.registerCall(reqID)
	defer c.unregisterPending(reqID)

	// 发送 tool call
	deadline := time.Now().Add(c.callTimeout())
	err = c.writeRequest(conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      reqID,
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": arguments,
		},
	}, deadline)
	if err != nil {
		return "", nil, fmt.Errorf("tool call failed: %w", err)
	}

	var capturedOutput strings.Builder

	// 等待最终结果
	timeout := time.NewTimer(time.Until(deadline))
	defer timeout.Stop()
	timedOut := false
	for {
		select {
		case evt := <-ch:
			if evt.IsError {
				if timedOut {
					return capturedOutput.String(), nil, executionOutcomeUnknownError()
				}
				return capturedOutput.String(), nil, fmt.Errorf("connection lost: %s", evt.Raw)
			}

			msg := evt.Parsed

			// 累加流式输出
			if method, ok := msg["method"].(string); ok && method == "notifications/message" {
				if params, ok := msg["params"].(map[string]interface{}); ok {
					if chunk, ok := params["data"].(string); ok {
						// Shell output notifications include a trailing newline. The
						// Agent's standalone display heartbeat does not; exclude only
						// that complete control message before joining output chunks.
						if toolName == "doops_shell" && params["kind"] == "raw" && shellProgressHeartbeatPattern.MatchString(chunk) {
							continue
						}
						capturedOutput.WriteString(chunk)
					}
				}
				continue
			}

			if _, ok := msg["method"]; ok {
				continue
			}

			if id, ok := msg["id"].(float64); ok && int64(id) == reqID {
				if result, ok := msg["result"].(map[string]interface{}); ok {
					if text := toolResultText(result, ""); text != "" {
						capturedOutput.WriteString(text)
					}
					if toolResultIsError(result) {
						return capturedOutput.String(), result, fmt.Errorf("%s", toolResultText(result, "tool returned an error"))
					}
					if capturedOutput.Len() > 0 {
						return capturedOutput.String(), result, nil
					}
				} else if rpcErr, ok := msg["error"].(map[string]interface{}); ok {
					code, _ := rpcErr["code"].(float64)
					data, _ := rpcErr["data"].(map[string]interface{})
					rpcError := &gatewayRPCError{Code: code, Message: fmt.Sprint(rpcErr["message"]), Data: data}
					var result map[string]interface{}
					if code == -32603 && rpcError.Message == "audit_completion_failed" {
						result, _ = data["result"].(map[string]interface{})
						capturedOutput.WriteString(toolResultText(result, ""))
					}
					return capturedOutput.String(), result, rpcError
				}

				return capturedOutput.String(), nil, fmt.Errorf("no result received")
			}
		case <-timeout.C:
			if timedOut {
				return capturedOutput.String(), nil, executionOutcomeUnknownError()
			}
			timedOut = true
			cancelDeadline := time.Now().Add(executionCancellationTimeout)
			if err := c.requestCancellation(conn, reqID, cancelDeadline); err != nil {
				return capturedOutput.String(), nil, executionOutcomeUnknownError()
			}
			timeout.Reset(time.Until(cancelDeadline))
		}
	}
}

// CallStructured invokes an MCP tool whose contract returns structuredContent.
// Human-readable tool text and progress notifications are intentionally ignored.
func (c *MCPClient) CallStructured(toolName string, arguments map[string]interface{}, destination interface{}) error {
	if err := c.connect(); err != nil {
		return err
	}
	if sid := c.SessionStore.Get(c.Target.Name, c.SessionName); sid != "" {
		arguments["session_id"] = sid
	} else {
		arguments["session_id"] = c.SessionName
	}

	reqID := c.nextReqID()
	conn, ch := c.registerCall(reqID)
	defer c.unregisterPending(reqID)

	deadline := time.Now().Add(c.callTimeout())
	err := c.writeRequest(conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      reqID,
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": arguments,
		},
	}, deadline)
	if err != nil {
		return fmt.Errorf("tool call failed: %w", err)
	}

	timeout := time.NewTimer(time.Until(deadline))
	defer timeout.Stop()
	for {
		select {
		case evt := <-ch:
			if evt.IsError {
				return fmt.Errorf("connection lost: %s", evt.Raw)
			}
			msg := evt.Parsed
			if _, notification := msg["method"]; notification {
				continue
			}
			id, ok := msg["id"].(float64)
			if !ok || int64(id) != reqID {
				continue
			}
			if result, ok := msg["result"].(map[string]interface{}); ok {
				if toolResultIsError(result) {
					return fmt.Errorf("%s", toolResultText(result, "tool returned an error"))
				}
				content, found := result["structuredContent"]
				if !found {
					return fmt.Errorf("tool %s returned no structuredContent", toolName)
				}
				data, err := json.Marshal(content)
				if err != nil {
					return fmt.Errorf("encode structured tool result: %w", err)
				}
				if err := json.Unmarshal(data, destination); err != nil {
					return fmt.Errorf("decode structured tool result: %w", err)
				}
				return nil
			}
			if rpcErr, ok := msg["error"].(map[string]interface{}); ok {
				code, _ := rpcErr["code"].(float64)
				data, _ := rpcErr["data"].(map[string]interface{})
				return &gatewayRPCError{Code: code, Message: fmt.Sprint(rpcErr["message"]), Data: data}
			}
			return fmt.Errorf("tool %s returned no result", toolName)
		case <-timeout.C:
			return fmt.Errorf("tool call timed out after %s", c.callTimeout())
		}
	}
}

func (c *MCPClient) callTimeout() time.Duration {
	if c.CallTimeout > 0 {
		return c.CallTimeout
	}
	return defaultToolCallTimeout()
}

func defaultToolCallTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("DOOPS_CLI_CALL_TIMEOUT"))
	if raw == "" {
		return 60 * time.Minute
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return 60 * time.Minute
}

func toolResultIsError(result map[string]interface{}) bool {
	isErr, ok := result["isError"]
	return ok && fmt.Sprintf("%v", isErr) == "true"
}

func toolResultText(result map[string]interface{}, fallback string) string {
	contentList, _ := result["content"].([]interface{})
	for _, item := range contentList {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "text" {
			if text, ok := m["text"].(string); ok && text != "" {
				return text
			}
		}
	}
	return fallback
}

// ExecBg is declared in detached.go: exec --bg submits doops_bg with
// detach:true and the Gateway hosts the task to its terminal state.

// printFinalResult reports whether the final tool result text must be printed.
// ask streams its answer as assistant deltas; printing the final result again
// duplicated the whole answer. Other tools print the result only when nothing
// was streamed, except bg, whose result is a summary of its own.
func printFinalResult(toolName string, streamedAny, streamedAnswer bool) bool {
	switch toolName {
	case "doops_agent_prompt":
		return !streamedAnswer
	case "doops_bg":
		return true
	default:
		return !streamedAny
	}
}
