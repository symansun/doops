package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/pkg/release"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	errGatewayClientDisconnected = errors.New("gateway client disconnected")
)

func (h *ClientService) HandleClientRPC(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	if cluster == "" {
		http.Error(w, "missing cluster", http.StatusBadRequest)
		return
	}
	if instance == "" {
		http.Error(w, "missing instance", http.StatusBadRequest)
		return
	}
	if !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[gateway] client upgrade failed: %v", err)
		return
	}
	h.trackClientConn(conn)
	defer func() {
		h.untrackClientConn(conn)
		_ = conn.Close()
	}()

	var connMu sync.Mutex
	var dataWriteState websocketDataWriteState
	writeJSON := func(v interface{}) error {
		if err := writeWebSocketJSON(conn, &connMu, &dataWriteState, v, agentWSDataWriteTimeout); err != nil {
			log.Printf("[gateway] client write failed: %v", err)
			return fmt.Errorf("%w: %v", errGatewayClientDisconnected, err)
		}
		return nil
	}
	writeRaw := func(raw []byte) error {
		return writeJSON(json.RawMessage(raw))
	}

	connCtx, cancelConn := context.WithCancel(r.Context())
	defer cancelConn()
	messages := make(chan []byte, 16)
	go func() {
		defer close(messages)
		defer cancelConn()
		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("[gateway] client read failed: %v", err)
				}
				return
			}
			if msgType != websocket.TextMessage {
				continue
			}
			select {
			case messages <- data:
			case <-connCtx.Done():
				return
			}
		}
	}()

	var callWG sync.WaitGroup
	var callsMu sync.Mutex
	calls := make(map[string]context.CancelFunc)
	for data := range messages {
		var req api.JSONRPCRequest
		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			agent := h.registry.Get(cluster, instance)
			if failure := h.auditUpgradeVerification(auth, cluster, instance, req, agent); failure != nil {
				writeJSON(*failure)
				continue
			}
			writeJSON(api.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"serverInfo": map[string]string{
						"name":          "doops-gateway",
						"version":       release.Version,
						"releaseCommit": release.Commit,
					},
					"capabilities": clientCapabilitiesFor(agent, h.opts.DetachedTaskTimeout),
				},
			})
		case "tools/call":
			key, ok := clientRPCRequestKey(req.ID)
			if !ok {
				writeJSON(buildErrorResponse(req.ID, -32600, "tools/call requires a string or numeric request id"))
				continue
			}
			callsMu.Lock()
			if _, exists := calls[key]; exists {
				callsMu.Unlock()
				writeJSON(buildErrorResponse(req.ID, -32600, "request id is already active"))
				continue
			}
			// 断线续接: a call that carries an execution_id is bound to the
			// execution, not to this socket. The Agent relay keeps running when
			// the client disconnects, and the client reads the terminal state
			// back with GET /v1/executions/<id> after it reconnects.
			detached := isDetachedToolCallRequest(req.Params)
			if executionID, _ := executionIDFromToolCall(req.Params); executionID != "" {
				detached = true
			}
			var callCtx context.Context
			var cancelCall context.CancelFunc
			callWriteRaw, callWriteJSON := writeRaw, writeJSON
			if detached {
				// A detached doops_bg outlives this connection: its context is
				// not a child of connCtx, it is not tracked by callWG (so the
				// handler returns promptly), and client write failures no
				// longer abort the Agent relay.
				callCtx, cancelCall = context.WithCancel(context.WithoutCancel(connCtx))
				var clientGone atomic.Bool
				callWriteRaw = func(raw []byte) error {
					if clientGone.Load() {
						return nil
					}
					if err := writeRaw(raw); err != nil {
						if errors.Is(err, errGatewayClientDisconnected) {
							clientGone.Store(true)
							return nil
						}
						return err
					}
					return nil
				}
				callWriteJSON = func(v interface{}) error {
					if clientGone.Load() {
						return nil
					}
					if err := writeJSON(v); err != nil {
						if errors.Is(err, errGatewayClientDisconnected) {
							clientGone.Store(true)
							return nil
						}
						return err
					}
					return nil
				}
			} else {
				callCtx, cancelCall = context.WithCancel(connCtx)
			}
			calls[key] = cancelCall
			callsMu.Unlock()
			if !detached {
				callWG.Add(1)
			}
			go func(req api.JSONRPCRequest) {
				if !detached {
					defer callWG.Done()
				}
				defer func() {
					cancelCall()
					callsMu.Lock()
					delete(calls, key)
					callsMu.Unlock()
				}()
				if err := h.handleGatewayToolCall(callCtx, auth, cluster, instance, req, callWriteRaw, callWriteJSON); err != nil {
					writeJSON(buildErrorResponse(req.ID, -32603, err.Error()))
				}
			}(req)
		case "tools/cancel":
			var params struct {
				ID interface{} `json:"id"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				writeJSON(buildErrorResponse(req.ID, -32602, "invalid tools/cancel params"))
				continue
			}
			key, ok := clientRPCRequestKey(params.ID)
			if !ok {
				writeJSON(buildErrorResponse(req.ID, -32602, "tools/cancel requires the original request id"))
				continue
			}
			callsMu.Lock()
			cancel := calls[key]
			callsMu.Unlock()
			if cancel != nil {
				cancel()
			}
			writeJSON(api.JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: map[string]interface{}{"canceled": cancel != nil},
			})
		default:
			writeJSON(buildErrorResponse(req.ID, -32601, "unknown method: "+req.Method))
		}
	}
	callWG.Wait()
}

func clientRPCRequestKey(id interface{}) (string, bool) {
	switch id.(type) {
	case string, float64:
		encoded, err := json.Marshal(id)
		return string(encoded), err == nil
	default:
		return "", false
	}
}
