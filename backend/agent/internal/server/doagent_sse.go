package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type doagentSSECollector func(update map[string]interface{}) error

func doagentAgentMessageText(content interface{}) (string, error) {
	switch value := content.(type) {
	case map[string]interface{}:
		text, _ := value["text"].(string)
		if text == "" {
			return "", fmt.Errorf("agent_message has no text content")
		}
		return text, nil
	case []interface{}:
		var text strings.Builder
		for _, item := range value {
			message, _ := item.(map[string]interface{})
			inner, _ := message["content"].(map[string]interface{})
			part, _ := inner["text"].(string)
			if part == "" {
				return "", fmt.Errorf("agent_message has non-text content")
			}
			text.WriteString(part)
		}
		if text.Len() == 0 {
			return "", fmt.Errorf("agent_message has no text content")
		}
		return text.String(), nil
	case string:
		if value == "" {
			return "", fmt.Errorf("agent_message has no text content")
		}
		return value, nil
	default:
		return "", fmt.Errorf("agent_message has unsupported content")
	}
}

func decodeDoagentJSONObject(text string) (map[string]interface{}, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var object map[string]interface{}
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("expected exactly one JSON object")
		}
		return nil, err
	}
	return object, nil
}

// subscribeDoagentSSE 订阅 doagent 的 SSE 事件流，将内容实时转发到 WebSocket 客户端。
// 仅当收到权威 turn_finished 或 error 事件时返回。
func subscribeDoagentSSE(ctx context.Context, baseURL string, sessionID string, pushProgress notificationSender) error {
	return subscribeDoagentSSEWithCollector(ctx, baseURL, sessionID, pushProgress, nil)
}

func subscribeDoagentSSEWithCollector(ctx context.Context, baseURL string, sessionID string, pushProgress notificationSender, collector doagentSSECollector) error {
	return subscribeDoagentSSEWithCollectorReady(ctx, baseURL, sessionID, pushProgress, collector, nil, false, true, nil)
}

var doagentWaitingNoticeInterval = 20 * time.Second

func subscribeDoagentSSEWithCollectorReady(
	ctx context.Context,
	baseURL string,
	sessionID string,
	pushProgress notificationSender,
	collector doagentSSECollector,
	targetTurnIDReady <-chan string,
	allowUntaggedCurrentTurn bool,
	forwardAgentMessages bool,
	ready chan<- error,
	structuredFormats ...bool,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/events?sid="+sessionID, nil)
	if err != nil {
		if ready != nil {
			ready <- err
		}
		return fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		if ready != nil {
			ready <- err
		}
		return fmt.Errorf("SSE connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("SSE connect returned HTTP %d", resp.StatusCode)
		if ready != nil {
			ready <- err
		}
		return err
	}
	if ready != nil {
		ready <- nil
	}

	scanner := bufio.NewScanner(resp.Body)
	// One SSE event carries a whole tool result, so a line can be far larger
	// than a log line. A line beyond this limit used to kill the stream and
	// turn a finished turn into an unknown outcome.
	scanner.Buffer(make([]byte, 256*1024), maxDoagentSSELineBytes)

	var eventData strings.Builder
	var pendingAgentMessage strings.Builder
	targetTurnID := ""
	structuredEvents := len(structuredFormats) == 1 && structuredFormats[0]
	// Serialize the ticker with SSE delivery, including callers whose output
	// buffer is not concurrent. Notices describe silence, never tool progress.
	var progressMu sync.Mutex
	lastActivity := time.Now()
	lastProgress := lastActivity
	lastEvent := "SSE connected"
	sendProgress := pushProgress
	pushProgress = func(event notificationEvent) {
		progressMu.Lock()
		defer progressMu.Unlock()
		sendProgress(event)
		lastProgress = time.Now()
	}
	noticesDone, stopNotices := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(noticesDone)
		ticker := time.NewTicker(doagentWaitingNoticeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopNotices:
				return
			case <-ticker.C:
				progressMu.Lock()
				silent := time.Since(lastProgress)
				if silent >= doagentWaitingNoticeInterval {
					text := fmt.Sprintf("[waiting] Still waiting for the terminal result; last event: %s (%s ago).\n",
						lastEvent, time.Since(lastActivity).Round(time.Second))
					if structuredEvents {
						sendProgress(publicDoagentUINotification(map[string]interface{}{
							"sessionUpdate": "doops_notice",
							"content":       map[string]interface{}{"type": "text", "text": text},
						}))
					} else {
						sendProgress(rawNotification(text))
					}
					lastProgress = time.Now()
				}
				progressMu.Unlock()
			}
		}
	}()
	defer func() {
		close(stopNotices)
		<-noticesDone
	}()
	toolNames := make(map[string]string)
	flushPendingAgentMessage := func() {
		if pendingAgentMessage.Len() == 0 {
			return
		}
		text := redactSecretText(pendingAgentMessage.String())
		if forwardAgentMessages && !structuredEvents {
			pushProgress(assistantDeltaNotification(text))
		}
		if structuredEvents {
			pushProgress(publicDoagentUINotification(map[string]interface{}{
				"sessionUpdate": "agent_message", "turnId": targetTurnID,
				"content": map[string]interface{}{"type": "text", "text": pendingAgentMessage.String()},
			}))
		}
		pendingAgentMessage.Reset()
	}
	// No idle timer: a running tool (Helm rollout wait, image pull, build) is
	// silent by design. The stream ends on turn_finished, a read error when the
	// connection drops, or the caller's execution deadline.
	receivedEvent := false

	for scanner.Scan() {
		line := scanner.Text()

		// SSE 格式: "event: type", "data: {json}", 空行分隔
		if strings.HasPrefix(line, "event: ") {
			continue // 事件类型从 data 的 type 字段判断
		}

		if strings.HasPrefix(line, "data: ") {
			chunk := strings.TrimPrefix(line, "data: ")
			if eventData.Len()+len(chunk) > maxDoagentSSELineBytes {
				pushProgress(errorNotification("[error] doagent_sse_event_too_large"))
				return errors.New("doagent_sse_event_too_large")
			}
			eventData.WriteString(chunk)
			continue
		}

		// 空行 = 一个完整事件结束
		if line == "" && eventData.Len() > 0 {
			data := eventData.String()
			eventData.Reset()
			receivedEvent = true

			// doagent SSE 使用 JSON-RPC 2.0 通知格式：{"jsonrpc":"2.0","method":"...","params":{...}}
			update, err := decodeDoagentJSONObject(data)
			if err != nil {
				pushProgress(errorNotification("[error] doagent_sse_protocol_invalid"))
				return errors.New("doagent_sse_protocol_invalid")
			}

			method, _ := update["method"].(string)
			params, _ := update["params"].(map[string]interface{})

			switch method {
			case "session/update":
				if eventSessionID, _ := params["sessionId"].(string); eventSessionID != "" && eventSessionID != sessionID {
					continue
				}
				// session/update 包含不同类型的更新
				sessionUpdate, _ := params["update"].(map[string]interface{})
				updateType, _ := sessionUpdate["sessionUpdate"].(string)
				if targetTurnIDReady != nil {
					if updateType == "turn_finished" && sessionUpdate["replay"] == true {
						continue
					}
					if targetTurnID == "" {
						select {
						case turnID, ok := <-targetTurnIDReady:
							if !ok || strings.TrimSpace(turnID) == "" {
								return fmt.Errorf("doagent prompt turn scope was not established")
							}
							targetTurnID = turnID
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					eventTurnID, _ := sessionUpdate["turnId"].(string)
					if strings.TrimSpace(eventTurnID) == "" {
						if !allowUntaggedCurrentTurn ||
							sessionUpdate["replay"] == true ||
							updateType == "turn_finished" {
							continue
						}
					} else if eventTurnID != targetTurnID {
						continue
					}
				}
				if sessionUpdate["replay"] != true {
					progressMu.Lock()
					lastActivity, lastEvent = time.Now(), updateType
					if updateType == "tool_call" || updateType == "tool_call_update" {
						callID, _ := sessionUpdate["toolCallId"].(string)
						if name := doagentToolName(sessionUpdate); name != "" {
							toolNames[callID] = sanitizeSecretText(name)
						}
						status, _ := sessionUpdate["status"].(string)
						lastEvent = fmt.Sprintf("%s %s", toolNames[callID], sanitizeSecretText(status))
					}
					progressMu.Unlock()
				}
				if updateType == "agent_message_chunk" {
					text, err := doagentAgentMessageText(sessionUpdate["content"])
					if err != nil {
						return errors.New("doagent_sse_protocol_invalid")
					}
					if pendingAgentMessage.Len()+len(text) > 4<<20 {
						return errors.New("doagent_message_too_large")
					}
					pendingAgentMessage.WriteString(text)
					continue
				}
				if updateType == "agent_message" {
					// agent_message usually repeats the buffered chunks as the whole
					// reply. Forwarding both showed the same answer twice.
					final, err := doagentAgentMessageText(sessionUpdate["content"])
					if !structuredEvents && err == nil && pendingAgentMessage.Len() > 0 &&
						strings.HasPrefix(final, pendingAgentMessage.String()) {
						pendingAgentMessage.Reset()
					}
					flushPendingAgentMessage()
				}
				if updateType == "turn_finished" && pendingAgentMessage.Len() > 0 {
					// A reply streamed only as chunks has no agent_message event; the
					// collector still needs it as the turn's final answer.
					streamed := redactSecretText(pendingAgentMessage.String())
					flushPendingAgentMessage()
					if collector != nil {
						if err := collector(map[string]interface{}{
							"sessionUpdate": doagentStreamedReplyUpdate, "turnId": targetTurnID,
							"content": map[string]interface{}{"type": "text", "text": streamed},
						}); err != nil {
							return err
						}
					}
				}
				if updateType == "agent_progress" {
					text, err := doagentAgentMessageText(sessionUpdate["content"])
					if err != nil {
						return errors.New("doagent_sse_protocol_invalid")
					}
					public, err := redactDoagentUIText(text)
					if err != nil {
						public = "[progress text withheld: private payload could not be redacted; still waiting for the execution result]"
					}
					if sessionUpdate["replay"] != true {
						if structuredEvents {
							pushProgress(publicDoagentUINotification(map[string]interface{}{
								"sessionUpdate": "doops_notice", "turnId": targetTurnID,
								"content": map[string]interface{}{"type": "text", "text": public},
							}))
						} else {
							pushProgress(rawNotification("[progress] " + public + "\n"))
						}
					}
					// Progress is diagnostic UI text, not execution evidence.
					// Do not pass it to the receipt collector.
					continue
				}
				if collector != nil {
					if err := collector(sessionUpdate); err != nil {
						return err
					}
				}
				if structuredEvents {
					pushProgress(publicDoagentUINotification(sessionUpdate))
				}
				if updateType == "turn_finished" {
					return doagentTurnFinishedError(sessionUpdate)
				}
				if structuredEvents {
					continue
				}
				sessionUpdate = sanitizeDoagentSessionUpdate(sessionUpdate)
				switch updateType {
				case "tool_call", "tool_call_update":
					callID, _ := sessionUpdate["toolCallId"].(string)
					toolName := toolNames[callID]
					status, _ := sessionUpdate["status"].(string)
					if toolName != "" && status != "" && sessionUpdate["replay"] != true {
						pushProgress(toolNotification(toolName, sanitizeSecretText(status)))
					}
				case "agent_message":
					text, err := doagentAgentMessageText(sessionUpdate["content"])
					if err != nil {
						return errors.New("doagent_sse_protocol_invalid")
					}
					if forwardAgentMessages {
						pushProgress(assistantDeltaNotification(text))
					}
				case "completed", "usage_update":
					// 非权威状态或 token 统计，继续等待 turn_finished。
				}

			case "permission.updated":
				message := "doagent permission required"
				pushProgress(errorNotification("[error] " + message))
				return errors.New(message)

			case "error":
				errMsg := "doagent_execution_failed"
				if params != nil {
					for _, key := range []string{"error", "failureReason", "message", "reason"} {
						if value, ok := params[key].(string); ok && strings.TrimSpace(value) != "" {
							errMsg = sanitizeSecretText(value)
							break
						}
					}
				}
				return fmt.Errorf("doagent execution failed: %s", errMsg)

			default:
				// 其他事件（session/update chunks 等）静默处理
				if method != "" {
					log.Printf("doagent SSE event ignored")
				} else {
					pushProgress(errorNotification("[error] doagent_sse_protocol_invalid"))
					return errors.New("doagent_sse_protocol_invalid")
				}
			}
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("SSE read: %w", err)
	}
	if receivedEvent {
		return fmt.Errorf("doagent SSE ended before final event")
	}
	return fmt.Errorf("doagent SSE ended without events")
}

func doagentTurnFinishedError(update map[string]interface{}) error {
	public, err := doagentUIStrings(update, []string{"status", "stopReason", "failureReason", "error"})
	if err != nil {
		return fmt.Errorf("doagent turn error text could not be redacted: %w", err)
	}
	update = public
	status := strings.TrimSpace(fmt.Sprint(update["status"]))
	stopReason := strings.TrimSpace(fmt.Sprint(update["stopReason"]))
	message := strings.TrimSpace(fmt.Sprint(update["failureReason"]))
	if message == "" || message == "<nil>" {
		message = strings.TrimSpace(fmt.Sprint(update["error"]))
	}
	if message != "" && message != "<nil>" {
		return fmt.Errorf("doagent turn failed: %s", sanitizeSecretText(message))
	}
	if status == "completed" {
		return nil
	}
	if status == "" || status == "<nil>" {
		status = "unknown"
	}
	if stopReason == "" || stopReason == "<nil>" {
		return fmt.Errorf("doagent turn %s", status)
	}
	return fmt.Errorf("doagent turn %s: %s", status, stopReason)
}

// maxDoagentSSELineBytes bounds one doagent SSE line. A line holds a whole
// tool result, so this must be well above maxToolOutputBytes.
const maxDoagentSSELineBytes = 8 * 1024 * 1024

func sanitizeDoagentSessionUpdate(update map[string]interface{}) map[string]interface{} {
	if update == nil {
		return nil
	}
	sanitized := make(map[string]interface{}, len(update))
	for key, value := range update {
		sanitized[key] = value
	}
	updateType, _ := update["sessionUpdate"].(string)
	switch updateType {
	case "agent_message_chunk", "agent_message":
		// Message text is the answer itself; redact it without the error-text cap.
		sanitized["content"] = redactDoagentValue(update["content"])
	case "error", "permission_request", "turn_finished":
		for _, key := range []string{"error", "failureReason", "message", "reason"} {
			if value, ok := update[key].(string); ok {
				sanitized[key] = sanitizeSecretText(value)
			}
		}
	}
	return sanitized
}
