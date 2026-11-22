package main

import (
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	websocketHandshakeAttempts   = 3
	websocketHandshakeRetryDelay = 50 * time.Millisecond
)

// connect 建立与 agent 的持久 WebSocket 连接并完成 MCP initialize 握手。
func (c *MCPClient) connect() error {
	return c.connectWithVerification("", "")
}

func (c *MCPClient) connectWithVerification(expectedRelease, requiredProtocol string) (err error) {
	defer func() {
		if c.UpgradeOperationID != "" && err != nil {
			var rpcErr *gatewayRPCError
			if errors.As(err, &rpcErr) {
				data, _ := redactUpgradeValue(rpcErr.Data, c.Token).(map[string]interface{})
				err = fmt.Errorf("initialize failed: %w", &gatewayRPCError{
					Code: rpcErr.Code, Message: redactUpgradeText(rpcErr.Message, c.Token), Data: data,
				})
			} else {
				err = errors.New(redactUpgradeText(err.Error(), c.Token))
			}
		}
	}()
	c.mu.Lock()
	if c.connected {
		if err := c.cosSnapshotGatewayErrorLocked(); err != nil {
			c.closeConnectionLocked(c.conn)
			c.mu.Unlock()
			return err
		}
		c.mu.Unlock()
		if c.UpgradeOperationID != "" && (expectedRelease != "" || requiredProtocol != "") {
			return c.verifyConnectedUpgrade(expectedRelease, requiredProtocol)
		}
		return nil
	}
	defer c.mu.Unlock()

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

	initializeDeadline := time.Now().Add(5 * time.Second)
	if err := conn.SetWriteDeadline(initializeDeadline); err != nil {
		conn.Close()
		return fmt.Errorf("initialization failed: %v", err)
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"id":      initID,
		"params":  c.initializeParams(expectedRelease, requiredProtocol),
	}); err != nil {
		c.conn.Close()
		return fmt.Errorf("initialization failed: %v", err)
	}

	// 启动后台事件分发循环
	go c.dispatchLoop(conn)
	c.dispatching = true

	// 启动客户端级 Ping 心跳，防范 NAT/LB 针对单向长连（如长时间无包）的静默超时。
	// 大多数云厂商 LB idle timeout 是 60~120s。
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			c.mu.Lock()
			active := c.connected && c.conn == conn
			c.mu.Unlock()
			if !active {
				return
			}
			err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			if err != nil {
				c.closeConnection(conn)
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
	case <-time.After(time.Until(initializeDeadline)):
		c.conn.Close()
		return fmt.Errorf("initialize handshake timed out")
	}

	if err := c.cosSnapshotGatewayErrorLocked(); err != nil {
		c.closeConnectionLocked(conn)
		return err
	}
	c.connected = true
	c.log("CLIENT:CONNECTED", "Persistent WebSocket connection established")
	return nil
}

func (c *MCPClient) initializeParams(expectedRelease, requiredProtocol string) map[string]interface{} {
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]string{"name": "doops-go-cli", "version": "2.0"},
	}
	if c.UpgradeOperationID != "" {
		params["upgrade_operation_id"] = c.UpgradeOperationID
		params["session_id"] = c.SessionName
		if expectedRelease != "" {
			params["expected_release_commit"] = expectedRelease
		}
		if requiredProtocol != "" {
			params["required_protocol"] = requiredProtocol
		}
	}
	return params
}

// A cached capability check cannot produce a Gateway verification receipt.
// Reused upgrade clients send a fresh initialize without replaying tool calls.
func (c *MCPClient) verifyConnectedUpgrade(expectedRelease, requiredProtocol string) error {
	id := c.nextReqID()
	conn, ch := c.registerCall(id)
	defer c.unregisterPending(id)
	deadline := time.Now().Add(5 * time.Second)
	if err := c.writeRequest(conn, map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": "initialize",
		"params": c.initializeParams(expectedRelease, requiredProtocol),
	}, deadline); err != nil {
		return err
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case evt := <-ch:
		if err := validateInitializeResponse(id, evt); err != nil {
			return err
		}
		c.mu.Lock()
		c.capabilities = initializeCapabilities(evt)
		c.mu.Unlock()
		return nil
	case <-timer.C:
		return fmt.Errorf("initialize verification timed out")
	}
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
		code, _ := rpcErr["code"].(float64)
		data, _ := rpcErr["data"].(map[string]interface{})
		return fmt.Errorf("initialize failed: %w", &gatewayRPCError{Code: code, Message: fmt.Sprint(rpcErr["message"]), Data: data})
	}
	result, ok := msg["result"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("initialize failed: missing result")
	}
	if toolResultIsError(result) {
		return fmt.Errorf("initialize failed: %s", toolResultText(result, "verification failed"))
	}
	return nil
}

func initializeCapabilities(evt wsEvent) map[string]interface{} {
	result, _ := evt.Parsed["result"].(map[string]interface{})
	capabilities, _ := result["capabilities"].(map[string]interface{})
	return capabilities
}

func (c *MCPClient) RequireProtocol(protocol string) error {
	if err := validateRequiredProtocol(protocol); err != nil {
		return err
	}
	if err := c.connectWithVerification("", protocol); err != nil {
		return err
	}
	if c.capabilities == nil {
		return fmt.Errorf("initialize verification failed: missing capabilities")
	}
	protocols, validProtocols := c.capabilities["protocols"].(map[string]interface{})
	if _, present := c.capabilities["protocols"]; present && !validProtocols {
		return fmt.Errorf("initialize verification failed: malformed protocols")
	}
	if !protocolCapabilityCompatible(protocol, protocols[protocol]) {
		return &upgradeVerificationError{category: "upgrade_verification_protocol_missing", error: fmt.Errorf(
			"target Agent does not advertise protocol %q; upgrade the Agent before using this operation",
			protocol,
		)}
	}
	return nil
}

func (c *MCPClient) RequireAgentRelease(expected string) error {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return fmt.Errorf("expected DoOps release commit is empty")
	}
	if err := c.connectWithVerification(expected, ""); err != nil {
		return err
	}
	if c.capabilities == nil {
		return fmt.Errorf("initialize verification failed: missing capabilities")
	}
	actual, validCommit := c.capabilities["releaseCommit"].(string)
	if _, present := c.capabilities["releaseCommit"]; present && !validCommit {
		return fmt.Errorf("initialize verification failed: malformed release commit")
	}
	actual = strings.TrimSpace(actual)
	if actual == "" {
		return &upgradeVerificationError{category: "upgrade_verification_commit_mismatch", error: fmt.Errorf("target Agent did not advertise a DoOps release commit")}
	}
	if actual != expected {
		// The CLI and the Agent are released as a pair. The Agent reports the
		// commit it was built from, so a difference means this CLI and that
		// Agent are not the same release, not that the Agent is broken.
		return &upgradeVerificationError{category: "upgrade_verification_commit_mismatch", error: fmt.Errorf(
			"target Agent release commit mismatch: got %s, want %s; "+
				"the CLI and the Agent are released as a pair, so install the CLI "+
				"that matches the target Agent before upgrading it",
			actual, expected,
		)}
	}
	return nil
}

func validateRequiredProtocol(protocol string) error {
	if protocol != "structuredSSH" && protocol != "executionLifecycle" {
		return fmt.Errorf("unsupported protocol %q; supported protocols: structuredSSH, executionLifecycle", protocol)
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
	case "executionLifecycle":
		capability, ok := value.(map[string]interface{})
		return ok &&
			len(capability) == 4 &&
			capability["schemaVersion"] == "doops.sh/execution-lifecycle/v1" &&
			capability["terminalResponse"] == true &&
			capability["backgroundLifetime"] == true &&
			capability["promptCancellation"] == true
	default:
		return false
	}
}
