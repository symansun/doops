/**
 * client.go - MCP 客户端（持久 WebSocket 连接版）
 *
 * 核心改造：MCPClient 在首次调用时建立一条持久 WebSocket 连接，
 * 后续所有 Call() 和 CallAndCapture() 复用同一个连接。
 * 这解决了旧版每次命令都新建连接导致的：
 *   1. 重复 tool call 创建新连接
 *   2. 短连接受 LB idle timeout 截断
 *   3. 连接握手延迟累积
 */
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	websocketHandshakeAttempts   = 3
	websocketHandshakeRetryDelay = 50 * time.Millisecond
)

// MCPClient 是 doops CLI 的 MCP 客户端。
// 改造后持有一条持久 WebSocket 连接，支持多次 tool call 复用。
type MCPClient struct {
	Target       Server
	SessionStore *SessionStore
	SessionName  string
	Verbose      bool
	Token        string

	// --- 持久连接状态 ---
	mu           sync.Mutex
	connected    bool
	conn         *websocket.Conn // WebSocket 长连接
	reqCounter   int64           // 请求 ID 计数器（原子递增）
	capabilities map[string]interface{}

	// --- WS 消息分发 ---
	// 所有 WS 消息通过 dispatchLoop 读取并按 request ID 分发
	dispatching bool
	pendingMu   sync.RWMutex
	pending     map[int64]chan wsEvent // reqID -> 等待响应的 channel

	CallTimeout time.Duration
}

// wsEvent 封装从 WebSocket 流中解析出的 JSON-RPC 消息
type wsEvent struct {
	Raw     string                 // 原始 JSON 字符串
	Parsed  map[string]interface{} // 解析后的 JSON 对象
	IsError bool                   // 是否为错误
}

// NewMCPClient 创建 MCP 客户端实例（此时不建连）
func NewMCPClient(target Server, ss *SessionStore, sessionName string, verbose bool) *MCPClient {
	return &MCPClient{
		Target:       target,
		SessionStore: ss,
		SessionName:  sessionName,
		Verbose:      verbose,
		pending:      make(map[int64]chan wsEvent),
		CallTimeout:  defaultToolCallTimeout(),
	}
}

// log 打印调试日志（仅 verbose 模式）
func (c *MCPClient) log(step, msg string) {
	if c.Verbose {
		fmt.Printf("\033[94m[%s]\033[0m %s\n", step, msg)
	}
}

// nextReqID 原子递增生成唯一请求 ID
func (c *MCPClient) nextReqID() int64 {
	return atomic.AddInt64(&c.reqCounter, 1)
}

// connect 建立与 agent 的持久 WebSocket 连接并完成 MCP initialize 握手。
func (c *MCPClient) connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	wsURL, err := c.targetWebSocketURL()
	if err != nil {
		return err
	}

	c.log("CLIENT:WS_INIT", fmt.Sprintf("Connecting to %s", wsURL))

	header := http.Header{}
	if c.Token != "" {
		header.Set("Authorization", "Bearer "+c.Token)
	}

	var conn *websocket.Conn
	var resp *http.Response
	for attempt := 1; attempt <= websocketHandshakeAttempts; attempt++ {
		conn, resp, err = websocket.DefaultDialer.Dial(wsURL, header)
		if err == nil || resp != nil || !isTransientWebSocketHandshakeError(err) || attempt == websocketHandshakeAttempts {
			break
		}
		c.log("CLIENT:WS_RETRY", fmt.Sprintf(
			"WebSocket handshake ended before a response (attempt %d/%d): %v",
			attempt,
			websocketHandshakeAttempts,
			err,
		))
		time.Sleep(time.Duration(attempt) * websocketHandshakeRetryDelay)
	}
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			httpErr := decodeGatewayHTTPError("gateway /v1/rpc", resp, body)
			return fmt.Errorf("failed to connect to %s: %w", wsURL, httpErr)
		}
		return fmt.Errorf("failed to connect to gateway /v1/rpc at %s: %v", wsURL, err)
	}

	c.conn = conn

	// MCP Initialize 握手
	initID := c.nextReqID()
	initCh := c.registerPending(initID)
	defer c.unregisterPending(initID)

	if err := c.conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"id":      initID,
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo": map[string]string{
				"name":    "doops-go-cli",
				"version": "2.0",
			},
		},
	}); err != nil {
		c.conn.Close()
		return fmt.Errorf("initialization failed: %v", err)
	}

	// 启动后台事件分发循环
	go c.dispatchLoop()
	c.dispatching = true

	// 启动客户端级 Ping 心跳，防范 NAT/LB 针对单向长连（如长时间无包）的静默超时。
	// 大多数云厂商 LB idle timeout 是 60~120s。
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			c.mu.Lock()
			if !c.connected || c.conn == nil {
				c.mu.Unlock()
				return
			}
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	// 等待 initialize 响应（带超时）
	select {
	case evt := <-initCh:
		if err := validateInitializeResponse(initID, evt); err != nil {
			c.conn.Close()
			return err
		}
		c.capabilities = initializeCapabilities(evt)
	case <-time.After(5 * time.Second):
		c.conn.Close()
		return fmt.Errorf("initialize handshake timed out")
	}

	c.connected = true
	c.log("CLIENT:CONNECTED", "Persistent WebSocket connection established")
	return nil
}

func isTransientWebSocketHandshakeError(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func (c *MCPClient) targetWebSocketURL() (string, error) {
	if strings.TrimSpace(c.Target.Gateway) == "" {
		return "", fmt.Errorf("target %q has no Gateway route", c.Target.Name)
	}

	rawGateway := strings.TrimSpace(c.Target.Gateway)
	if !strings.Contains(rawGateway, "://") {
		rawGateway = "wss://" + rawGateway
	}
	u, err := url.Parse(rawGateway)
	if err != nil {
		return "", fmt.Errorf("invalid gateway URL %q: %w", c.Target.Gateway, err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported gateway scheme %q", u.Scheme)
	}
	if err := enforceSecureGatewayURL(c.Target.Gateway, u); err != nil {
		return "", err
	}
	u.Path = joinURLPath(u.Path, "v1", "rpc")
	q := u.Query()
	cluster := strings.TrimSpace(c.Target.Cluster)
	if cluster == "" {
		return "", fmt.Errorf("target %q has no Gateway cluster", c.Target.Name)
	}
	instance := strings.TrimSpace(c.Target.Instance)
	if instance == "" {
		return "", fmt.Errorf("target %q has no Gateway instance", c.Target.Name)
	}
	q.Set("cluster", cluster)
	q.Set("instance", instance)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// dispatchLoop 持续读取 WS 流并按 request ID 分发。
func (c *MCPClient) dispatchLoop() {
	for {
		msgType, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.log("CLIENT:WS_ERROR", fmt.Sprintf("WS read error: %v", err))
			} else {
				c.log("CLIENT:WS_CLOSE", "WS connection closed cleanly by server")
			}

			c.mu.Lock()
			c.connected = false
			c.dispatching = false
			c.mu.Unlock()

			// 通知所有等待者连接已断
			c.pendingMu.Lock()
			for _, ch := range c.pending {
				select {
				case ch <- wsEvent{IsError: true, Raw: "WS connection lost"}:
				default:
				}
			}
			// 清空 pending map 防止内存泄漏
			c.pending = make(map[int64]chan wsEvent)
			c.pendingMu.Unlock()
			return
		}

		if msgType != websocket.TextMessage {
			continue // 忽略 ping/pong 等二进制帧（gorilla 内部会自动处理底层心跳帧）
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		c.dispatchMessage(msg)
	}
}

func (c *MCPClient) dispatchMessage(msg map[string]interface{}) {
	evt := wsEvent{Parsed: msg}
	if raw, err := json.Marshal(msg); err == nil {
		evt.Raw = string(raw)
	}

	// 检查是否有 request ID 可以匹配
	if id, ok := msg["id"].(float64); ok {
		reqID := int64(id)
		c.pendingMu.RLock()
		ch, found := c.pending[reqID]
		c.pendingMu.RUnlock()
		if found {
			sendWSEvent(ch, evt)
			return
		}
	}

	if method, ok := msg["method"].(string); ok && method == "notifications/message" {
		requestID, ok := requestIDFromClientNotification(msg)
		if !ok {
			return
		}
		c.pendingMu.RLock()
		ch := c.pending[requestID]
		c.pendingMu.RUnlock()
		if ch != nil {
			sendWSEvent(ch, evt)
		}
	}
}

func sendWSEvent(ch chan wsEvent, evt wsEvent) {
	ch <- evt
}

func validateInitializeResponse(initID int64, evt wsEvent) error {
	if evt.IsError {
		return fmt.Errorf("initialize failed: %s", evt.Raw)
	}
	msg := evt.Parsed
	if msg == nil {
		return fmt.Errorf("initialize failed: empty response")
	}
	id, ok := msg["id"].(float64)
	if !ok || int64(id) != initID {
		return fmt.Errorf("initialize failed: unexpected response id")
	}
	if rpcErr, ok := msg["error"].(map[string]interface{}); ok {
		return fmt.Errorf("initialize failed: %v", rpcErr["message"])
	}
	if _, ok := msg["result"].(map[string]interface{}); !ok {
		return fmt.Errorf("initialize failed: missing result")
	}
	return nil
}

func initializeCapabilities(evt wsEvent) map[string]interface{} {
	result, _ := evt.Parsed["result"].(map[string]interface{})
	capabilities, _ := result["capabilities"].(map[string]interface{})
	return capabilities
}

func (c *MCPClient) RequireProtocol(protocol string) error {
	if err := c.connect(); err != nil {
		return err
	}
	protocols, _ := c.capabilities["protocols"].(map[string]interface{})
	if !protocolCapabilityCompatible(protocol, protocols[protocol]) {
		return fmt.Errorf(
			"target Agent does not advertise protocol %q; upgrade the Agent before using this operation",
			protocol,
		)
	}
	return nil
}

func (c *MCPClient) RequireAgentRelease(expected string) error {
	if err := c.connect(); err != nil {
		return err
	}
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return fmt.Errorf("expected DoOps release commit is empty")
	}
	actual, _ := c.capabilities["releaseCommit"].(string)
	actual = strings.TrimSpace(actual)
	if actual == "" {
		return fmt.Errorf("target Agent did not advertise a DoOps release commit")
	}
	if actual != expected {
		return fmt.Errorf("target Agent release commit mismatch: got %s, want %s", actual, expected)
	}
	return nil
}

func protocolCapabilityCompatible(protocol string, value interface{}) bool {
	switch protocol {
	case "structuredSSH":
		capability, ok := value.(map[string]interface{})
		return ok &&
			len(capability) == 3 &&
			capability["schemaVersion"] == "doops.sh/structured-ssh/v1" &&
			capability["tool"] == "doops_ssh" &&
			capability["structuredArgv"] == true
	default:
		return false
	}
}

// registerPending 为指定 reqID 注册一个事件接收 channel
func (c *MCPClient) registerPending(reqID int64) chan wsEvent {
	ch := make(chan wsEvent, 4096) // 流式输出可能包含批量 SSH/诊断结果
	c.pendingMu.Lock()
	c.pending[reqID] = ch
	c.pendingMu.Unlock()
	return ch
}

// unregisterPending 移除 reqID 的事件接收 channel
func (c *MCPClient) unregisterPending(reqID int64) {
	c.pendingMu.Lock()
	delete(c.pending, reqID)
	c.pendingMu.Unlock()
}

func requestIDFromClientNotification(msg map[string]interface{}) (int64, bool) {
	params, _ := msg["params"].(map[string]interface{})
	if params == nil {
		return 0, false
	}
	requestID, ok := params["request_id"]
	if !ok {
		return 0, false
	}
	switch value := requestID.(type) {
	case float64:
		if value != math.Trunc(value) || value < math.MinInt64 || value > math.MaxInt64 {
			return 0, false
		}
		return int64(value), true
	case int:
		return int64(value), true
	case int64:
		return value, true
	default:
		return 0, false
	}
}

// Close 关闭持久连接，释放资源
func (c *MCPClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connected = false
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
	ch := c.registerPending(reqID)
	defer c.unregisterPending(reqID)

	// 发送 tool call
	c.log("CLIENT:SEND_TOOL_CALL", fmt.Sprintf("Invoking tool %s (reqID=%d)", toolName, reqID))
	// WebSocket 发送
	c.mu.Lock()
	err := c.conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      reqID,
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": arguments,
		},
	})
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("tool call failed: %v", err)
	}

	// 监听事件：流式输出 + 最终结果
	streamedAny := false
	timeout := time.NewTimer(c.callTimeout())
	defer timeout.Stop()
	for {
		select {
		case evt := <-ch:
			if evt.IsError {
				return fmt.Errorf("connection lost: %s", evt.Raw)
			}

			msg := evt.Parsed

			// Handle progress notifications（流式 PTY 输出）
			if method, ok := msg["method"].(string); ok && method == "notifications/message" {
				if params, ok := msg["params"].(map[string]interface{}); ok {
					if chunk, ok := params["data"].(string); ok {
						lines := strings.Split(chunk, "\n")
						for i, l := range lines {
							trimmed := strings.TrimSpace(l)
							var contentToPrint string = trimmed
							var jsonPart string

							if idx := strings.Index(trimmed, "{"); idx != -1 {
								jsonPart = trimmed[idx:]
								contentToPrint = trimmed[:idx]
							}

							if jsonPart != "" {
								var agentEvt map[string]interface{}
								if err := json.Unmarshal([]byte(jsonPart), &agentEvt); err == nil {
									if contentToPrint != "" {
										fmt.Print(contentToPrint)
									}
									formatAgentEvent(agentEvt)
									streamedAny = true
									continue
								}
							}

							if i == len(lines)-1 && len(chunk) > 0 && chunk[len(chunk)-1] != '\n' {
								fmt.Print(l)
							} else if i < len(lines)-1 || (len(chunk) > 0 && chunk[len(chunk)-1] == '\n') {
								if len(l) > 0 {
									fmt.Println(l)
								}
							}
							streamedAny = true
						}
					}
				}
				continue
			}

			// Handle final result
			if id, ok := msg["id"].(float64); ok && int64(id) == reqID {
				if result, ok := msg["result"].(map[string]interface{}); ok {
					contentList, _ := result["content"].([]interface{})
					for _, item := range contentList {
						m := item.(map[string]interface{})
						if m["type"] == "text" {
							text := m["text"].(string)
							if !streamedAny || toolName == "doops_agent_prompt" || toolName == "doops_distill" || toolName == "doops_bg" {
								fmt.Println(text)
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
					return fmt.Errorf("remote error: %v", rpcErr["message"])
				}
				return nil // 收到最终结果，完成
			}
		case <-timeout.C:
			return fmt.Errorf("tool call timed out after %s", c.callTimeout())
		}
	}
}

// CallAndCapture 在持久连接上执行 MCP tool call，捕获结果文本而非打印。
func (c *MCPClient) CallAndCapture(toolName string, arguments map[string]interface{}) (string, error) {
	// 确保连接已建立
	if err := c.connect(); err != nil {
		return "", err
	}

	// 注入 session_id
	if sid := c.SessionStore.Get(c.Target.Name, c.SessionName); sid != "" {
		arguments["session_id"] = sid
	} else {
		arguments["session_id"] = c.SessionName
	}

	// 分配请求 ID 并注册接收 channel
	reqID := c.nextReqID()
	ch := c.registerPending(reqID)
	defer c.unregisterPending(reqID)

	// 发送 tool call
	c.mu.Lock()
	err := c.conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      reqID,
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": arguments,
		},
	})
	c.mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("tool call failed: %v", err)
	}

	var capturedOutput strings.Builder

	// 等待最终结果
	timeout := time.NewTimer(c.callTimeout())
	defer timeout.Stop()
	for {
		select {
		case evt := <-ch:
			if evt.IsError {
				return "", fmt.Errorf("connection lost: %s", evt.Raw)
			}

			msg := evt.Parsed

			// 累加流式输出
			if method, ok := msg["method"].(string); ok && method == "notifications/message" {
				if params, ok := msg["params"].(map[string]interface{}); ok {
					if chunk, ok := params["data"].(string); ok {
						capturedOutput.WriteString(captureToolNotificationText(chunk))
					}
				}
				continue
			}

			if _, ok := msg["method"]; ok {
				continue
			}

			if id, ok := msg["id"].(float64); ok && int64(id) == reqID {
				if result, ok := msg["result"].(map[string]interface{}); ok {
					if toolResultIsError(result) {
						return "", fmt.Errorf("%s", toolResultText(result, "tool returned an error"))
					}
					if text := toolResultText(result, ""); text != "" {
						capturedOutput.WriteString(text)
					}
					if capturedOutput.Len() > 0 {
						return capturedOutput.String(), nil
					}
				} else if rpcErr, ok := msg["error"].(map[string]interface{}); ok {
					return "", fmt.Errorf("remote error: %v", rpcErr["message"])
				}

				return "", fmt.Errorf("no result received")
			}
		case <-timeout.C:
			return "", fmt.Errorf("tool call timed out after %s", c.callTimeout())
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
	ch := c.registerPending(reqID)
	defer c.unregisterPending(reqID)

	c.mu.Lock()
	err := c.conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      reqID,
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": arguments,
		},
	})
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("tool call failed: %v", err)
	}

	timeout := time.NewTimer(c.callTimeout())
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
				return fmt.Errorf("remote error: %v", rpcErr["message"])
			}
			return fmt.Errorf("tool %s returned no result", toolName)
		case <-timeout.C:
			return fmt.Errorf("tool call timed out after %s", c.callTimeout())
		}
	}
}

func captureToolNotificationText(chunk string) string {
	var captured strings.Builder
	for _, line := range strings.Split(chunk, "\n") {
		content := line
		trimmed := strings.TrimSpace(line)
		if idx := strings.Index(trimmed, "{"); idx != -1 {
			jsonPart := trimmed[idx:]
			var event map[string]interface{}
			if json.Unmarshal([]byte(jsonPart), &event) == nil {
				if _, ok := event["type"].(string); ok {
					content = line[:strings.Index(line, "{")]
				}
			}
		}
		if content != "" {
			captured.WriteString(content)
			captured.WriteByte('\n')
		}
	}
	return captured.String()
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
		return 30 * time.Minute
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return 30 * time.Minute
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

// formatAgentEvent 将 doagent 的 JSON 事件格式化为人类可读的终端输出。
// 支持 step_start、tool_use、text、step_finish 四种核心事件类型。
func formatAgentEvent(evt map[string]interface{}) {
	evtType, _ := evt["type"].(string)

	switch evtType {
	case "step_start":
		fmt.Println("\n\033[96m🚀 开始新步骤...\033[0m")

	case "tool_use":
		part, _ := evt["part"].(map[string]interface{})
		if part == nil {
			return
		}
		toolName, _ := part["tool"].(string)
		state, _ := part["state"].(map[string]interface{})
		if state == nil {
			return
		}

		input, _ := state["input"].(map[string]interface{})
		cmd, _ := input["command"].(string)
		desc, _ := input["description"].(string)

		if cmd != "" {
			fmt.Printf("\033[93m🔧 [%s]\033[0m %s\n", toolName, cmd)
		} else if desc != "" {
			fmt.Printf("\033[93m🔧 [%s]\033[0m %s\n", toolName, desc)
		} else {
			title, _ := state["title"].(string)
			if title != "" {
				fmt.Printf("\033[93m📋 [%s]\033[0m %s\n", toolName, title)
			}
		}

		output, _ := state["output"].(string)
		errorMsg, _ := state["error"].(string)

		if errorMsg != "" {
			lines := strings.Split(strings.TrimSpace(errorMsg), "\n")
			for _, l := range lines {
				fmt.Printf("  \033[91m│\033[0m %s\n", l)
			}
		} else if output != "" {
			lines := strings.Split(strings.TrimSpace(output), "\n")
			maxLines := 15
			if len(lines) <= maxLines {
				for _, l := range lines {
					fmt.Printf("  \033[90m│\033[0m %s\n", l)
				}
			} else {
				for _, l := range lines[:5] {
					fmt.Printf("  \033[90m│\033[0m %s\n", l)
				}
				fmt.Printf("  \033[90m│ ... (%d lines omitted)\033[0m\n", len(lines)-10)
				for _, l := range lines[len(lines)-5:] {
					fmt.Printf("  \033[90m│\033[0m %s\n", l)
				}
			}
		}

	case "text":
		part, _ := evt["part"].(map[string]interface{})
		if part == nil {
			return
		}
		text, _ := part["text"].(string)
		if text != "" {
			fmt.Printf("\n\033[92m💬 AI:\033[0m %s\n", text)
		}

	case "step_finish":
		part, _ := evt["part"].(map[string]interface{})
		if part == nil {
			return
		}
		reason, _ := part["reason"].(string)
		switch reason {
		case "end-turn", "stop":
			fmt.Println("\033[92m✅ 任务完成\033[0m")
		case "tool-calls":
			fmt.Println("\033[90m   ↳ 继续执行下一步...\033[0m")
		default:
			fmt.Printf("\033[90m   ↳ 步骤结束 (reason: %s)\033[0m\n", reason)
		}
	}
}

// ExecBg streams one doops_bg call until the Agent reports the process has stopped.
func (c *MCPClient) ExecBg(command string) error {
	return c.Call("doops_bg", map[string]interface{}{
		"command": command,
	})
}

// postJSON 已废弃，被直接的 conn.WriteJSON 替代

// Distill 对长输出进行 LLM 压缩摘要
func (c *MCPClient) Distill(content string) string {
	maxSize := 2048
	if len(content) < maxSize {
		return content
	}

	apiEndpoint := os.Getenv("OPENAI_API_BASE")
	if apiEndpoint == "" {
		apiEndpoint = os.Getenv("API_BASE_URL")
	}
	if apiEndpoint != "" && !strings.HasSuffix(apiEndpoint, "/chat/completions") {
		apiEndpoint = strings.TrimRight(apiEndpoint, "/") + "/chat/completions"
	}
	if apiEndpoint == "" {
		apiEndpoint = "https://api.example.com/v1/chat/completions"
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return content // 未配置 API Key，不做压缩
	}
	model := os.Getenv("DO_AGENT_MODEL")
	if model == "" {
		model = "gpt-5.4-mini"
	}

	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a Log Distillation Expert. Analyze the raw log/output and distill it into a concise summary focusing on status, errors, resource usage, and key paths. Max 500 words."},
			{"role": "user", "content": fmt.Sprintf("=== RAW OUTPUT START ===\n%s\n=== RAW OUTPUT END ===", content)},
		},
		"temperature": 0.3,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", apiEndpoint, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return content
	}
	defer resp.Body.Close()

	var respData struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil || len(respData.Choices) == 0 {
		return content
	}

	originalSize := len(content)
	distilledText := respData.Choices[0].Message.Content
	ratio := float64(len(distilledText)) / float64(originalSize)

	return fmt.Sprintf("\n[Smart Compression %.1f%%] Original: %dB\n%s\n%s\n%s\n", ratio*100, originalSize, strings.Repeat("-", 50), distilledText, strings.Repeat("-", 50))
}
