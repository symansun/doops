package server

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type AgentRegistrationHandler struct {
	registry         *AgentRegistry
	lease            time.Duration
	heartbeatTimeout time.Duration
	messageBudget    *gatewayMessageBudget
	// allowOpenRegistration admits a session without an enrollment token to a
	// target that has no confirmed enrollment. It is a temporary migration
	// escape hatch; closed is the default.
	allowOpenRegistration bool
	draining              atomic.Bool
	acknowledge           func(*GatewayAgent) error
	// onRegistered runs after an Agent is registered, outside the read loop.
	onRegistered func(*GatewayAgent)
}

func NewAgentRegistrationHandler(
	registry *AgentRegistry,
	lease time.Duration,
	messageBudget *gatewayMessageBudget,
	allowOpenRegistration bool,
) *AgentRegistrationHandler {
	return &AgentRegistrationHandler{
		registry:              registry,
		lease:                 lease,
		heartbeatTimeout:      10 * time.Second,
		messageBudget:         messageBudget,
		allowOpenRegistration: allowOpenRegistration,
		acknowledge: func(session *GatewayAgent) error {
			return session.writeJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "gateway/registered",
			})
		},
	}
}

func (h *AgentRegistrationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "gateway_draining: the Gateway is restarting; retry in a moment", http.StatusServiceUnavailable)
		return
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	if cluster == "" || instance == "" {
		http.Error(w, "cluster and instance are required", http.StatusBadRequest)
		return
	}
	// Open registration is closed by default. A session needs an enrollment
	// token bound to this exact target unless the operator temporarily opened
	// registration for targets that have no confirmed enrollment yet.
	locked, err := h.registry.store.TargetHasActiveAgentEnrollment(cluster, instance)
	if err != nil {
		http.Error(w, "agent enrollment unavailable", http.StatusServiceUnavailable)
		return
	}
	enrollmentID := ""
	if token := bearerToken(r); token != "" {
		// A presented token is always verified, even on an unlocked target: an
		// invalid token never falls back to open registration.
		enrollment, verifyErr := h.registry.store.VerifyAgentEnrollment(token, cluster, instance)
		if verifyErr == nil {
			enrollmentID = enrollment.ID
		} else if !errors.Is(verifyErr, ErrUnauthorized) {
			http.Error(w, "agent enrollment unavailable", http.StatusServiceUnavailable)
			return
		} else {
			writeAgentEnrollmentRequired(w)
			return
		}
	} else if locked || !h.allowOpenRegistration {
		writeAgentEnrollmentRequired(w)
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
		enrollmentID,
		r.RemoteAddr,
		conn,
		h.messageBudget,
	)
	initialReadTimeout := h.lease
	if h.heartbeatTimeout > initialReadTimeout {
		initialReadTimeout = h.heartbeatTimeout
	}
	go session.readLoop(h.lease, initialReadTimeout)
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
	if h.draining.Load() {
		log.Printf("[gateway] agent registration rejected while draining: %s", session.Key)
		_ = conn.Close()
		<-session.closed
		return
	}
	if err := h.registry.RegisterWithEnrollment(session, enrollmentID); err != nil {
		if errors.Is(err, ErrAgentEnrollmentRequired) {
			log.Printf("[gateway] agent registration closed while connecting: %s", session.Key)
			_ = conn.Close()
			<-session.closed
			return
		}
		log.Printf("[gateway] agent registration failed: %s: %v", session.Key, err)
		_ = conn.Close()
		<-session.closed
		return
	}
	if session.TokenID == "" {
		log.Printf("[gateway] WARNING: unenrolled agent session accepted for %s; open agent registration is enabled (-allow-open-agent-registration)", session.Key)
	}
	log.Printf("[gateway] agent online: %s generation=%d remote=%s", session.Key, session.Generation, session.Remote)
	go session.pingLoop(h.registry, h.lease)
	if h.onRegistered != nil {
		go h.onRegistered(session)
	}

	<-session.closed
	if err := h.registry.Unregister(session); err != nil {
		log.Printf("[gateway] agent offline persistence failed: %s generation=%d: %v", session.Key, session.Generation, err)
	}
	log.Printf("[gateway] agent offline: %s generation=%d", session.Key, session.Generation)
}

func writeAgentEnrollmentRequired(w http.ResponseWriter) {
	writeDoopsError(w, http.StatusUnauthorized, `Bearer realm="doops-agent"`, doopsErrorResponse{
		Error:      "unauthorized",
		Code:       "agent_enrollment_auth_failed",
		AuthDomain: "agent_enrollment",
		Message:    "Agent enrollment token is missing, invalid, revoked, or bound to another target",
		NextAction: "Use the active enrollment token issued for this exact cluster and instance; do not use a Gateway user token or managed credential",
	})
}

func waitForInitialAgentHeartbeat(session *GatewayAgent, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	payload := "registration-preflight-" + randomHex(8)
	if !writeHeartbeatPing(session.conn, &session.dataWriteState, []byte(payload), timeout) {
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
