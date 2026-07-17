package server

import (
	"log"
	"net/http"
	"strings"
	"time"
)

type AgentRegistrationHandler struct {
	registry         *AgentRegistry
	lease            time.Duration
	heartbeatTimeout time.Duration
	messageBudget    *gatewayMessageBudget
	acknowledge      func(*GatewayAgent) error
}

func NewAgentRegistrationHandler(
	registry *AgentRegistry,
	lease time.Duration,
	messageBudget *gatewayMessageBudget,
) *AgentRegistrationHandler {
	return &AgentRegistrationHandler{
		registry:         registry,
		lease:            lease,
		heartbeatTimeout: 10 * time.Second,
		messageBudget:    messageBudget,
		acknowledge: func(session *GatewayAgent) error {
			return session.writeJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "gateway/registered",
			})
		},
	}
}

func (h *AgentRegistrationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	if cluster == "" || instance == "" {
		http.Error(w, "cluster and instance are required", http.StatusBadRequest)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[gateway] agent websocket upgrade failed: %v", err)
		return
	}
	session := newGatewayAgentSession(
		cluster,
		instance,
		"",
		r.RemoteAddr,
		conn,
		h.messageBudget,
	)
	go session.readLoop(h.lease)
	if err := session.initialize(); err != nil {
		log.Printf("[gateway] agent initialize failed: %s: %v", session.Key, err)
		_ = conn.Close()
		<-session.closed
		return
	}
	if !waitForInitialAgentHeartbeat(session, h.heartbeatTimeout) {
		log.Printf("[gateway] agent heartbeat preflight failed: %s", session.Key)
		_ = conn.Close()
		<-session.closed
		return
	}
	if err := h.acknowledge(session); err != nil {
		log.Printf("[gateway] agent registration acknowledgement failed: %s: %v", session.Key, err)
		_ = conn.Close()
		<-session.closed
		return
	}
	if err := h.registry.Register(session); err != nil {
		log.Printf("[gateway] agent registration failed: %s: %v", session.Key, err)
		_ = conn.Close()
		<-session.closed
		return
	}
	log.Printf("[gateway] agent online: %s generation=%d remote=%s", session.Key, session.Generation, session.Remote)
	go session.pingLoop(h.registry, h.lease)

	<-session.closed
	if err := h.registry.Unregister(session); err != nil {
		log.Printf("[gateway] agent offline persistence failed: %s generation=%d: %v", session.Key, session.Generation, err)
	}
	log.Printf("[gateway] agent offline: %s generation=%d", session.Key, session.Generation)
}

func waitForInitialAgentHeartbeat(session *GatewayAgent, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	payload := "registration-preflight-" + randomHex(8)
	if !writeHeartbeatPing(session.conn, []byte(payload), timeout) {
		return false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		session.stateMu.RLock()
		heartbeatPayload := session.heartbeatPayload
		session.stateMu.RUnlock()
		if heartbeatPayload == payload {
			return true
		}
		select {
		case <-session.closed:
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
		}
	}
}
