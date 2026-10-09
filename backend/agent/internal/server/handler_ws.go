package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/internal/dispatcher"
	"github.com/user/doops/agent/pkg/release"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: checkWebSocketOrigin,
}

// checkWebSocketOrigin decides whether a cross-origin WebSocket upgrade is
// allowed. Non-browser clients (CLI/SDK) typically send no Origin header and
// are always allowed. Browser clients are validated against the request host
// and an optional allowlist (DOOPS_ALLOWED_WS_ORIGINS, comma-separated). A
// fully permissive mode (the old behaviour) can be re-enabled for development
// via DOOPS_ALLOW_ALL_WS_ORIGINS=1.
func checkWebSocketOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// No Origin header: not a browser-enforced request (CLI/SDK/agent).
		return true
	}
	if strings.TrimSpace(os.Getenv("DOOPS_ALLOW_ALL_WS_ORIGINS")) == "1" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := strings.ToLower(originURL.Host)
	if originHost == "" {
		return false
	}
	// Same-host requests are always allowed.
	if strings.EqualFold(originHost, r.Host) || strings.EqualFold(originURL.Hostname(), hostOnly(r.Host)) {
		return true
	}
	for _, allowed := range strings.Split(os.Getenv("DOOPS_ALLOWED_WS_ORIGINS"), ",") {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		if allowed == originHost || allowed == strings.ToLower(originURL.Hostname()) {
			return true
		}
	}
	return false
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

const (
	agentWSReadTimeout            = 20 * time.Second
	agentWSPingInterval           = 5 * time.Second
	agentWSPingWriteTimeout       = 3 * time.Second
	agentWSDataWriteTimeout       = 60 * time.Second
	maxAgentPromptAttachments     = 4
	maxAgentPromptAttachmentBytes = 25 * 1024 * 1024
)

type websocketControlWriter interface {
	WriteControl(messageType int, data []byte, deadline time.Time) error
	Close() error
}

type websocketDataWriter interface {
	SetWriteDeadline(deadline time.Time) error
	WriteJSON(v interface{}) error
	Close() error
}

type websocketBinaryWriter interface {
	SetWriteDeadline(deadline time.Time) error
	WriteMessage(messageType int, data []byte) error
	Close() error
}

type websocketDataWriteState struct {
	sequence atomic.Uint64
}

func (s *websocketDataWriteState) begin() {
	s.sequence.Add(1)
}

func (s *websocketDataWriteState) end() {
	s.sequence.Add(1)
}

func (s *websocketDataWriteState) overlaps(start uint64) bool {
	end := s.sequence.Load()
	return start%2 == 1 || end%2 == 1 || start != end
}

func writeHeartbeatControl(conn websocketControlWriter, dataWriteState *websocketDataWriteState, messageType int, payload []byte, timeout time.Duration) bool {
	writeSequence := dataWriteState.sequence.Load()
	if err := conn.WriteControl(messageType, payload, time.Now().Add(timeout)); err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) &&
			netErr.Timeout() &&
			netErr.Temporary() &&
			dataWriteState.overlaps(writeSequence) {
			return true
		}
		_ = conn.Close()
		return false
	}
	return true
}

func writeHeartbeatPing(conn websocketControlWriter, dataWriteState *websocketDataWriteState, payload []byte, timeout time.Duration) bool {
	return writeHeartbeatControl(conn, dataWriteState, websocket.PingMessage, payload, timeout)
}

func writeWebSocketBinary(conn websocketBinaryWriter, writeMu *sync.Mutex, dataWriteState *websocketDataWriteState, payload []byte, timeout time.Duration) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	dataWriteState.begin()
	defer dataWriteState.end()
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		_ = conn.Close()
		return err
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return err
	}
	return nil
}

func writeWebSocketJSON(conn websocketDataWriter, writeMu *sync.Mutex, dataWriteState *websocketDataWriteState, value interface{}, timeout time.Duration) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	dataWriteState.begin()
	defer dataWriteState.end()
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return err
	}
	if err := conn.WriteJSON(value); err != nil {
		_ = conn.Close()
		return err
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return err
	}
	return nil
}

func readWebSocketMessage(conn *websocket.Conn, onProgress func()) (int, []byte, error) {
	messageType, reader, err := conn.NextReader()
	if err != nil {
		return 0, nil, err
	}
	var message bytes.Buffer
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			_, _ = message.Write(buffer[:n])
			if onProgress != nil {
				onProgress()
			}
		}
		if readErr == io.EOF {
			return messageType, message.Bytes(), nil
		}
		if readErr != nil {
			return 0, nil, readErr
		}
	}
}

// ServeGatewayWebSocketConnWithReady serves the reverse tunnel after Gateway
// authorization.
func (gw *Gateway) ServeGatewayWebSocketConnWithReady(conn *websocket.Conn, remoteAddr string, onReady func()) {
	gw.serveWebSocketConn(conn, remoteAddr, onReady)
}

func (gw *Gateway) serveWebSocketConn(conn *websocket.Conn, remoteAddr string, onReady func()) {
	defer conn.Close()
	conn.SetReadLimit(maxWebSocketMessageBytes())
	connectionCtx, cancelConnection := context.WithCancel(context.Background())

	// per-connection 写互斥锁（gorilla/websocket 不允许并发写）
	var connMu sync.Mutex
	var dataWriteState websocketDataWriteState

	conn.SetReadDeadline(time.Now().Add(agentWSReadTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(agentWSReadTimeout))
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

	gitBodyMu := sync.Mutex{}
	gitBodyWriters := make(map[int64]*io.PipeWriter)
	activeGitMu := sync.Mutex{}
	activeGitCancels := make(map[int64]context.CancelFunc)
	proxyBodyMu := sync.Mutex{}
	proxyBodyWriters := make(map[int64]*io.PipeWriter)
	activeProxyMu := sync.Mutex{}
	activeProxyCancels := make(map[int64]context.CancelFunc)
	activeToolMu := sync.Mutex{}
	activeToolCalls := make(map[int64]activeToolCall)
	var executionWG sync.WaitGroup
	var readyOnce sync.Once
	closeGitBody := func(id int64) {
		gitBodyMu.Lock()
		pw := gitBodyWriters[id]
		delete(gitBodyWriters, id)
		gitBodyMu.Unlock()
		if pw != nil {
			_ = pw.Close()
		}
	}
	defer func() {
		_ = conn.Close()
		cancelConnection()
		gitBodyMu.Lock()
		writers := gitBodyWriters
		gitBodyWriters = make(map[int64]*io.PipeWriter)
		gitBodyMu.Unlock()
		for _, pw := range writers {
			_ = pw.Close()
		}
		proxyBodyMu.Lock()
		proxyWriters := proxyBodyWriters
		proxyBodyWriters = make(map[int64]*io.PipeWriter)
		proxyBodyMu.Unlock()
		for _, pw := range proxyWriters {
			_ = pw.Close()
		}
		executionWG.Wait()
	}()

	// 主读取循环
	for {
		messageType, message, err := readWebSocketMessage(conn, func() {
			_ = conn.SetReadDeadline(time.Now().Add(agentWSReadTimeout))
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
					"capabilities": map[string]interface{}{
						"tools": map[string]interface{}{},
						"protocols": map[string]interface{}{
							"credentialPlan":     credentialPlanCapability(),
							"structuredSSH":      structuredSSHCapability(),
							"executionLifecycle": executionLifecycleCapability(),
						},
					},
				},
			})
			continue
		}
		if req.Method == "gateway/registered" {
			if onReady != nil {
				readyOnce.Do(onReady)
			}
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
			pr, pw := io.Pipe()
			gitBodyMu.Lock()
			gitBodyWriters[id] = pw
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
				gw.handleGitHTTPOverWS(ctx, req.ID, params, pr, writeJSON)
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
			pr, pw := io.Pipe()
			proxyBodyMu.Lock()
			proxyBodyWriters[id] = pw
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
				defer func() {
					proxyBodyMu.Lock()
					delete(proxyBodyWriters, id)
					proxyBodyMu.Unlock()
					_ = pw.Close()
				}()
				gw.handleHTTPProxyOverWS(ctx, req.ID, params, pr, writeJSON)
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
			proxyBodyMu.Lock()
			pw := proxyBodyWriters[params.ID]
			delete(proxyBodyWriters, params.ID)
			proxyBodyMu.Unlock()
			if pw != nil {
				_ = pw.Close()
			}
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
			pw := gitBodyWriters[frame.ID]
			gitBodyMu.Unlock()
			if pw == nil {
				continue
			}
			if frame.DataB64 != "" {
				data, err := base64.StdEncoding.DecodeString(frame.DataB64)
				if err != nil {
					_ = pw.CloseWithError(err)
					closeGitBody(frame.ID)
					continue
				}
				if _, err := pw.Write(data); err != nil {
					closeGitBody(frame.ID)
					continue
				}
			}
			if frame.EOF {
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
			pw := proxyBodyWriters[frame.ID]
			proxyBodyMu.Unlock()
			if pw == nil {
				continue
			}
			if frame.DataB64 != "" {
				data, err := base64.StdEncoding.DecodeString(frame.DataB64)
				if err != nil {
					_ = pw.CloseWithError(err)
					continue
				}
				if _, err := pw.Write(data); err != nil {
					continue
				}
			}
			if frame.EOF {
				proxyBodyMu.Lock()
				delete(proxyBodyWriters, frame.ID)
				proxyBodyMu.Unlock()
				_ = pw.Close()
			}
			continue
		}

		if req.Method == "tools/cancel" {
			var params struct {
				ID int64 `json:"id"`
			}
			_ = json.Unmarshal(req.Params, &params)
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

			// 异步处理实际的 tool 执行
			ctx, cancel := context.WithCancel(connectionCtx)
			if id, ok := numericID(req.ID); ok {
				activeToolMu.Lock()
				activeToolCalls[id] = activeToolCall{
					cancel: cancel,
				}
				activeToolMu.Unlock()
				executionWG.Add(1)
				go func() {
					defer executionWG.Done()
					defer func() {
						activeToolMu.Lock()
						delete(activeToolCalls, id)
						activeToolMu.Unlock()
						cancel()
					}()
					gw.handleToolCallOverWS(ctx, req.ID, params.Name, params.Arguments, writeJSON)
				}()
			} else {
				executionWG.Add(1)
				go func() {
					defer executionWG.Done()
					defer cancel()
					gw.handleToolCallOverWS(ctx, req.ID, params.Name, params.Arguments, writeJSON)
				}()
			}
		}
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

// notificationSender 抽象了发送 notifications/message 的能力。
type notificationSender func(notificationEvent)

type notificationEvent struct {
	Kind   string
	Data   string
	Tool   string
	Status string
	Path   string
}

type activeToolCall struct {
	cancel context.CancelFunc
}

func rawNotification(text string) notificationEvent {
	return notificationEvent{Kind: "raw", Data: text}
}

func assistantDeltaNotification(text string) notificationEvent {
	return notificationEvent{Kind: "assistant_delta", Data: text}
}

func toolNotification(toolName, status string) notificationEvent {
	return notificationEvent{
		Kind:   "tool",
		Data:   fmt.Sprintf("[tool:%s]", toolName),
		Tool:   toolName,
		Status: status,
	}
}

func errorNotification(text string) notificationEvent {
	return notificationEvent{Kind: "error", Data: text}
}

// handleToolCallOverWS 处理具体的 MCP tool 调用（复用原有的处理逻辑，但直接向 WS 写入结果）
func (gw *Gateway) handleToolCallOverWS(ctx context.Context, reqID interface{}, toolName string, argBytes json.RawMessage, writeJSON func(v interface{})) {
	var sessionID string
	var argsMap map[string]interface{}
	json.Unmarshal(argBytes, &argsMap)
	if sid, ok := argsMap["session_id"].(string); ok {
		sessionID = sid
	} else {
		sessionID = "default" // fallback
	}
	if err := validateSession(sessionID); err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
		return
	}

	// 统一流式推送方法
	pushProgress := func(evt notificationEvent) {
		if evt.Kind == "" {
			evt.Kind = "raw"
		}
		params := map[string]interface{}{
			"request_id": reqID,
			"sessionID":  sessionID,
			"data":       evt.Data,
			"kind":       evt.Kind,
		}
		if evt.Tool != "" {
			params["tool"] = evt.Tool
		}
		if evt.Status != "" {
			params["status"] = evt.Status
		}
		if evt.Path != "" {
			params["path"] = evt.Path
		}
		writeJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "notifications/message",
			"params":  params,
		})
	}

	// 工具分发
	switch toolName {
	case "doops_shell":
		var args api.ShellParams
		json.Unmarshal(argBytes, &args)
		if gw.Dispatcher != nil && gw.Dispatcher.Classify(args.Command) == dispatcher.PathBlocked {
			writeJSON(buildErrorResponse(reqID, -32602, "blocked dangerous command"))
			return
		}

		log.Printf("🖥️  WS Exec: [%s] %s", sessionID, args.Command)

		// 放弃过度设计的 PTY Sentinel 模式，换用标准 os/exec Streaming
		execCtx, cancelExec := context.WithTimeout(ctx, maxToolExecutionDuration())
		defer cancelExec()
		cmd, stdoutPipe, stderrPipe, err := executeRawCommand(execCtx, args.Command)
		if err != nil {
			resultText := "Error starting command: " + err.Error()
			writeJSON(buildSuccessResponse(reqID, resultText))
			return
		}

		cmdErr := waitCommandStreams(execCtx, cmd, stdoutPipe, stderrPipe, pushProgress, true)
		if errors.Is(cmdErr, errExecutionOutcomeUnknown) {
			writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
			return
		}
		if execCtx.Err() != nil {
			writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
			return
		}
		writeSessionCommandAudit(sessionID, args.Command, cmdErr)

		if cmdErr != nil {
			finalText := fmt.Sprintf("Command failed with error: %v", cmdErr)
			writeJSON(buildToolErrorResponse(reqID, finalText))
		} else {
			writeJSON(buildSuccessResponse(reqID, "Operation complete."))
		}

	case "doops_ssh":
		var args api.SSHParams
		if err := json.Unmarshal(argBytes, &args); err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, "invalid SSH arguments"))
			return
		}
		log.Printf("🔐 WS SSH: [%s] host=%s options=%d remote_args=%d",
			sessionID, args.Host, len(args.Options), len(args.Command))

		execCtx, cancelExec := context.WithTimeout(ctx, maxToolExecutionDuration())
		defer cancelExec()
		cmd, stdoutPipe, stderrPipe, err := executeStructuredSSHCommand(execCtx, args)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
			return
		}

		cmdErr := waitCommandStreams(execCtx, cmd, stdoutPipe, stderrPipe, pushProgress, true)
		if errors.Is(cmdErr, errExecutionOutcomeUnknown) {
			writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
			return
		}
		if execCtx.Err() != nil {
			writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
			return
		}
		writeSessionCommandAudit(sessionID, sshAuditSummary(args), cmdErr)
		if cmdErr != nil {
			if exitCode, ok := sshExitCode(cmdErr); ok {
				writeJSON(buildStructuredToolErrorResponse(
					reqID,
					fmt.Sprintf("SSH command failed: %v", cmdErr),
					map[string]interface{}{"sshExitCode": exitCode},
				))
			} else {
				writeJSON(buildToolErrorResponse(reqID, fmt.Sprintf("SSH command failed: %v", cmdErr)))
			}
		} else {
			writeJSON(buildSuccessResponse(reqID, "SSH command finished."))
		}

	case "doops_docker":
		var args api.DockerParams
		json.Unmarshal(argBytes, &args)

		if strings.ContainsAny(args.Command, ";|&$`()\r\n") {
			writeJSON(buildErrorResponse(reqID, -32602, "Rejected unsafe characters"))
			return
		}

		fullCmd := gw.containerRuntime + " " + args.Command
		log.Printf("🐳 WS Docker: [%s] %s", sessionID, fullCmd)

		execCtx, cancelExec := context.WithTimeout(ctx, maxToolExecutionDuration())
		defer cancelExec()
		cmd, stdoutPipe, stderrPipe, err := executeRawCommand(execCtx, fullCmd)
		if err != nil {
			writeJSON(buildSuccessResponse(reqID, "Error starting command: "+err.Error()))
			return
		}

		cmdErr := waitCommandStreams(execCtx, cmd, stdoutPipe, stderrPipe, pushProgress, true)
		if errors.Is(cmdErr, errExecutionOutcomeUnknown) {
			writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
			return
		}
		if execCtx.Err() != nil {
			writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
			return
		}

		if cmdErr != nil {
			finalText := fmt.Sprintf("Docker command failed: %v", cmdErr)
			writeJSON(buildToolErrorResponse(reqID, finalText))
		} else {
			writeJSON(buildSuccessResponse(reqID, "Docker command finished."))
		}

	case "doops_node_info":
		var args api.NodeInfoParams
		json.Unmarshal(argBytes, &args)

		cmdStr := strings.Join([]string{
			"echo '--- System Info ---'",
			"uname -a",
			"echo ''",
			"echo '--- Uptime ---'",
			"uptime",
			"echo ''",
			"echo '--- Memory ---'",
			"free -m 2>/dev/null || echo 'free command not found'",
			"echo ''",
			"echo '--- Disk ---'",
			"df -h /",
			"echo ''",
			"echo '--- Capabilities ---'",
			"if command -v kubectl >/dev/null 2>&1; then echo 'kubectl: OK '$(command -v kubectl); else echo 'kubectl: MISSING'; fi",
			"if command -v nerdctl >/dev/null 2>&1; then echo 'container-runtime: nerdctl '$(command -v nerdctl); elif command -v docker >/dev/null 2>&1; then echo 'container-runtime: docker '$(command -v docker); elif command -v podman >/dev/null 2>&1; then echo 'container-runtime: podman '$(command -v podman); else echo 'container-runtime: MISSING'; fi",
			"if command -v buildctl >/dev/null 2>&1; then echo 'buildctl: OK '$(command -v buildctl); else echo 'buildctl: MISSING'; fi",
			"if command -v skopeo >/dev/null 2>&1; then echo 'skopeo: OK '$(command -v skopeo); else echo 'skopeo: MISSING'; fi",
			"if [ -S /run/buildkit/buildkitd.sock ]; then echo 'buildkit-sock: OK /run/buildkit/buildkitd.sock'; else echo 'buildkit-sock: MISSING'; fi",
			"if [ -f /root/.kube/config ]; then echo 'kubeconfig: OK /root/.kube/config'; elif [ -f /etc/rancher/k3s/k3s.yaml ]; then echo 'kubeconfig: OK /etc/rancher/k3s/k3s.yaml'; elif [ -f /etc/kubernetes/admin.conf ]; then echo 'kubeconfig: OK /etc/kubernetes/admin.conf'; else echo 'kubeconfig: MISSING'; fi",
		}, "; ")
		execCtx, cancelExec := context.WithTimeout(ctx, maxToolExecutionDuration())
		defer cancelExec()
		cmd, stdoutPipe, stderrPipe, err := executeRawCommand(execCtx, cmdStr)
		if err != nil {
			resultText := "Error starting command: " + err.Error()
			writeJSON(buildSuccessResponse(reqID, resultText))
			return
		}

		cmdErr := waitCommandStreams(execCtx, cmd, stdoutPipe, stderrPipe, pushProgress, false)
		if errors.Is(cmdErr, errExecutionOutcomeUnknown) {
			writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
			return
		}
		if execCtx.Err() != nil {
			writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
			return
		}

		if cmdErr != nil {
			finalText := fmt.Sprintf("Command failed with error: %v", cmdErr)
			writeJSON(buildToolErrorResponse(reqID, finalText))
		} else {
			writeJSON(buildSuccessResponse(reqID, "Operation complete."))
		}

	case "doops_k8s":
		result, err := handleK8STool(ctx, argBytes, pushProgress)
		if errors.Is(err, errExecutionOutcomeUnknown) {
			writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
		} else if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_web":
		result, err := handleWebTool(ctx, argBytes, pushProgress)
		if errors.Is(err, errExecutionOutcomeUnknown) {
			writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
		} else if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_check_deployment":
		result, err := handleCheckDeployment(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_clean_workspace":
		result, err := handleCleanWorkspace(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_agent_upgrade":
		result, err := handleAgentUpgrade(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_bg":
		var args api.BgExecParams
		json.Unmarshal(argBytes, &args)
		if gw.Dispatcher != nil && gw.Dispatcher.IsHelmMutation(args.Command) {
			writeJSON(buildErrorResponse(reqID, -32602, "background Helm mutation is prohibited; use foreground exec"))
			return
		}
		// 与 doops_shell 一致: 后台任务同样要经过危险命令分类过滤。
		if gw.Dispatcher != nil && gw.Dispatcher.Classify(args.Command) == dispatcher.PathBlocked {
			writeJSON(buildErrorResponse(reqID, -32602, "blocked dangerous command"))
			return
		}
		log.Printf("🚀 WS doops_bg: %s", args.Command)
		if ctx.Err() != nil {
			writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
			return
		}
		task, err := gw.submitBgTask(sessionID, args.Command, args.LogPath)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "Task failed: "+err.Error()))
			return
		}
		defer task.cancel()
		stopCancellation := context.AfterFunc(ctx, task.cancel)
		defer stopCancellation()
		pushProgress(rawNotification(fmt.Sprintf("Task submitted.\nTaskID: %s\nPID: %d\nLog: %s\n", task.TaskID, task.PID, task.LogPath)))
		logReader, logErr := os.Open(task.LogPath)
		if logErr != nil {
			task.cancel()
			<-task.done
		} else {
			defer logReader.Close()
			buffer := make([]byte, 32<<10)
			pendingText := make([]byte, 0, 32<<10)
			publishLog := func(data []byte, final bool) {
				pendingText = append(pendingText, data...)
				complete := len(pendingText)
				if !final {
					complete = 0
					for complete < len(pendingText) && utf8.FullRune(pendingText[complete:]) {
						_, size := utf8.DecodeRune(pendingText[complete:])
						complete += size
					}
				}
				if complete != 0 {
					pushProgress(rawNotification(string(pendingText[:complete])))
					pendingText = append(pendingText[:0], pendingText[complete:]...)
				}
			}
			readLog := func() error {
				for {
					n, err := logReader.Read(buffer)
					if n != 0 {
						publishLog(buffer[:n], false)
					}
					if err == io.EOF {
						return nil
					}
					if err != nil {
						return err
					}
				}
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
		streamLogs:
			for {
				if logErr = readLog(); logErr != nil {
					task.cancel()
					<-task.done
					break
				}
				select {
				case <-ticker.C:
				case <-task.done:
					logErr = readLog()
					break streamLogs
				case <-ctx.Done():
					task.cancel()
					<-task.done
					logErr = readLog()
					break streamLogs
				}
			}
			publishLog(nil, true)
		}
		if errors.Is(task.err, errExecutionOutcomeUnknown) {
			writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
		} else if ctx.Err() != nil {
			writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
		} else if logErr != nil {
			writeJSON(buildToolErrorResponse(reqID, "background_log_unavailable"))
		} else if task.err != nil {
			writeJSON(buildToolErrorResponse(reqID, fmt.Sprintf("Task failed.\nTaskID: %s\nExitCode: %d", task.TaskID, task.ExitCode)))
		} else {
			writeJSON(buildSuccessResponse(reqID, fmt.Sprintf("Task complete.\nTaskID: %s\nExitCode: %d", task.TaskID, task.ExitCode)))
		}

	case "doops_task_status":
		var args api.TaskStatusParams
		json.Unmarshal(argBytes, &args)
		info, err := gw.getTaskStatus(args.TaskID, args.Lines)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
		} else {
			resultText := fmt.Sprintf("TaskID: %s\nPID: %d\nStatus: %s\nExitCode: %d\nLog: %s\n---\n%s",
				info.TaskID, info.PID, info.Status, info.ExitCode, info.LogPath, info.LogTail)
			writeJSON(buildSuccessResponse(reqID, resultText))
		}

	case "doops_agent_prompt":
		var args api.AgentPromptParams
		json.Unmarshal(argBytes, &args)
		operation := strings.ToLower(strings.TrimSpace(args.Operation))
		if operation == "apply" {
			if strings.TrimSpace(args.Instruction) == "" {
				writeJSON(buildErrorResponse(reqID, -32602, "apply requires a non-empty instruction"))
				return
			}
		}
		if len(args.CredentialRefs) != 0 {
			writeJSON(buildErrorResponse(reqID, -32602, "credential_refs must be resolved by the gateway"))
			return
		}
		if len(args.CredentialBindings) != 0 {
			if operation != "" && operation != "ask" && operation != "apply" {
				writeJSON(buildErrorResponse(reqID, -32602, "credential bindings require an ask or apply operation"))
				return
			}
			if err := validatePromptCredentialAccess(operation, args.Skill, true); err != nil {
				writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
				return
			}
			bindingContext, err := credentialBindingContext(args.CredentialBindings)
			if err != nil {
				writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
				return
			}
			args.Instruction += "\n\nGateway-managed credential bindings (metadata only; never read Secret values):\n" + bindingContext
			if hasHelmCredentialBinding(args.CredentialBindings) {
				if operation != "apply" {
					writeJSON(buildErrorResponse(reqID, -32602, "Helm credential bindings require apply"))
					return
				}
				cleanup, err := bindHelmCredentialsForPrompt(sessionID, args.CredentialBindings)
				if err != nil {
					writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
					return
				}
				defer cleanup()
			}
			if hasDatabaseCredentialBinding(args.CredentialBindings) {
				cleanup, err := bindDoSQLCredentialsForPrompt(sessionID, args.CredentialBindings)
				if err != nil {
					writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
					return
				}
				defer cleanup()
			}
		}
		nativeMode, err := doagentModeForPrompt(operation, "")
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
			return
		}
		if args.ResponseFormat != "" && args.ResponseFormat != "json" {
			writeJSON(buildErrorResponse(reqID, -32602, `response_format must be "json" when specified`))
			return
		}
		if args.EventFormat != "" && args.EventFormat != "acp" {
			writeJSON(buildErrorResponse(reqID, -32602, `event_format must be "acp" when specified`))
			return
		}
		if operation == "apply" && len(args.Attachments) > 0 {
			writeJSON(buildErrorResponse(reqID, -32602, "deployment form does not accept conversation attachments"))
			return
		}
		if err := validateAgentPromptAttachments(args.Attachments); err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
			return
		}
		switch strings.ToLower(strings.TrimSpace(args.Mode)) {
		case "metadata":
			if args.ResponseFormat != "" {
				writeJSON(buildErrorResponse(reqID, -32602, "response_format is only supported for agent prompts"))
				return
			}
			result, err := gw.handleAgentMetadataWS(ctx, sessionID)
			if err != nil {
				writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
			} else {
				writeJSON(buildSuccessResponse(reqID, result))
			}
			return
		case "history":
			if args.ResponseFormat != "" {
				writeJSON(buildErrorResponse(reqID, -32602, "response_format is only supported for agent prompts"))
				return
			}
			result, err := gw.handleAgentHistoryWS(ctx, sessionID)
			if err != nil {
				writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
			} else {
				writeJSON(buildSuccessResponse(reqID, result))
			}
			return
		}
		gw.handleAgentPromptWS(
			ctx,
			reqID,
			sessionID,
			args.Instruction,
			args.Attachments,
			args.Model,
			args.ResponseFormat,
			agentPromptExecutionContext{
				NativeMode:          nativeMode,
				RequireToolEvidence: operation == "apply",
				StructuredEvents:    args.EventFormat == "acp",
				Skill:               args.Skill,
			},
			pushProgress,
			writeJSON,
		)

	case "doops_credential_import":
		result, err := handleCredentialSecretImportTool(ctx, argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_credential_materialize":
		result, err := handleCredentialMaterializeTool(ctx, argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_credential_cleanup":
		result, err := handleCredentialCleanupTool(ctx, argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_git_clone":
		result, err := handleGitClone(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_file_write":
		result, err := handleFileWrite(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_file_read":
		result, err := handleFileRead(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_workspace_begin":
		result, err := handleWorkspaceBegin(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_workspace_chunk":
		result, err := handleWorkspaceChunk(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_workspace_commit":
		result, err := handleWorkspaceCommit(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_workspace_pull_begin":
		result, err := handleWorkspacePullBegin(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	case "doops_workspace_pull_chunk":
		result, err := handleWorkspacePullChunk(argBytes)
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}

	default:
		writeJSON(buildErrorResponse(reqID, -32601, "Unknown tool over WS: "+toolName))
	}
}

func credentialBindingContext(bindings []api.AgentCredentialBinding) (string, error) {
	for _, binding := range bindings {
		if !validCredentialMetadataToken(binding.Name, 128) ||
			!validCredentialUse(CredentialUse(binding.Use)) ||
			!validKubernetesCredentialName(binding.Namespace) ||
			!validKubernetesCredentialName(binding.ResourceName) ||
			!validCredentialMetadataToken(binding.ResourceVersion, 256) ||
			credentialScalarContainsInlineMaterial(binding.ResourceVersion) {
			return "", errors.New("credential_binding_metadata_invalid")
		}
		switch binding.SecretType {
		case "", "Opaque", "kubernetes.io/dockerconfigjson", "kubernetes.io/tls":
		default:
			return "", errors.New("credential_binding_metadata_invalid")
		}
		if binding.Digest != "" && !validCredentialSHA256Digest(binding.Digest) {
			return "", errors.New("credential_binding_metadata_invalid")
		}
		if binding.Use == string(CredentialUseDatabase) {
			if len(binding.Keys) == 0 {
				return "", errors.New("credential_binding_metadata_invalid")
			}
		}
		if binding.Use == string(CredentialUseHelmPull) {
			if binding.ContextKind != "helm-native" || !validNativeHelmSocketPath(binding.ContextSocket) || binding.SecretType != "" {
				return "", errors.New("credential_binding_metadata_invalid")
			}
		}
		for _, key := range binding.Keys {
			if !validCredentialMetadataToken(key, 253) || credentialScalarContainsInlineMaterial(key) {
				return "", errors.New("credential_binding_metadata_invalid")
			}
		}
	}
	encoded, err := json.Marshal(bindings)
	if err != nil {
		return "", errors.New("credential_binding_metadata_invalid")
	}
	return string(encoded), nil
}

func hasHelmCredentialBinding(bindings []api.AgentCredentialBinding) bool {
	for _, binding := range bindings {
		if binding.Use == string(CredentialUseHelmPull) || binding.ContextKind == "helm-native" {
			return true
		}
	}
	return false
}

func hasDatabaseCredentialBinding(bindings []api.AgentCredentialBinding) bool {
	for _, binding := range bindings {
		if binding.Use == string(CredentialUseDatabase) {
			return true
		}
	}
	return false
}

func doagentModeForPrompt(operation, executionMode string) (string, error) {
	operation = strings.ToLower(strings.TrimSpace(operation))
	executionMode = strings.ToLower(strings.TrimSpace(executionMode))
	if executionMode != "" {
		return "", fmt.Errorf("execution_mode is not supported for ordinary agent prompts")
	}
	switch operation {
	case "", "ask":
		return "auto", nil
	case "apply":
		return "build", nil
	default:
		return "", fmt.Errorf("unsupported agent prompt operation: %s", operation)
	}
}

type agentPromptExecutionContext struct {
	NativeMode          string
	RequireToolEvidence bool
	StructuredEvents    bool
	Skill               string
}

// handleAgentPromptWS 封装 doops_agent_prompt 处理逻辑，通过 ACP HTTP API 调用本地 doagent 服务。
func (gw *Gateway) handleAgentPromptWS(ctx context.Context, reqID interface{}, doopsSessionID string, instr string, attachments []api.AgentPromptAttachment, model string, responseFormat string, execution agentPromptExecutionContext, pushProgress notificationSender, writeJSON func(v interface{})) {
	ctx, cancelExecution := context.WithTimeout(ctx, 30*time.Minute)
	defer cancelExecution()
	log.Printf("🤖 WS Running doagent via ACP HTTP: session=%s structured=%t model=%s",
		doopsSessionID, responseFormat == "json", model)

	var mutationContext string
	if execution.RequireToolEvidence {
		var err error
		mutationContext, err = mutationContextDigest(doopsSessionID, instr)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, "doagent_apply_mutation_context_invalid"))
			return
		}
	}

	doagentURL := configuredDoagentURL()
	if err := ensureDoagentAvailable(doagentURL); err != nil {
		writeJSON(buildErrorResponse(reqID, -32603, "doagent_unavailable"))
		return
	}
	// Machine-result prompts use a fresh doagent session so their tool trace
	// catalog and turn_finished boundary cannot include a previous turn.
	var entry *sessionEntry
	if responseFormat != "json" {
		gw.sessionMapMu.RLock()
		entry = gw.sessionMap[doopsSessionID]
		gw.sessionMapMu.RUnlock()
	}

	var targetSessionID string
	if entry != nil {
		targetSessionID = entry.doagentSessionID
		gw.sessionMapMu.Lock()
		entry.lastUsed = time.Now()
		gw.sessionMapMu.Unlock()
	}

	// 首次会话：创建 doagent session 并注入系统提示词
	if targetSessionID == "" {
		requestedSessionID := doopsSessionID
		if responseFormat == "json" {
			requestedSessionID = fmt.Sprintf("%s-json-%d", doopsSessionID, time.Now().UnixNano())
		}
		var systemPrompt string
		sysPromptPaths := []string{"/app/skills/system_prompt.md", "/app/self-docs/agent/skills/system_prompt.md"}
		for _, sp := range sysPromptPaths {
			if data, err := os.ReadFile(sp); err == nil {
				systemPrompt = string(data)
				log.Printf("📋 首次会话，已注入系统提示词: %s (%d bytes)", sp, len(data))
				break
			}
		}

		createResp, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      "create-" + doopsSessionID,
			"method":  "session/new",
			"params": map[string]interface{}{
				"sessionId":      requestedSessionID,
				"systemPrompt":   systemPrompt,
				"cwd":            "/root/ws/" + doopsSessionID,
				"permissionMode": "restricted",
			},
		})
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_session_create_failed"))
			return
		}

		if result, ok := createResp["result"].(map[string]interface{}); ok {
			if sid, ok := result["sessionId"].(string); ok {
				targetSessionID = sid
			}
		}
		if targetSessionID == "" {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_session_create_invalid"))
			return
		}

		if responseFormat != "json" {
			gw.sessionMapMu.Lock()
			gw.sessionMap[doopsSessionID] = &sessionEntry{
				doagentSessionID: targetSessionID,
				lastUsed:         time.Now(),
			}
			gw.sessionMapMu.Unlock()
		}
		log.Printf("🔗 Bound Doops Session %s -> doagent Session %s", doopsSessionID, targetSessionID)
	}

	// 仅当调用方显式指定时覆盖模型，并在复用会话时同样生效。
	if model != "" {
		targetModel := model
		if !strings.Contains(targetModel, "/") {
			targetModel = "openai/" + targetModel
		}
		if _, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      "setmodel-" + doopsSessionID,
			"method":  "session/setModel",
			"params": map[string]interface{}{
				"sessionId": targetSessionID,
				"model":     targetModel,
			},
		}); err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_model_configuration_failed"))
			return
		}
	}

	if _, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "setmode-" + doopsSessionID,
		"method":  "session/setMode",
		"params": map[string]interface{}{
			"sessionId": targetSessionID,
			"modeId":    execution.NativeMode,
		},
	}); err != nil {
		writeJSON(buildErrorResponse(reqID, -32603, "doagent_mode_configuration_failed"))
		return
	}
	log.Printf("🧭 Session %s using native doagent mode %s", targetSessionID, execution.NativeMode)

	var promptAttachments []map[string]interface{}
	if len(attachments) > 0 {
		var uploadErr error
		promptAttachments, uploadErr = gw.uploadAgentPromptAttachments(ctx, doagentURL, doopsSessionID, attachments)
		if uploadErr != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_attachment_upload_failed"))
			return
		}
	}

	// 启动 SSE 事件订阅（在 prompt 之前连接，防止丢失事件）
	// Caller cancellation must not close the terminal-evidence subscription.
	sseCtx, sseCancel := context.WithCancel(context.WithoutCancel(ctx))

	sseDone := make(chan struct{})
	var sseErr error
	var terminalSeen atomic.Bool
	sseReady := make(chan error, 1)
	turnIDReady := make(chan string, 1)
	toolTraces := newDoagentToolTraceCollector(false)
	toolTraces.bindSession(doopsSessionID)
	go func() {
		defer close(sseDone)
		sseErr = subscribeDoagentSSEWithCollectorReady(
			sseCtx,
			doagentURL,
			targetSessionID,
			pushProgress,
			func(update map[string]interface{}) error {
				if update["sessionUpdate"] == "turn_finished" {
					switch update["status"] {
					case "completed", "cancelled", "failed", "interrupted":
						terminalSeen.Store(true)
					}
				}
				return toolTraces.collect(update)
			},
			turnIDReady,
			true,
			responseFormat != "json",
			sseReady,
			execution.StructuredEvents,
		)
	}()
	defer func() {
		sseCancel()
		<-sseDone
	}()
	select {
	case err := <-sseReady:
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_sse_unavailable"))
			return
		}
	case <-ctx.Done():
		writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
		return
	}

	// session/prompt 只返回本轮 turnId；终态仍以同 turn 的 SSE turn_finished 为准。
	promptValue := interface{}(instr)
	if len(promptAttachments) > 0 {
		content := make([]interface{}, 0, len(promptAttachments)+1)
		if instr != "" {
			content = append(content, map[string]interface{}{"type": "text", "text": instr})
		}
		for _, attachment := range promptAttachments {
			content = append(content, attachment)
		}
		promptValue = content
	}
	promptParams := map[string]interface{}{"sessionId": targetSessionID, "prompt": promptValue}
	allowedTools := doagentAllowedTools(execution.RequireToolEvidence, execution.Skill)
	promptParams["allowedTools"] = allowedTools
	promptParams["mutationAuthorized"] = execution.RequireToolEvidence
	if execution.RequireToolEvidence {
		authorityID, issuedAt, authorityToken, authorityDigest, authorityErr :=
			newMutationAuthorization(targetSessionID, allowedTools, mutationContext, time.Now())
		if authorityErr != nil {
			sseCancel()
			<-sseDone
			writeJSON(buildErrorResponse(reqID, -32603, "doagent_mutation_authority_unavailable"))
			return
		}
		promptParams["mutationAuthorizationId"] = authorityID
		promptParams["mutationAuthorizationIssuedAt"] = issuedAt
		promptParams["mutationAuthorizationToken"] = authorityToken
		promptParams["mutationContextDigest"] = mutationContext
		toolTraces.bindMutationAuthority(authorityDigest)
	}
	if responseFormat == "json" {
		promptParams["response_format"] = "json"
	}
	promptParams["responseMode"] = "stream"
	if ctx.Err() != nil {
		writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
		return
	}
	promptResponse, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "prompt-" + doopsSessionID,
		"method":  "session/prompt",
		"params":  promptParams,
	})
	if err != nil {
		if doagentPromptRejected(promptResponse) {
			writeJSON(buildErrorResponse(reqID, -32003, "doagent_prompt_rejected"))
			return
		}
		// A transport failure can hide an accepted prompt. Without its turn ID
		// even a cancel ACK cannot prove which execution has stopped.
		_ = cancelDoagentPrompt(ctx, doagentURL, targetSessionID, nil, &terminalSeen, doagentCancellationTimeout)
		writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
		return
	}
	turnID, err := doagentPromptTurnID(promptResponse)
	if err != nil {
		_ = cancelDoagentPrompt(ctx, doagentURL, targetSessionID, nil, &terminalSeen, doagentCancellationTimeout)
		writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
		return
	}
	turnIDReady <- turnID
	close(turnIDReady)

	// 等待本轮权威 turn_finished 终态或错误。对于 apply，已经完成并验签的
	// Helm 运行时结果是变更事实；最终模型轮失败不能把已完成的变更改判为失败。
	select {
	case <-ctx.Done():
		if err := cancelDoagentPrompt(ctx, doagentURL, targetSessionID, sseDone, &terminalSeen, doagentCancellationTimeout); err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
		} else {
			writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
		}
		return
	case <-sseDone:
	}
	if !terminalSeen.Load() {
		_ = cancelDoagentPrompt(ctx, doagentURL, targetSessionID, sseDone, &terminalSeen, doagentCancellationTimeout)
		writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
		return
	}
	if ctx.Err() != nil {
		_ = cancelDoagentPrompt(ctx, doagentURL, targetSessionID, sseDone, &terminalSeen, doagentCancellationTimeout)
		writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
		return
	}
	if execution.RequireToolEvidence && toolTraces.hasUnresolvedMutation() {
		writeJSON(buildErrorResponse(reqID, -32603, errExecutionOutcomeUnknown.Error()))
		return
	}
	verifiedMutation := execution.RequireToolEvidence && len(toolTraces.completed()) != 0
	sseFailed := sseErr != nil && !errors.Is(sseErr, context.Canceled)
	if sseFailed && !verifiedMutation {
		log.Printf("doagent execution failed: session=%s category=doagent_execution_failed detail=%s",
			targetSessionID, sanitizeSecretText(sseErr.Error()))
		pushProgress(errorNotification("[error] " + sanitizeSecretText(sseErr.Error())))
		writeJSON(buildToolErrorResponse(reqID, "doagent_execution_failed"))
		return
	}
	if execution.RequireToolEvidence {
		if err := toolTraces.requireCompletedToolEvidence(); err != nil {
			writeJSON(buildToolErrorResponse(reqID, err.Error()))
			return
		}
		if sseFailed {
			warning := "[warning] Helm mutation was verified; doagent final turn ended with " + sanitizeSecretText(sseErr.Error())
			if execution.StructuredEvents {
				pushProgress(publicDoagentUINotification(map[string]interface{}{
					"sessionUpdate": "doops_notice", "content": map[string]interface{}{"type": "text", "text": warning},
				}))
			} else {
				pushProgress(rawNotification(warning))
			}
		}
	}
	if responseFormat == "json" {
		resultText, structuredContent, err := toolTraces.structuredResult(false)
		if err != nil {
			if verifiedMutation {
				resultText, structuredContent, err = toolTraces.verifiedMutationResult()
			}
		}
		if err != nil {
			writeJSON(buildToolErrorResponse(reqID, fmt.Sprintf("read final machine result: %v", err)))
			return
		}
		writeJSON(buildStructuredSuccessResponse(reqID, resultText, structuredContent))
		return
	}

	writeJSON(buildSuccessResponse(reqID, "Operation complete."))
}

func validateAgentPromptAttachments(attachments []api.AgentPromptAttachment) error {
	if len(attachments) > maxAgentPromptAttachments {
		return fmt.Errorf("一次最多上传 %d 个附件", maxAgentPromptAttachments)
	}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.Filename) == "" || strings.ContainsAny(attachment.Filename, `/\\`) || strings.ContainsAny(attachment.Filename, "\x00\r\n") {
			return fmt.Errorf("附件文件名无效")
		}
		if strings.TrimSpace(attachment.MediaType) == "" || strings.ContainsAny(attachment.MediaType, "\x00\r\n") {
			return fmt.Errorf("附件媒体类型无效")
		}
		decoded, err := base64.StdEncoding.DecodeString(attachment.Base64Data)
		if err != nil {
			return fmt.Errorf("附件编码无效")
		}
		if len(decoded) == 0 {
			return fmt.Errorf("附件不能为空")
		}
		if len(decoded) > maxAgentPromptAttachmentBytes {
			return fmt.Errorf("附件超过 25 MiB 限制")
		}
	}
	return nil
}

func (gw *Gateway) uploadAgentPromptAttachments(ctx context.Context, doagentURL, doopsSessionID string, attachments []api.AgentPromptAttachment) ([]map[string]interface{}, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	if filepath.Base(doopsSessionID) != doopsSessionID || strings.Contains(doopsSessionID, "..") {
		return nil, fmt.Errorf("invalid agent session id")
	}
	workspaceResp, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "workspace-" + doopsSessionID,
		"method":  "workspace/open",
		"params":  map[string]interface{}{"path": filepath.Join("/root/ws", doopsSessionID)},
	})
	if err != nil {
		return nil, fmt.Errorf("open doagent workspace: %w", err)
	}
	workspaceResult, ok := workspaceResp["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("doagent workspace response is invalid")
	}
	workspaceID, _ := workspaceResult["id"].(string)
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("doagent workspace id is missing")
	}

	blocks := make([]map[string]interface{}, 0, len(attachments))
	for index, attachment := range attachments {
		response, uploadErr := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      fmt.Sprintf("attachment-%s-%d", doopsSessionID, index),
			"method":  "attachment/upload",
			"params": map[string]interface{}{
				"workspaceId": workspaceID,
				"filename":    attachment.Filename,
				"mediaType":   attachment.MediaType,
				"base64Data":  attachment.Base64Data,
			},
		})
		if uploadErr != nil {
			return nil, fmt.Errorf("upload attachment %q: %w", attachment.Filename, uploadErr)
		}
		result, ok := response["result"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("attachment upload response is invalid")
		}
		attachmentID, _ := result["attachment_id"].(string)
		if strings.TrimSpace(attachmentID) == "" {
			return nil, fmt.Errorf("attachment upload id is missing")
		}
		blocks = append(blocks, map[string]interface{}{
			"type":          "attachment",
			"attachment_id": attachmentID,
			"workspaceId":   workspaceID,
		})
	}
	return blocks, nil
}

func doagentAllowedTools(apply bool, skill string) []string {
	tools := []string{"Read", "Glob", "Grep", "Skill", "mcp_doops_k8s_read", "mcp_doops_web_read"}
	if strings.TrimSpace(skill) == "dosql" {
		tools = append(tools, "mcp_dosql_read")
	}
	if apply {
		switch skill {
		case "":
			tools = append(tools, "mcp_doops_helm")
		case "doweb":
			tools = append(tools, "mcp_doops_web")
		case "dosql":
			tools = append(tools, "mcp_dosql_execute")
		}
	}
	return tools
}

func (gw *Gateway) handleAgentMetadataWS(ctx context.Context, doopsSessionID string) (string, error) {
	doagentURL := configuredDoagentURL()
	status := inspectDoagentModelChannels()
	if status.status == "unconfigured" && status.settingsFound {
		return marshalDoagentMetadataStatus(status)
	}
	if err := ensureDoagentAvailable(doagentURL); err != nil {
		status.status = "unavailable"
		status.channels = upsertModelChannel(status.channels, agentModelChannelStatus{
			Name:    "acp",
			Status:  "error",
			Message: "doagent_unavailable",
		})
		return marshalDoagentMetadataStatus(status)
	}

	resp, err := doagentRPC(doagentURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "metadata-" + doopsSessionID,
		"method":  "initialize",
		"params":  map[string]interface{}{},
	})
	if err != nil {
		status.status = "unavailable"
		status.channels = upsertModelChannel(status.channels, agentModelChannelStatus{
			Name:    "acp",
			Status:  "error",
			Message: "doagent_initialize_failed",
		})
		return marshalDoagentMetadataStatus(status)
	}

	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		status.status = "unavailable"
		status.channels = upsertModelChannel(status.channels, agentModelChannelStatus{
			Name:    "acp",
			Status:  "error",
			Message: "doagent initialize returned no result",
		})
		return marshalDoagentMetadataStatus(status)
	}
	meta, _ := result["_meta"].(map[string]interface{})
	model, _ := meta["model"].(string)
	tools, _ := meta["tools"].([]interface{})
	if strings.TrimSpace(model) != "" {
		status.model = normalizeProviderModel(status.provider, model)
	}
	status.status = "connected"
	status.channels = upsertModelChannel(status.channels, agentModelChannelStatus{
		Name:    "acp",
		Status:  "ok",
		Message: "doagent ACP initialize ok",
	})
	data, err := json.Marshal(map[string]interface{}{
		"source":         "doagent",
		"event_formats":  []string{"acp"},
		"event_schema":   "doops.sh/acp-events/v1",
		"status":         status.status,
		"provider":       status.provider,
		"model":          status.model,
		"models":         status.models,
		"tools":          tools,
		"channels":       status.channels,
		"channel_status": modelChannelsByName(status.channels),
		"metadata":       meta,
	})
	if err != nil {
		return "", fmt.Errorf("marshal doagent metadata: %w", err)
	}
	return string(data), nil
}

func (gw *Gateway) handleAgentHistoryWS(ctx context.Context, doopsSessionID string) (string, error) {
	_ = ctx
	gw.sessionMapMu.RLock()
	entry := gw.sessionMap[doopsSessionID]
	gw.sessionMapMu.RUnlock()
	if entry == nil || strings.TrimSpace(entry.doagentSessionID) == "" {
		return marshalDoagentHistory(doopsSessionID, nil)
	}

	doagentURL := configuredDoagentURL()
	if err := ensureDoagentAvailable(doagentURL); err != nil {
		return "", errors.New("doagent_unavailable")
	}
	resp, err := doagentRPC(doagentURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "history-" + doopsSessionID,
		"method":  "session/messages",
		"params": map[string]interface{}{
			"sessionId": entry.doagentSessionID,
		},
	})
	if err != nil {
		return "", errors.New("doagent_history_unavailable")
	}
	return marshalDoagentHistory(doopsSessionID, resp["result"])
}

func marshalDoagentHistory(doopsSessionID string, messages interface{}) (string, error) {
	if messages == nil {
		messages = []interface{}{}
	}
	data, err := json.Marshal(map[string]interface{}{
		"source":    "doagent",
		"sessionId": doopsSessionID,
		"messages":  sanitizeDoagentValue(messages),
	})
	if err != nil {
		return "", fmt.Errorf("marshal doagent history: %w", err)
	}
	return string(data), nil
}

type agentModelChannelStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type agentModelRuntimeStatus struct {
	status        string
	source        string
	provider      string
	model         string
	models        []string
	settingsFound bool
	channels      []agentModelChannelStatus
}

func inspectDoagentModelChannels() agentModelRuntimeStatus {
	path := doagentSettingsPath()
	status := agentModelRuntimeStatus{
		status: "unconfigured",
		source: "doagent-settings",
		channels: []agentModelChannelStatus{
			{Name: "settings", Status: "error", Message: "settings.json not found"},
			{Name: "api_key", Status: "error", Message: "API key is missing"},
			{Name: "acp", Status: "unknown", Message: "not checked yet"},
		},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return status
	}
	status.settingsFound = true
	status.channels[0] = agentModelChannelStatus{Name: "settings", Status: "ok", Message: path}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		status.channels[0] = agentModelChannelStatus{Name: "settings", Status: "error", Message: "settings.json is not valid JSON"}
		return status
	}

	status.model, _ = settings["model"].(string)
	providers, _ := settings["provider"].(map[string]interface{})
	if len(providers) > 0 {
		status.provider = providerForSettingsModel(providers, status.model)
		if status.provider == "" {
			status.provider = firstSettingsProvider(providers)
		}
		status.models = modelsForSettingsProvider(providers, status.provider)
	}
	if len(status.models) == 0 && strings.TrimSpace(status.model) != "" {
		status.models = []string{status.model}
	}
	if strings.TrimSpace(status.model) == "" || strings.TrimSpace(status.provider) == "" {
		status.channels[1] = agentModelChannelStatus{Name: "api_key", Status: "error", Message: "model or provider is missing"}
		return status
	}

	apiKey, baseURL := providerCredentials(providers, status.provider)
	switch {
	case strings.TrimSpace(apiKey) == "":
		status.channels[1] = agentModelChannelStatus{Name: "api_key", Status: "error", Message: "API key is empty"}
	case isPlaceholderAPIKey(apiKey):
		status.channels[1] = agentModelChannelStatus{Name: "api_key", Status: "error", Message: "API key is still a placeholder"}
	default:
		msg := "API key configured"
		if strings.TrimSpace(baseURL) != "" {
			msg = "API key configured; baseURL=" + strings.TrimSpace(baseURL)
		}
		status.channels[1] = agentModelChannelStatus{Name: "api_key", Status: "ok", Message: msg}
		status.status = "configured"
	}
	return status
}

func marshalDoagentMetadataStatus(status agentModelRuntimeStatus) (string, error) {
	data, err := json.Marshal(map[string]interface{}{
		"source":         status.source,
		"status":         status.status,
		"provider":       status.provider,
		"model":          status.model,
		"models":         status.models,
		"channels":       status.channels,
		"channel_status": modelChannelsByName(status.channels),
		"metadata": map[string]interface{}{
			"model":    status.model,
			"provider": status.provider,
			"models":   status.models,
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal doagent metadata status: %w", err)
	}
	return string(data), nil
}

func modelChannelsByName(channels []agentModelChannelStatus) map[string]agentModelChannelStatus {
	out := make(map[string]agentModelChannelStatus, len(channels))
	for _, ch := range channels {
		if ch.Name != "" {
			out[ch.Name] = ch
		}
	}
	return out
}

func upsertModelChannel(channels []agentModelChannelStatus, next agentModelChannelStatus) []agentModelChannelStatus {
	for i := range channels {
		if channels[i].Name == next.Name {
			channels[i] = next
			return channels
		}
	}
	return append(channels, next)
}

func providerForSettingsModel(providers map[string]interface{}, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	for name, raw := range providers {
		cfg, _ := raw.(map[string]interface{})
		models, _ := cfg["models"].(map[string]interface{})
		if _, ok := models[model]; ok {
			return name
		}
		if _, ok := models[providerModelKey(name, model)]; ok {
			return name
		}
	}
	if strings.Contains(model, "/") {
		name := strings.SplitN(model, "/", 2)[0]
		if _, ok := providers[name]; ok {
			return name
		}
	}
	return ""
}

func firstSettingsProvider(providers map[string]interface{}) string {
	for name := range providers {
		return name
	}
	return ""
}

func modelsForSettingsProvider(providers map[string]interface{}, provider string) []string {
	cfg, _ := providers[provider].(map[string]interface{})
	modelsMap, _ := cfg["models"].(map[string]interface{})
	models := make([]string, 0, len(modelsMap))
	for model := range modelsMap {
		if strings.Contains(model, "/") {
			models = append(models, model)
		} else {
			models = append(models, provider+"/"+model)
		}
	}
	return models
}

func providerCredentials(providers map[string]interface{}, provider string) (string, string) {
	cfg, _ := providers[provider].(map[string]interface{})
	options, _ := cfg["options"].(map[string]interface{})
	apiKey, _ := options["apiKey"].(string)
	baseURL, _ := options["baseURL"].(string)
	if apiKey == "" {
		apiKey, _ = options["api_key"].(string)
	}
	return apiKey, baseURL
}

func providerModelKey(provider, model string) string {
	if strings.HasPrefix(model, provider+"/") {
		return strings.TrimPrefix(model, provider+"/")
	}
	return provider + "/" + model
}

func normalizeProviderModel(provider, model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.Contains(model, "/") || strings.TrimSpace(provider) == "" {
		return model
	}
	return strings.TrimSpace(provider) + "/" + model
}

func isPlaceholderAPIKey(apiKey string) bool {
	apiKey = strings.TrimSpace(strings.ToLower(apiKey))
	return apiKey == "" || strings.Contains(apiKey, "your_") || strings.Contains(apiKey, "replace") || strings.Contains(apiKey, "placeholder")
}

func doagentSettingsPath() string {
	if path := strings.TrimSpace(os.Getenv("DO_AGENT_SETTINGS")); path != "" {
		return path
	}
	return "/root/.agent/settings.json"
}

func configuredDoagentURL() string {
	if baseURL := strings.TrimSpace(os.Getenv("DO_AGENT_URL")); baseURL != "" {
		return baseURL
	}
	port := strings.TrimSpace(os.Getenv("DO_AGENT_PORT"))
	if port == "" {
		port = "9000"
	}
	return "http://127.0.0.1:" + port
}

func ensureDoagentAvailable(baseURL string) error {
	if doagentTCPReady(baseURL, 500*time.Millisecond) {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid DO_AGENT_URL %q: %w", baseURL, err)
	}
	host := parsed.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "" {
		return fmt.Errorf("%s is not reachable", baseURL)
	}
	port := parsed.Port()
	if port == "" {
		port = "9000"
	}
	bin, err := exec.LookPath("do-agent")
	if err != nil {
		bin = "/usr/local/bin/do-agent"
		if _, statErr := os.Stat(bin); statErr != nil {
			return fmt.Errorf("do-agent binary not found")
		}
	}
	if err := os.MkdirAll("/root/ws", 0755); err != nil {
		return fmt.Errorf("create /root/ws: %w", err)
	}
	cmd := exec.Command(bin, "acp-http", "--port", port, "--cwd", "/root/ws")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start do-agent acp-http: %w", err)
	}
	log.Printf("🤖 started doagent ACP HTTP on demand: pid=%d port=%s", cmd.Process.Pid, port)
	for i := 0; i < 20; i++ {
		if doagentTCPReady(baseURL, 500*time.Millisecond) {
			return nil
		}
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			return errors.New("do-agent exited before listening")
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("do-agent did not listen after startup")
}

func doagentTCPReady(baseURL string, timeout time.Duration) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "9000"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func tailFile(path string, max int) string {
	data, err := os.ReadFile(path)
	if err != nil || max <= 0 {
		return ""
	}
	if len(data) <= max {
		return string(data)
	}
	return string(data[len(data)-max:])
}

// doagentRPC 向本地 doagent ACP HTTP 服务发送 JSON-RPC 请求并返回响应。
func doagentRPC(baseURL string, payload map[string]interface{}) (map[string]interface{}, error) {
	return doagentRPCContext(context.Background(), baseURL, payload)
}

func doagentRPCContext(ctx context.Context, baseURL string, payload map[string]interface{}) (map[string]interface{}, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/rpc", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /rpc: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response (status=%d): %w", resp.StatusCode, err)
	}

	if rpcErr, ok := result["error"]; ok && rpcErr != nil {
		return result, fmt.Errorf("RPC error: %v", rpcErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RPC HTTP status %d", resp.StatusCode)
	}

	return result, nil
}

const doagentCancellationTimeout = 10 * time.Second

func cancelDoagentPrompt(ctx context.Context, baseURL, sessionID string, done <-chan struct{}, terminalSeen *atomic.Bool, timeout time.Duration) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	// session/cancel acknowledges a request, not termination. Leave SSE open
	// until the matching turn_finished, even if the ACK says not_running.
	_, _ = doagentRPCContext(cleanupCtx, baseURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "cancel-" + sessionID,
		"method":  "session/cancel",
		"params":  map[string]interface{}{"sessionId": sessionID},
	})
	if done != nil {
		select {
		case <-done:
		case <-cleanupCtx.Done():
		}
	}
	if terminalSeen.Load() {
		return nil
	}
	return errExecutionOutcomeUnknown
}

func doagentPromptRejected(response map[string]interface{}) bool {
	if response["result"] != nil {
		return false
	}
	rpcErr, _ := response["error"].(map[string]interface{})
	data, _ := rpcErr["data"].(map[string]interface{})
	if data["failureClass"] == "admission_timeout" {
		return false
	}
	// These errors are emitted before admission. Internal/timeout failures
	// are deliberately not treated as proof that a prompt never started.
	switch rpcErr["code"] {
	case float64(-32602), float64(-32001), float64(-32003):
		return true
	default:
		return false
	}
}

func doagentPromptTurnID(response map[string]interface{}) (string, error) {
	result, ok := response["result"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("result must be an object")
	}
	status, _ := result["status"].(string)
	if status != "accepted" && status != "queued" {
		return "", fmt.Errorf("result.status must be accepted or queued")
	}
	turnID, _ := result["turnId"].(string)
	if strings.TrimSpace(turnID) == "" {
		return "", fmt.Errorf("result.turnId is required")
	}
	return turnID, nil
}

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

	client := &http.Client{}
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
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var eventData strings.Builder
	var pendingAgentMessage strings.Builder
	targetTurnID := ""
	structuredEvents := len(structuredFormats) == 1 && structuredFormats[0]
	flushPendingAgentMessage := func() {
		if pendingAgentMessage.Len() == 0 {
			return
		}
		text := sanitizeSecretText(pendingAgentMessage.String())
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
	receivedEvent := false
	idleTimeout := doagentSSEIdleTimeout()
	idleTimer := time.NewTimer(idleTimeout)
	idleDone := make(chan struct{})
	var idleMu sync.Mutex
	var idleErr error
	defer func() {
		close(idleDone)
		idleTimer.Stop()
	}()
	resetIdle := func() {
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(idleTimeout)
	}
	go func() {
		select {
		case <-idleTimer.C:
			err := fmt.Errorf("doagent SSE idle timeout after %s", idleTimeout)
			idleMu.Lock()
			idleErr = err
			idleMu.Unlock()
			cancel()
		case <-idleDone:
		}
	}()

	for scanner.Scan() {
		line := scanner.Text()

		// SSE 格式: "event: type", "data: {json}", 空行分隔
		if strings.HasPrefix(line, "event: ") {
			continue // 事件类型从 data 的 type 字段判断
		}

		if strings.HasPrefix(line, "data: ") {
			eventData.WriteString(strings.TrimPrefix(line, "data: "))
			continue
		}

		// 空行 = 一个完整事件结束
		if line == "" && eventData.Len() > 0 {
			data := eventData.String()
			eventData.Reset()
			receivedEvent = true
			resetIdle()

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
					flushPendingAgentMessage()
				}
				if updateType == "turn_finished" && pendingAgentMessage.Len() > 0 {
					flushPendingAgentMessage()
				}
				if collector != nil {
					if err := collector(sessionUpdate); err != nil {
						return err
					}
				}
				if structuredEvents {
					pushProgress(publicDoagentUINotification(sessionUpdate))
					if updateType == "turn_finished" {
						return doagentTurnFinishedError(sessionUpdate)
					}
					continue
				}
				sessionUpdate = sanitizeDoagentSessionUpdate(sessionUpdate)
				switch updateType {
				case "tool_call_update":
					// 工具调用进度
					toolName, _ := sessionUpdate["toolName"].(string)
					status, _ := sessionUpdate["status"].(string)
					if toolName != "" && status == "in_progress" {
						pushProgress(toolNotification(toolName, status))
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
				case "turn_finished":
					if err := doagentTurnFinishedError(sessionUpdate); err != nil {
						return err
					}
					return nil
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
		idleMu.Lock()
		timeoutErr := idleErr
		idleMu.Unlock()
		if timeoutErr != nil && errors.Is(err, context.Canceled) {
			return timeoutErr
		}
		return fmt.Errorf("SSE read: %w", err)
	}
	idleMu.Lock()
	timeoutErr := idleErr
	idleMu.Unlock()
	if timeoutErr != nil {
		return timeoutErr
	}
	if receivedEvent {
		return fmt.Errorf("doagent SSE ended before final event")
	}
	return fmt.Errorf("doagent SSE ended without events")
}

func doagentTurnFinishedError(update map[string]interface{}) error {
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

func doagentSSEIdleTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("DOOPS_DOAGENT_SSE_IDLE_TIMEOUT"))
	if raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	// Idle = no SSE event from the agent-core for this long. The timer resets on
	// every streamed event, so this only fires during a genuine silent gap.
	// Agent-driven work (a reasoning step plus a long tool call: build, image
	// pull, kubectl wait) routinely stays silent well beyond 30s, so a short
	// default aborts healthy long-running stages (notably agent-driven CI/CD).
	// Default to a generous 5m; set DOOPS_DOAGENT_SSE_IDLE_TIMEOUT higher for
	// heavy build stages, or lower for snappy hang-detection in interactive use.
	return 5 * time.Minute
}

func sanitizeSecretText(s string) string {
	if s == "" {
		return ""
	}
	replacers := []*regexp.Regexp{
		regexp.MustCompile(`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`),
		regexp.MustCompile(`(?i)([?&](?:[A-Za-z0-9._-]*(?:token|password|secret|signature|credential|api[_-]?key)|sig|auth|authorization|cookie)=)[^&#\s]+`),
		regexp.MustCompile(`sk-[A-Za-z0-9_-]+`),
		regexp.MustCompile(`(?i)(api[_-]?key[=: ]+)[^ ,}\n]+`),
		regexp.MustCompile(`(?i)(Bearer )[A-Za-z0-9._-]+`),
		regexp.MustCompile(`(?i)(Basic )[A-Za-z0-9+/=._-]+`),
		regexp.MustCompile(`(?i)((?:authorization|cookie|password|token|secret)[_-]?[A-Za-z0-9_-]*[=: ]+)[^ ,}\n]+`),
	}
	s = replacers[0].ReplaceAllString(s, "...REDACTED PRIVATE KEY...")
	for _, replacer := range replacers[1:] {
		s = replacer.ReplaceAllString(s, "${1}...REDACTED")
	}
	for _, pattern := range credentialInlineMaterialPatterns {
		s = pattern.ReplaceAllString(s, "...REDACTED")
	}
	if len(s) > 1200 {
		return s[len(s)-1200:]
	}
	return s
}

func sanitizeDoagentValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case string:
		return sanitizeSecretText(typed)
	case []interface{}:
		sanitized := make([]interface{}, len(typed))
		for index, item := range typed {
			sanitized[index] = sanitizeDoagentValue(item)
		}
		return sanitized
	case map[string]interface{}:
		sanitized := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			if credentialSecretLikeKey(key) || strings.EqualFold(strings.TrimSpace(key), "passwd") {
				sanitized[key] = "...REDACTED"
			} else {
				sanitized[key] = sanitizeDoagentValue(item)
			}
		}
		return sanitized
	default:
		return value
	}
}

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
		sanitized["content"] = sanitizeDoagentValue(update["content"])
	case "error", "permission_request", "turn_finished":
		for _, key := range []string{"error", "failureReason", "message", "reason"} {
			if value, ok := update[key].(string); ok {
				sanitized[key] = sanitizeSecretText(value)
			}
		}
	}
	return sanitized
}

// -----------------------------------------------------------------------------
// 共享的底层基础设施流式处理器
// -----------------------------------------------------------------------------

// executeRawCommand 使用纯标准的 os/exec 启动进程（去 PTY 化），返回 cmd 对象、标准输出管、错误输出管
func executeRawCommand(ctx context.Context, command string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	if ctx.Err() != nil {
		return nil, nil, nil, ctx.Err()
	}
	return startRawCommand(ctx, exec.Command("bash", "-c", command))
}

func startRawCommand(ctx context.Context, cmd *exec.Cmd) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	if ctx.Err() != nil {
		return nil, nil, nil, ctx.Err()
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = commandPipeGracePeriod

	if cmd.Stdout != nil || cmd.Stderr != nil {
		return nil, nil, nil, errors.New("command output already set")
	}
	stdoutPipe, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	defer stdoutWriter.Close()
	stderrPipe, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutPipe.Close()
		return nil, nil, nil, err
	}
	defer stderrWriter.Close()
	// Wait must not own the read ends; fast commands can exit before readers run.
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return nil, nil, nil, err
	}
	return cmd, stdoutPipe, stderrPipe, nil
}

// Wait for both the command and its process group. Cancellation escalation is
// joined here, never left running after a terminal RPC response.
const postLeaderDescendantGracePeriod = 10 * time.Second
const commandPipeGracePeriod = time.Second

func waitProcessGroup(ctx context.Context, pid int, commandDone <-chan error) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	ctxDone := ctx.Done()
	var killTimer, cleanupTimer, postLeaderTimer *time.Timer
	var killDeadline, cleanupDeadline, postLeaderDeadline <-chan time.Time
	forcedCleanup := false
	defer func() {
		if killTimer != nil {
			killTimer.Stop()
		}
		if cleanupTimer != nil {
			cleanupTimer.Stop()
		}
		if postLeaderTimer != nil {
			postLeaderTimer.Stop()
		}
	}()
	var commandErr error
	for {
		if commandDone == nil {
			// Only reap adopted descendants after exec.Cmd.Wait owns the leader's status.
			for {
				var status syscall.WaitStatus
				reaped, _ := syscall.Wait4(-pid, &status, syscall.WNOHANG, nil)
				if reaped <= 0 {
					break
				}
			}
			err := syscall.Kill(-pid, 0)
			if errors.Is(err, syscall.ESRCH) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if forcedCleanup {
					return fmt.Errorf("%w: detached process group was terminated", errExecutionOutcomeUnknown)
				}
				return commandErr
			}
			// A canceled group remains owned until absence is observed or the
			// cleanup deadline expires, including a temporarily denied status probe.
			if err != nil && !(ctx.Err() != nil && errors.Is(err, syscall.EPERM)) {
				return fmt.Errorf("%w: process group status: %v", errExecutionOutcomeUnknown, err)
			}
		}
		select {
		case commandErr = <-commandDone:
			commandDone = nil
			if ctx.Err() == nil {
				postLeaderTimer = time.NewTimer(postLeaderDescendantGracePeriod)
				postLeaderDeadline = postLeaderTimer.C
			}
		case <-ctxDone:
			ctxDone = nil
			if postLeaderTimer != nil {
				postLeaderTimer.Stop()
				postLeaderDeadline = nil
			}
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			killTimer = time.NewTimer(2 * time.Second)
			cleanupTimer = time.NewTimer(10 * time.Second)
			killDeadline = killTimer.C
			cleanupDeadline = cleanupTimer.C
		case <-killDeadline:
			killDeadline = nil
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		case <-cleanupDeadline:
			return fmt.Errorf("%w: process group did not terminate", errExecutionOutcomeUnknown)
		case <-postLeaderDeadline:
			postLeaderDeadline = nil
			forcedCleanup = true
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			killTimer = time.NewTimer(2 * time.Second)
			cleanupTimer = time.NewTimer(10 * time.Second)
			killDeadline = killTimer.C
			cleanupDeadline = cleanupTimer.C
		case <-ticker.C:
		}
	}
}

// waitCommandStreams owns the read ends independently of Cmd.Wait. Bound the
// final drain because writers can escape the process-group watchdog.
func waitCommandStreams(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.ReadCloser, push notificationSender, heartbeat bool) error {
	streamDone := make(chan error, 2)
	readStream := func(pipe io.ReadCloser) {
		scanner := bufio.NewScanner(pipe)
		defer func() {
			_ = pipe.Close()
			streamDone <- scanner.Err()
		}()
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			push(rawNotification(scanner.Text() + "\n"))
		}
	}
	go readStream(stdout)
	go readStream(stderr)

	var done, heartbeatDone chan struct{}
	if heartbeat {
		done = make(chan struct{})
		heartbeatDone = make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			slowProgressHeartbeat(done, push)
		}()
	}
	commandDone := make(chan error, 1)
	go func() { commandDone <- cmd.Wait() }()
	cmdErr := waitProcessGroup(ctx, cmd.Process.Pid, commandDone)
	if errors.Is(cmdErr, errExecutionOutcomeUnknown) {
		_ = stdout.Close()
		_ = stderr.Close()
	}
	drainTimer := time.NewTimer(commandPipeGracePeriod)
	defer drainTimer.Stop()
	drainDeadline := drainTimer.C
	for remaining := 2; remaining > 0; {
		select {
		case readErr := <-streamDone:
			remaining--
			if readErr != nil && !errors.Is(cmdErr, errExecutionOutcomeUnknown) {
				cmdErr = fmt.Errorf("%w: command output read: %w", errExecutionOutcomeUnknown, readErr)
			}
		case <-drainDeadline:
			drainDeadline = nil
			_ = stdout.Close()
			_ = stderr.Close()
			cmdErr = fmt.Errorf("%w: command output drain timed out", errExecutionOutcomeUnknown)
		}
	}
	if done != nil {
		close(done)
		<-heartbeatDone
	}
	return cmdErr
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func sshExitCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	code := exitErr.ExitCode()
	return code, code >= 0 && code <= 255
}

func writeSessionCommandAudit(sessionID, summary string, cmdErr error) {
	if sessionID == "" {
		return
	}
	entry := fmt.Sprintf("# [%s] exit=%d\n%s\n", time.Now().Format("2006-01-02 15:04:05"), commandExitCode(cmdErr), summary)
	if logFile, err := appendSessionAuditLog(sessionID, entry); err == nil {
		log.Printf("✅ Audit Log saved to %s", logFile)
	} else {
		log.Printf("❌ Failed to save Audit Log for session %s: %v", sessionID, err)
	}
}

// streamReader 逐行读取 pipe 内容并推送到 WS 客户端
func streamReader(pipe io.ReadCloser, pushProgress notificationSender) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 允许长行
	for scanner.Scan() {
		line := scanner.Text() + "\n"
		pushProgress(rawNotification(line))
	}
}

// slowProgressHeartbeat 定期发送正在执行心跳标签
func slowProgressHeartbeat(doneCh chan struct{}, pushProgress notificationSender) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	elapsed := 0
	for {
		select {
		case <-doneCh:
			return
		case <-ticker.C:
			elapsed += 5
			pushProgress(rawNotification(fmt.Sprintf("\r\033[K[agent] ⏳ 命令后台执行中... (耗时 %ds)", elapsed)))
		}
	}
}

// buildSuccessResponse 构建标准的 MCP ToolCall 成功响应包
func buildSuccessResponse(reqID interface{}, text string) api.JSONRPCResponse {
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      reqID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": text,
				},
			},
		},
	}
}

func buildStructuredSuccessResponse(reqID interface{}, text string, structuredContent map[string]interface{}) api.JSONRPCResponse {
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      reqID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": text,
				},
			},
			"structuredContent": structuredContent,
		},
	}
}

// buildErrorResponse 构建标准的 MCP ToolCall 异常响应包
func buildErrorResponse(reqID interface{}, code int, message string) api.JSONRPCResponse {
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      reqID,
		Error:   &api.RPCError{Code: code, Message: message},
	}
}

// buildToolErrorResponse 构建工具执行逻辑失败的响应包（退出码非 0）
func buildToolErrorResponse(reqID interface{}, text string) api.JSONRPCResponse {
	return buildStructuredToolErrorResponse(reqID, text, nil)
}

func buildStructuredToolErrorResponse(reqID interface{}, text string, fields map[string]interface{}) api.JSONRPCResponse {
	result := map[string]interface{}{
		"isError": true,
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": text,
			},
		},
	}
	for key, value := range fields {
		result[key] = value
	}
	return api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      reqID,
		Result:  result,
	}
}
