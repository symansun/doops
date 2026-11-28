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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// MCPClient 是 doops CLI 的 MCP 客户端。
// 改造后持有一条持久 WebSocket 连接，支持多次 tool call 复用。
type MCPClient struct {
	Target       Server
	SessionStore *SessionStore
	SessionName  string
	Verbose      bool
	RawOutput    bool // 抑制非命令原始输出的进度心跳与终态壳层文案（用于 exec/ssh/k8s --raw/--quiet）
	Token        string
	// UpgradeOperationID correlates upgrade calls and their initialize probes.
	UpgradeOperationID string

	// --- 持久连接状态 ---
	mu           sync.Mutex
	writes       chan struct{}
	connected    bool
	conn         *websocket.Conn // WebSocket 长连接
	reqCounter   int64           // 请求 ID 计数器（原子递增）
	capabilities map[string]interface{}
	// Enabled only by the COS snapshot client; every connection must advertise
	// the Gateway's executable-bit contract before snapshot calls can be sent.
	requireCOSSnapshotExecutable bool

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
		writes:       make(chan struct{}, 1),
		CallTimeout:  defaultToolCallTimeout(),
	}
}

// log 打印调试日志（仅 verbose 模式）
func (c *MCPClient) log(step, msg string) {
	if c.Verbose {
		if c.UpgradeOperationID != "" {
			msg = redactUpgradeText(msg, c.Token)
		}
		if c.RawOutput {
			fmt.Fprintf(os.Stderr, "\033[94m[%s]\033[0m %s\n", step, msg)
			return
		}
		fmt.Printf("\033[94m[%s]\033[0m %s\n", step, msg)
	}
}

// nextReqID 原子递增生成唯一请求 ID
func (c *MCPClient) nextReqID() int64 {
	return atomic.AddInt64(&c.reqCounter, 1)
}

// dispatchLoop 持续读取 WS 流并按 request ID 分发。
func (c *MCPClient) dispatchLoop(conn *websocket.Conn) {
	for {
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.log("CLIENT:WS_ERROR", fmt.Sprintf("WS read error: %v", err))
			} else {
				c.log("CLIENT:WS_CLOSE", "WS connection closed cleanly by server")
			}

			c.closeConnection(conn)
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

	method, _ := msg["method"].(string)
	if method == "notifications/message" || method == "notifications/doops/background_task" {
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

// Bind the pending response and its writer before Close can retire this connection.
func (c *MCPClient) registerCall(reqID int64) (*websocket.Conn, chan wsEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn, c.registerPending(reqID)
}

// writeRequest bounds both writer acquisition and socket backpressure. A failed
// write may have reached the peer; close that transport and never replay it.
func (c *MCPClient) writeRequest(conn *websocket.Conn, request interface{}, deadline time.Time) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case c.writes <- struct{}{}:
		defer func() { <-c.writes }()
	case <-timer.C:
		return fmt.Errorf("request not sent: %w", context.DeadlineExceeded)
	}
	c.mu.Lock()
	connected := c.connected && c.conn == conn
	protocolErr := c.cosSnapshotGatewayErrorLocked()
	c.mu.Unlock()
	if !connected || conn == nil {
		return errors.New("request not sent: connection is not available")
	}
	if protocolErr != nil {
		return protocolErr
	}
	if !time.Now().Before(deadline) {
		return fmt.Errorf("request not sent: %w", context.DeadlineExceeded)
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		c.closeConnection(conn)
		return fmt.Errorf("request not sent: %w", err)
	}
	if err := conn.WriteJSON(request); err != nil {
		c.closeConnection(conn)
		return fmt.Errorf("%w: WebSocket request write failed: %v", executionOutcomeUnknownError(), err)
	}
	return nil
}

func (c *MCPClient) requestCancellation(conn *websocket.Conn, reqID int64, deadline time.Time) error {
	return c.writeRequest(conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/cancel",
		"id":      fmt.Sprintf("cancel-%d", reqID),
		"params":  map[string]interface{}{"id": reqID},
	}, deadline)
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
	c.closeConnectionLocked(c.conn)
}

func (c *MCPClient) closeConnection(conn *websocket.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeConnectionLocked(conn)
}

func (c *MCPClient) closeConnectionLocked(conn *websocket.Conn) {
	if conn == nil || c.conn != conn {
		return
	}
	conn.Close()
	c.conn = nil
	c.connected = false
	c.dispatching = false
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for _, ch := range c.pending {
		select {
		case ch <- wsEvent{IsError: true, Raw: "WS connection lost"}:
		default:
		}
	}
	c.pending = make(map[int64]chan wsEvent)
}
