package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/pkg/release"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	errGatewayAgentDisconnected = errors.New("agent disconnected")
)

const (
	gatewayAgentTouchTimeout           = 2 * time.Second
	gatewayReadProgressAckInterval     = 5 * time.Second
	gatewayReadProgressAckWriteTimeout = 3 * time.Second
)

type GatewayAgent struct {
	Cluster                string    `json:"cluster"`
	Instance               string    `json:"instance"`
	Key                    string    `json:"key"`
	Remote                 string    `json:"remote"`
	TokenID                string    `json:"token_id,omitempty"`
	Generation             uint64    `json:"generation"`
	RuntimeID              string    `json:"runtime_id,omitempty"`
	ReleaseCommit          string    `json:"release_commit,omitempty"`
	CredentialPlanProtocol string    `json:"credential_plan_protocol,omitempty"`
	StructuredSSH          bool      `json:"structured_ssh,omitempty"`
	ExecutionLifecycle     bool      `json:"execution_lifecycle,omitempty"`
	UpgradeAudit           bool      `json:"upgrade_audit,omitempty"`
	RegistryAuthfile       bool      `json:"registry_authfile,omitempty"`
	COSSnapshotExecutable  bool      `json:"cos_snapshot_executable,omitempty"`
	ToolDeadline           bool      `json:"tool_deadline,omitempty"`
	ConnectedAt            time.Time `json:"connected_at"`
	LastSeen               time.Time `json:"last_seen"`
	HeartbeatAt            time.Time `json:"heartbeat_at,omitempty"`
	Status                 string    `json:"status,omitempty"`
	BusyReason             string    `json:"busy_reason,omitempty"`
	ActiveOps              int       `json:"active_ops,omitempty"`
	QueuedOps              int       `json:"queued_ops,omitempty"`

	stateMu          sync.RWMutex
	conn             *websocket.Conn
	writeMu          sync.Mutex
	dataWriteState   websocketDataWriteState
	heartbeatPayload string
	lastProgressAck  time.Time
	queueMu          sync.Mutex
	queued           int
	opsMu            sync.Mutex
	stateChanged     chan struct{}
	upgradeWaiters   int
	writers          int
	sessionWriters   map[string]int
	readers          int
	resources        map[string]*agentResourceSlot
	pendingMu        sync.Mutex
	pending          map[int64]*gatewayMessageQueue
	activeByRequest  map[int64]*gatewayMessageQueue
	activeSessions   map[string]int
	messageBudget    *gatewayMessageBudget
	closed           chan struct{}
	reqID            int64
	modelVersionID   string
}

type agentResourceSlot struct {
	slot    chan struct{}
	queued  int
	session string
}

type GatewayTarget struct {
	Cluster       string    `json:"cluster"`
	Instance      string    `json:"instance"`
	Key           string    `json:"key"`
	Remote        string    `json:"remote"`
	TokenID       string    `json:"token_id,omitempty"`
	ReleaseCommit string    `json:"release_commit,omitempty"`
	ConnectedAt   time.Time `json:"connected_at"`
	LastSeen      time.Time `json:"last_seen"`
	Busy          bool      `json:"busy"`
	Status        string    `json:"status"`
	BusyReason    string    `json:"busy_reason,omitempty"`
	ActiveOps     int       `json:"active_ops"`
	QueuedOps     int       `json:"queued_ops"`
	// Enrolled reports whether the live session authenticated with a target
	// enrollment token. The token and its ID never leave the Gateway.
	Enrolled  bool     `json:"enrolled"`
	Resources []string `json:"resources,omitempty"`
	Sessions  []string `json:"sessions,omitempty"`
}

func newGatewayAgentSession(
	cluster,
	instance,
	tokenID,
	remote string,
	conn *websocket.Conn,
	messageBudget *gatewayMessageBudget,
) *GatewayAgent {
	agent := &GatewayAgent{
		Cluster:         cluster,
		Instance:        instance,
		Key:             tunnelKey(cluster, instance),
		Remote:          remote,
		TokenID:         tokenID,
		ConnectedAt:     time.Now().UTC(),
		LastSeen:        time.Now().UTC(),
		conn:            conn,
		pending:         make(map[int64]*gatewayMessageQueue),
		activeByRequest: make(map[int64]*gatewayMessageQueue),
		resources:       make(map[string]*agentResourceSlot),
		sessionWriters:  make(map[string]int),
		activeSessions:  make(map[string]int),
		messageBudget:   messageBudget,
		closed:          make(chan struct{}),
		stateChanged:    make(chan struct{}),
	}
	return agent
}

func (a *GatewayAgent) initialize() error {
	id := atomic.AddInt64(&a.reqID, 1)
	ch := a.registerPending(id, "")
	defer a.unregisterPending(id)
	if err := a.writeJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"id":      id,
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo": map[string]string{
				"name":    "doops-gateway",
				"version": "1.0",
			},
		},
	}); err != nil {
		return err
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		if msg, ok, queueErr := ch.dequeue(); queueErr != nil {
			return queueErr
		} else if ok {
			if result, ok := msg.Parsed["result"].(map[string]interface{}); ok {
				a.applyInitializeResult(result)
			}
			return nil
		}
		select {
		case <-ch.notify:
		case <-timer.C:
			return fmt.Errorf("agent initialize timed out")
		case <-a.closed:
			return fmt.Errorf("agent connection closed")
		}
	}
}

func (a *GatewayAgent) applyInitializeResult(result map[string]interface{}) {
	runtimeID, _ := result["runtimeId"].(string)
	a.RuntimeID = strings.TrimSpace(runtimeID)
	serverInfo, _ := result["serverInfo"].(map[string]interface{})
	releaseCommit, _ := serverInfo["releaseCommit"].(string)
	releaseCommit = strings.TrimSpace(releaseCommit)
	if !release.ValidCommit(releaseCommit) {
		releaseCommit = ""
	}
	a.ReleaseCommit = releaseCommit
	capabilities, _ := result["capabilities"].(map[string]interface{})
	protocols, ok := capabilities["protocols"].(map[string]interface{})
	a.ExecutionLifecycle = executionLifecycleCapabilityCompatible(protocols[executionLifecycleProtocol])
	a.UpgradeAudit = upgradeAuditCapabilityCompatible(protocols[upgradeAuditProtocol])
	// An Agent that does not advertise this rejects an authfile_only request
	// outright, because it decodes the materialization request strictly.
	a.RegistryAuthfile = registryAuthfileCapabilityCompatible(protocols[registryAuthfileProtocol])
	a.COSSnapshotExecutable = cosSnapshotExecutableCapabilityCompatible(protocols[cosSnapshotExecutableProtocol])
	// Only an Agent that declares this understands a timeout_ms tools/call
	// field and replies with agent_tool_timeout when its own deadline fires.
	a.ToolDeadline = toolDeadlineCapabilityCompatible(protocols[toolDeadlineProtocol])
	if !ok {
		return
	}
	if credentialPlanCapabilityCompatible(protocols["credentialPlan"]) {
		a.CredentialPlanProtocol = credentialPlanProtocolV2
	}
	a.StructuredSSH = structuredSSHCapabilityCompatible(protocols[structuredSSHProtocol])
}

func (a *GatewayAgent) readLoop(lease, initialReadTimeout time.Duration) {
	defer close(a.closed)
	defer a.conn.Close()
	a.conn.SetReadDeadline(time.Now().Add(initialReadTimeout))
	a.conn.SetPongHandler(func(payload string) error {
		a.heartbeat(payload)
		return a.conn.SetReadDeadline(time.Now().Add(lease))
	})
	for {
		refreshActivity := func() {
			now := time.Now()
			_ = a.conn.SetReadDeadline(now.Add(lease))
			a.touchMemory(now.UTC())
			a.acknowledgeReadProgress(now)
		}
		msgType, data, err := readWebSocketMessage(a.conn, refreshActivity)
		if err != nil {
			if !errors.Is(err, io.EOF) && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("[gateway] agent read failed: %s: %v", a.Key, err)
			}
			return
		}
		refreshActivity()
		if msgType != websocket.TextMessage {
			continue
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			continue
		}
		msg := gatewayWSMessage{Raw: append([]byte(nil), data...), Parsed: parsed}
		if id, ok := numericID(parsed["id"]); ok {
			a.pendingMu.Lock()
			queue := a.pending[id]
			a.pendingMu.Unlock()
			if queue != nil {
				queue.observeTerminal(msg)
				if err := queue.enqueue(msg); err != nil && !errors.Is(err, errGatewayMessageQueueClosed) {
					log.Printf("[gateway] agent message queue failed: %s id=%d: %v", a.Key, id, err)
					a.cancelPending(queue, true)
				}
				continue
			}
		}
		if method, _ := parsed["method"].(string); method == "notifications/message" {
			requestID, ok := requestIDFromNotification(parsed)
			if !ok {
				log.Printf("[gateway] agent protocol violation: notification missing request_id: %s", a.Key)
				return
			}
			a.pendingMu.Lock()
			queue := a.activeByRequest[requestID]
			a.pendingMu.Unlock()
			if queue != nil {
				if err := queue.enqueue(msg); err != nil && !errors.Is(err, errGatewayMessageQueueClosed) {
					log.Printf("[gateway] agent notification queue failed: %s request_id=%d: %v", a.Key, requestID, err)
					a.cancelPending(queue, true)
				}
			}
		}
	}
}

func (a *GatewayAgent) acknowledgeReadProgress(now time.Time) {
	if !a.lastProgressAck.IsZero() && now.Sub(a.lastProgressAck) < gatewayReadProgressAckInterval {
		return
	}
	a.lastProgressAck = now
	// An unsolicited Pong proves that the gateway consumed bytes before a large message finishes.
	writeHeartbeatControl(
		a.conn,
		&a.dataWriteState,
		websocket.PongMessage,
		[]byte("read-progress"),
		gatewayReadProgressAckWriteTimeout,
	)
}

func (a *GatewayAgent) cancelPending(queue *gatewayMessageQueue, async bool) <-chan struct{} {
	if queue == nil || queue.cancelMethod == "" {
		return nil
	}
	send := func() {
		defer close(queue.cancelDone)
		if err := a.writeJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  queue.cancelMethod,
			"id":      fmt.Sprintf("cancel-%d", queue.requestID),
			"params":  map[string]interface{}{"id": queue.requestID},
		}); err != nil {
			log.Printf("[gateway] cancel failed: %s id=%d method=%s: %v", a.Key, queue.requestID, queue.cancelMethod, err)
		}
	}
	queue.cancelOnce.Do(func() {
		if async {
			go send()
			return
		}
		send()
	})
	return queue.cancelDone
}

func (a *GatewayAgent) waitPendingCancel(queue *gatewayMessageQueue) error {
	select {
	case <-queue.terminal:
		return queue.terminalErr
	default:
	}
	a.cancelPending(queue, true)
	timer := time.NewTimer(executionCancellationTimeout)
	defer timer.Stop()
	select {
	case <-queue.terminal:
		return queue.terminalErr
	case <-a.closed:
		return fmt.Errorf("%w: agent disconnected before acknowledging cancellation", errExecutionOutcomeUnknown)
	case <-timer.C:
		return fmt.Errorf("%w: agent did not acknowledge cancellation within %s", errExecutionOutcomeUnknown, executionCancellationTimeout)
	}
}

func (a *GatewayAgent) pingLoop(registry *AgentRegistry, lease time.Duration) {
	interval := lease / 3
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if !writeHeartbeatPing(a.conn, &a.dataWriteState, []byte("ping"), 10*time.Second) {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if !writeHeartbeatPing(a.conn, &a.dataWriteState, []byte("ping"), 10*time.Second) {
				return
			}
			if registry != nil {
				a.stateMu.RLock()
				lastSeen := a.LastSeen
				a.stateMu.RUnlock()
				ctx, cancel := context.WithTimeout(context.Background(), gatewayAgentTouchTimeout)
				err := registry.TouchContext(ctx, a, lastSeen)
				cancel()
				if err != nil {
					log.Printf("[gateway] persist agent last-seen failed: %s generation=%d: %v", a.Key, a.Generation, err)
				}
			}
		case <-a.closed:
			return
		}
	}
}

func (a *GatewayAgent) touchMemory(lastSeen time.Time) {
	a.stateMu.Lock()
	a.LastSeen = lastSeen
	a.stateMu.Unlock()
}

func (a *GatewayAgent) heartbeat(payload string) {
	now := time.Now().UTC()
	a.stateMu.Lock()
	a.HeartbeatAt = now
	a.heartbeatPayload = payload
	a.LastSeen = now
	a.stateMu.Unlock()
}

func (a *GatewayAgent) registerPending(id int64, cancelMethod string) *gatewayMessageQueue {
	queue := newGatewayMessageQueue(a.messageBudget, gatewayMessageQueueMaxBytes, id, cancelMethod)
	a.pendingMu.Lock()
	a.pending[id] = queue
	a.pendingMu.Unlock()
	return queue
}

func (a *GatewayAgent) unregisterPending(id int64) {
	a.pendingMu.Lock()
	queue := a.pending[id]
	delete(a.pending, id)
	a.pendingMu.Unlock()
	if queue != nil {
		queue.close()
	}
}

func (a *GatewayAgent) writeJSON(v interface{}) error {
	return writeWebSocketJSON(a.conn, &a.writeMu, &a.dataWriteState, v, agentWSDataWriteTimeout)
}

type targetRuntimeState struct {
	busy      bool
	status    string
	reason    string
	activeOps int
	queuedOps int
	resources []string
	sessions  []string
}

func (a *GatewayAgent) runtimeState() targetRuntimeState {
	state := targetRuntimeState{status: "idle"}
	a.opsMu.Lock()
	writers := a.writers
	readers := a.readers
	targetExclusive := false
	for lockKey, count := range a.sessionWriters {
		if count > 0 && strings.HasPrefix(lockKey, "target:") {
			targetExclusive = true
			break
		}
	}
	resources := make([]string, 0, len(a.resources))
	resourceQueued := 0
	for key, slot := range a.resources {
		resources = append(resources, key)
		if slot != nil {
			resourceQueued += slot.queued
		}
	}
	a.opsMu.Unlock()
	sort.Strings(resources)

	a.queueMu.Lock()
	queued := a.queued
	a.queueMu.Unlock()

	a.pendingMu.Lock()
	sessions := make([]string, 0, len(a.activeSessions))
	for sessionID := range a.activeSessions {
		sessions = append(sessions, sessionID)
	}
	a.pendingMu.Unlock()
	sort.Strings(sessions)

	state.activeOps = writers + readers
	state.queuedOps = queued + resourceQueued
	state.resources = resources
	state.sessions = sessions

	switch {
	case targetExclusive:
		state.busy = true
		state.status = "busy"
		state.reason = "exclusive_operation"
	case state.activeOps > 0 || len(resources) > 0 || state.queuedOps > 0:
		state.status = "active"
	}
	return state
}

func (a *GatewayAgent) snapshot() GatewayTarget {
	a.stateMu.RLock()
	target := GatewayTarget{
		Cluster:       a.Cluster,
		Instance:      a.Instance,
		Key:           a.Key,
		Remote:        a.Remote,
		TokenID:       a.TokenID,
		Enrolled:      a.TokenID != "",
		ReleaseCommit: a.ReleaseCommit,
		ConnectedAt:   a.ConnectedAt,
		LastSeen:      a.LastSeen,
	}
	a.stateMu.RUnlock()
	state := a.runtimeState()
	target.Busy = state.busy
	target.Status = state.status
	target.BusyReason = state.reason
	target.ActiveOps = state.activeOps
	target.QueuedOps = state.queuedOps
	target.Resources = state.resources
	target.Sessions = state.sessions
	return target
}
