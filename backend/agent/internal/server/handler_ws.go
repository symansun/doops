package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/pkg/release"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ServeGatewayWebSocketConnWithReady serves the reverse tunnel after Gateway
// authorization.
func (gw *Gateway) ServeGatewayWebSocketConnWithReady(conn *websocket.Conn, remoteAddr string, onReady func()) {
	manifestDir, err := agentManifestDirectory()
	if err != nil {
		log.Printf("[agent] target manifest directory unavailable: %v", err)
	}
	gw.serveWebSocketConn(conn, remoteAddr, onReady, manifestDir)
}

func (gw *Gateway) serveWebSocketConn(conn *websocket.Conn, remoteAddr string, onReady func(), manifestDir string) {
	defer conn.Close()
	conn.SetReadLimit(maxWebSocketMessageBytes())
	connectionCtx, cancelConnection := context.WithCancel(context.Background())
	var resourceSession *agentResourceSession

	// per-connection 写互斥锁（gorilla/websocket 不允许并发写）
	var connMu sync.Mutex
	var dataWriteState websocketDataWriteState
	// Read once per connection so a read timeouts change cannot split a deadline
	// window between the initial deadline, the pong handler and the loop.
	readTimeout := agentTunnelReadTimeout()

	conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readTimeout))
	})

	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() {
		ticker := time.NewTicker(agentWSPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingDone:
				return
			case <-ticker.C:
				if !writeHeartbeatPing(conn, &dataWriteState, nil, agentWSPingWriteTimeout) {
					return
				}
			}
		}
	}()

	log.Printf("🔗 WS Client Connected: %s", remoteAddr)

	// per-connection 线程安全写函数（替代全局 gw.mu）
	writeJSON := func(v interface{}) {
		if err := writeWebSocketJSON(conn, &connMu, &dataWriteState, v, agentWSDataWriteTimeout); err != nil {
			log.Printf("WS WriteJSON error: %v", err)
		}
	}

	// Request bodies are handed to handlers through tunnelBody so a slow handler
	// can never block this read loop, which also answers Gateway pings.
	gitBodyMu := sync.Mutex{}
	gitBodyWriters := make(map[int64]*tunnelBody)
	activeGitMu := sync.Mutex{}
	activeGitCancels := make(map[int64]context.CancelFunc)
	proxyBodyMu := sync.Mutex{}
	proxyBodyWriters := make(map[int64]*tunnelBody)
	activeProxyMu := sync.Mutex{}
	activeProxyCancels := make(map[int64]context.CancelFunc)
	activeToolMu := sync.Mutex{}
	activeToolCalls := make(map[int64]activeToolCall)
	var executionWG sync.WaitGroup
	// inFlightToolCalls counts started tool calls that have not returned yet.
	// It only feeds the disconnect log; cancellation semantics stay with
	// connectionCtx and executionWG.
	var inFlightToolCalls atomic.Int64
	var readyOnce sync.Once
	closeGitBody := func(id int64) {
		gitBodyMu.Lock()
		body := gitBodyWriters[id]
		delete(gitBodyWriters, id)
		gitBodyMu.Unlock()
		if body != nil {
			body.Abort(nil)
		}
	}
	closeProxyBody := func(id int64) {
		proxyBodyMu.Lock()
		body := proxyBodyWriters[id]
		delete(proxyBodyWriters, id)
		proxyBodyMu.Unlock()
		if body != nil {
			body.Abort(nil)
		}
	}
	defer func() {
		_ = conn.Close()
		cancelConnection()
		gitBodyMu.Lock()
		writers := gitBodyWriters
		gitBodyWriters = make(map[int64]*tunnelBody)
		gitBodyMu.Unlock()
		for _, body := range writers {
			body.Abort(nil)
		}
		proxyBodyMu.Lock()
		proxyWriters := proxyBodyWriters
		proxyBodyWriters = make(map[int64]*tunnelBody)
		proxyBodyMu.Unlock()
		for _, body := range proxyWriters {
			body.Abort(nil)
		}
		executionWG.Wait()
	}()

	// 主读取循环
	for {
		messageType, message, err := readWebSocketMessage(conn, func() {
			// Data frames count as liveness; pongs keep this deadline too.
			_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		})
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WS error: %v", err)
			}
			break
		}
		if messageType != websocket.TextMessage {
			continue
		}

		var req api.JSONRPCRequest
		if err := json.Unmarshal(message, &req); err != nil {
			// 如果连格式都不是 JSONRPC，忽略
			continue
		}

		if req.Method == "initialize" {
			// 处理初始化握手
			runtime := currentTargetRuntime(connectionCtx)
			writeJSON(api.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"serverInfo": map[string]string{
						"name":          "doops-agent",
						"version":       release.Version,
						"releaseCommit": release.Commit,
					},
					"runtimeId": agentProcessRuntimeID,
					// INT-82b: a per-process generation so the Gateway can tell a
					// reattach to this process from a redelivery after a restart.
					"bootId":              agentBootID,
					"targetMode":          runtime.mode(),
					"kubernetesAvailable": runtime.Kubernetes,
					"capabilities": map[string]interface{}{
						"tools": map[string]interface{}{},
						"protocols": map[string]interface{}{
							"credentialPlan":              credentialPlanCapability(),
							"structuredSSH":               structuredSSHCapability(),
							"executionLifecycle":          executionLifecycleCapability(),
							upgradeAuditProtocol:          upgradeAuditCapability(),
							registryAuthfileProtocol:      registryAuthfileCapability(),
							cosSnapshotExecutableProtocol: cosSnapshotExecutableCapability(),
							toolDeadlineProtocol:          toolDeadlineCapability(),
							knowledgeBaseProtocol:         knowledgeBaseCapability(),
							agentResourceSyncProtocol:     onReady != nil && manifestDir != "",
						},
					},
				},
			})
			continue
		}
		if req.Method == "gateway/registered" {
			if onReady != nil {
				readyOnce.Do(func() {
					var target targetManifestTarget
					if json.Unmarshal(req.Params, &target) == nil && target.Cluster != "" && target.Instance != "" {
						resourceSession = &agentResourceSession{target: target, dir: manifestDir}
					}
					onReady()
				})
			}
			continue
		}
		if req.Method == agentResourceObserveMethod || req.Method == agentResourceApplyMethod {
			if resourceSession == nil {
				writeJSON(buildErrorResponse(req.ID, -32601, "target_resource_sync_requires_gateway"))
				continue
			}
			session := resourceSession
			executionWG.Add(1)
			go func() {
				defer executionWG.Done()
				session.handle(connectionCtx, req, writeJSON)
			}()
			continue
		}

		if req.Method == "git/http" {
			id, ok := numericID(req.ID)
			if !ok {
				writeJSON(api.JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &api.RPCError{Code: -32602, Message: "git/http requires numeric id"},
				})
				continue
			}
			var params gitHTTPRequestParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				writeJSON(api.JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &api.RPCError{Code: -32602, Message: "Invalid git/http params"},
				})
				continue
			}
			body := newTunnelBody()
			gitBodyMu.Lock()
			gitBodyWriters[id] = body
			gitBodyMu.Unlock()
			ctx, cancel := context.WithCancel(connectionCtx)
			activeGitMu.Lock()
			activeGitCancels[id] = cancel
			activeGitMu.Unlock()
			executionWG.Add(1)
			go func() {
				defer executionWG.Done()
				defer func() {
					activeGitMu.Lock()
					delete(activeGitCancels, id)
					activeGitMu.Unlock()
					cancel()
				}()
				defer closeGitBody(id)
				gw.handleGitHTTPOverWS(ctx, req.ID, params, body.Reader(), writeJSON)
			}()
			continue
		}

		if req.Method == "proxy/http" {
			id, ok := numericID(req.ID)
			if !ok {
				writeJSON(api.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
					Error: &api.RPCError{Code: -32602, Message: "proxy/http requires numeric id"}})
				continue
			}
			var params proxyHTTPRequestParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				writeJSON(api.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
					Error: &api.RPCError{Code: -32602, Message: "Invalid proxy/http params"}})
				continue
			}
			body := newTunnelBody()
			proxyBodyMu.Lock()
			proxyBodyWriters[id] = body
			proxyBodyMu.Unlock()
			ctx, cancel := context.WithCancel(connectionCtx)
			activeProxyMu.Lock()
			activeProxyCancels[id] = cancel
			activeProxyMu.Unlock()
			executionWG.Add(1)
			go func() {
				defer executionWG.Done()
				defer func() {
					activeProxyMu.Lock()
					delete(activeProxyCancels, id)
					activeProxyMu.Unlock()
					cancel()
				}()
				defer closeProxyBody(id)
				gw.handleHTTPProxyOverWS(ctx, req.ID, params, body.Reader(), writeJSON)
			}()
			continue
		}

		if req.Method == "git/cancel" {
			var params struct {
				ID int64 `json:"id"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if params.ID == 0 {
				if id, ok := numericID(req.ID); ok {
					params.ID = id
				}
			}
			activeGitMu.Lock()
			cancel := activeGitCancels[params.ID]
			activeGitMu.Unlock()
			if cancel != nil {
				cancel()
				closeGitBody(params.ID)
			}
			writeJSON(api.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]interface{}{"canceled": cancel != nil},
			})
			continue
		}

		if req.Method == "proxy/cancel" {
			var params struct {
				ID int64 `json:"id"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if params.ID == 0 {
				if id, ok := numericID(req.ID); ok {
					params.ID = id
				}
			}
			activeProxyMu.Lock()
			cancel := activeProxyCancels[params.ID]
			activeProxyMu.Unlock()
			if cancel != nil {
				cancel()
			}
			closeProxyBody(params.ID)
			writeJSON(api.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Result: map[string]interface{}{"canceled": cancel != nil}})
			continue
		}

		if req.Method == "git/body" {
			var frame struct {
				ID      int64  `json:"id"`
				DataB64 string `json:"data_b64,omitempty"`
				EOF     bool   `json:"eof,omitempty"`
			}
			if err := json.Unmarshal(req.Params, &frame); err != nil {
				continue
			}
			gitBodyMu.Lock()
			body := gitBodyWriters[frame.ID]
			gitBodyMu.Unlock()
			if body == nil {
				continue
			}
			if !pushTunnelBodyFrame(body, frame.DataB64, frame.EOF) {
				closeGitBody(frame.ID)
			}
			continue
		}

		if req.Method == "proxy/body" {
			var frame struct {
				ID      int64  `json:"id"`
				DataB64 string `json:"data_b64,omitempty"`
				EOF     bool   `json:"eof,omitempty"`
			}
			if err := json.Unmarshal(req.Params, &frame); err != nil {
				continue
			}
			proxyBodyMu.Lock()
			body := proxyBodyWriters[frame.ID]
			proxyBodyMu.Unlock()
			if body == nil {
				continue
			}
			if !pushTunnelBodyFrame(body, frame.DataB64, frame.EOF) {
				closeProxyBody(frame.ID)
			}
			continue
		}

		if req.Method == "tools/cancel" {
			var params struct {
				ID          int64  `json:"id"`
				ExecutionID string `json:"execution_id"`
			}
			_ = json.Unmarshal(req.Params, &params)
			// INT-82a: cancel by execution ID is the Gateway's entry point for
			// POST /v1/executions/<id>/cancel. It is separate from the client's
			// tools/cancel (which carries the numeric request id): the addressable
			// execution is not in activeToolCalls, so the numeric path cannot
			// reach it.
			if params.ExecutionID != "" {
				found, finished := agentExecutionStore.cancelExecutionByID(params.ExecutionID)
				writeJSON(api.JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]interface{}{
						"canceled": found,
						"finished": finished,
					},
				})
				continue
			}
			if params.ID == 0 {
				if id, ok := numericID(req.ID); ok {
					params.ID = id
				}
			}
			activeToolMu.Lock()
			call, ok := activeToolCalls[params.ID]
			activeToolMu.Unlock()
			if ok {
				call.cancel()
			}
			writeJSON(api.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]interface{}{"canceled": ok},
			})
			continue
		}

		if req.Method == "tools/call" {
			var params api.ToolCallParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				writeJSON(api.JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &api.RPCError{Code: -32602, Message: "Invalid Request"},
				})
				continue
			}

			session := resourceSession
			run := func(ctx context.Context, write func(interface{})) {
				if len(params.TargetManifest) != 0 || session != nil && usesExecutionManifest(params.Name) {
					if session == nil {
						write(buildErrorResponse(req.ID, -32602, "target_resource_sync_requires_gateway"))
						return
					}
					pinned, err := session.pinExecution(ctx, params)
					if err != nil {
						write(buildErrorResponse(req.ID, -32603, err.Error()))
						return
					}
					ctx = pinned
				}
				gw.handleToolCallOverWS(ctx, req.ID, params.Name, params.Arguments, params.TimeoutMS, write)
			}

			// 断线续接: an execution that carries an execution_id belongs to the
			// Agent, not to this tunnel connection. It keeps running after the
			// socket closes and is re-attached when the Gateway reconnects.
			if executionID, _ := executionIDFromToolCall(req.Params); executionID != "" {
				gw.resumeAgentToolCall(connectionCtx, executionID, req.ID, params, run, writeJSON)
				continue
			}

			// 异步处理实际的 tool 执行
			ctx, cancel := context.WithCancel(connectionCtx)
			if id, ok := numericID(req.ID); ok {
				activeToolMu.Lock()
				activeToolCalls[id] = activeToolCall{
					cancel: cancel,
				}
				activeToolMu.Unlock()
				inFlightToolCalls.Add(1)
				executionWG.Add(1)
				go func() {
					defer executionWG.Done()
					defer inFlightToolCalls.Add(-1)
					defer func() {
						activeToolMu.Lock()
						delete(activeToolCalls, id)
						activeToolMu.Unlock()
						cancel()
					}()
					run(ctx, writeJSON)
				}()
			} else {
				inFlightToolCalls.Add(1)
				executionWG.Add(1)
				go func() {
					defer executionWG.Done()
					defer inFlightToolCalls.Add(-1)
					defer cancel()
					run(ctx, writeJSON)
				}()
			}
		}
	}

	// Cleanup below cancels this connection's executions. Report the count once
	// before cancelling so an idle-timeout disconnect is visible in Agent logs;
	// only the count is logged, never command text.
	if pending := inFlightToolCalls.Load(); pending > 0 {
		log.Printf("[agent] tunnel disconnected; ending %d in-flight executions after the read timeout", pending)
	}

	log.Printf("🔗 WS Client Disconnected: %s", remoteAddr)
}

var agentProcessRuntimeID = fmt.Sprintf("%s:%d:%d", processHostname(), os.Getpid(), time.Now().UnixNano())

func processHostname() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "unknown"
	}
	return strings.TrimSpace(hostname)
}

type activeToolCall struct {
	cancel context.CancelFunc
}
