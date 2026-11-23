package server

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ClientService struct {
	hub      *GatewayHub
	store    *GatewayStore
	registry *AgentRegistry
	opts     GatewayHubOptions

	draining atomic.Bool

	activeOpsMu sync.Mutex
	activeOps   map[string]*gatewayActiveOperation
	idle        chan struct{}

	clientConnsMu sync.Mutex
	clientConns   map[*websocket.Conn]struct{}

	detached *detachedTaskRegistry
}

func NewClientService(
	store *GatewayStore,
	registry *AgentRegistry,
	opts GatewayHubOptions,
) *ClientService {
	return &ClientService{
		store:       store,
		registry:    registry,
		opts:        opts,
		activeOps:   make(map[string]*gatewayActiveOperation),
		idle:        make(chan struct{}),
		clientConns: make(map[*websocket.Conn]struct{}),
		detached:    newDetachedTaskRegistry(store),
	}
}

func (s *ClientService) beginDrain() {
	s.draining.Store(true)
}

func (s *ClientService) isDraining() bool {
	return s.draining.Load()
}

func (s *ClientService) activeOperationCount() int {
	s.activeOpsMu.Lock()
	defer s.activeOpsMu.Unlock()
	return len(s.activeOps)
}

// waitIdle blocks until all active operations finish or ctx expires.
// It returns the number of operations still active when it returns (0 when drained).
func (s *ClientService) waitIdle(ctx context.Context) int {
	for {
		s.activeOpsMu.Lock()
		n := len(s.activeOps)
		ch := s.idle
		s.activeOpsMu.Unlock()
		if n == 0 {
			return 0
		}
		select {
		case <-ctx.Done():
			s.activeOpsMu.Lock()
			remaining := len(s.activeOps)
			s.activeOpsMu.Unlock()
			return remaining
		case <-ch:
		}
	}
}

func (s *ClientService) trackClientConn(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	s.clientConnsMu.Lock()
	s.clientConns[conn] = struct{}{}
	s.clientConnsMu.Unlock()
}

func (s *ClientService) untrackClientConn(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	s.clientConnsMu.Lock()
	delete(s.clientConns, conn)
	s.clientConnsMu.Unlock()
}

func (s *ClientService) closeClientConns() {
	s.clientConnsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.clientConns))
	for conn := range s.clientConns {
		conns = append(conns, conn)
	}
	s.clientConnsMu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (s *ClientService) registerActiveOperation(op GatewayActiveOperation, cancel context.CancelFunc) string {
	id, _ := s.registerActiveOperationForAgent(op, cancel, nil)
	return id
}

// registerActiveOperationForAgent binds the operation to its Agent before any
// lease wait, so replacement connections can be fenced without bypassing the
// current connection's lease queue.
func (s *ClientService) registerActiveOperationForAgent(op GatewayActiveOperation, cancel context.CancelFunc, agent *GatewayAgent) (string, string) {
	id := "op-" + randomHex(12)
	op.ID = id
	op.State = "waiting"
	if op.RequestedResource == "" {
		op.RequestedResource = op.Resource
	}
	if op.EffectiveLock == "" {
		op.EffectiveLock = exclusiveLockKey(op.Resource, op.Session)
	}
	op.StartedAt = time.Now().UTC()
	if agent == nil && s.registry != nil {
		agent = s.registry.Get(op.Cluster, op.Instance)
	}
	s.activeOpsMu.Lock()
	s.activeOps[id] = &gatewayActiveOperation{
		GatewayActiveOperation: op,
		cancel:                 cancel,
		agent:                  agent,
	}
	holderID := s.activeOperationConflictLocked(op, agent, id)
	s.activeOpsMu.Unlock()
	return id, holderID
}

func (s *ClientService) finishActiveOperation(id string) {
	if id == "" {
		return
	}
	s.activeOpsMu.Lock()
	op := s.activeOps[id]
	if op != nil && op.State == "finalizing" {
		s.activeOpsMu.Unlock()
		return
	}
	if op != nil {
		// Execution has ended. Audit retries retain evidence, not execution locks.
		op.State = "finalizing"
	}
	var lease *operationLease
	if op != nil {
		lease, op.lease = op.lease, nil
	}
	s.activeOpsMu.Unlock()
	if op == nil {
		return
	}
	lease.Release()
	if op.AuditID != 0 {
		if err := s.persistOperationFinish(op); err != nil {
			if errors.Is(err, errAuditTerminalConflict) {
				log.Printf("[gateway] terminal audit conflict: audit=%d: %v", op.AuditID, err)
				s.releaseFinishedOperation(id, op)
				return
			}
			log.Printf("[gateway] terminal audit persistence pending: audit=%d: %v", op.AuditID, err)
			go s.retryFinishActiveOperation(id, op)
			return
		}
	}
	s.releaseFinishedOperation(id, op)
}

func (s *ClientService) retryFinishActiveOperation(id string, expected *gatewayActiveOperation) {
	backoff := time.Second
	for {
		time.Sleep(backoff)
		s.activeOpsMu.Lock()
		current := s.activeOps[id]
		s.activeOpsMu.Unlock()
		if current != expected {
			return
		}
		if err := s.persistOperationFinish(expected); err == nil {
			s.releaseFinishedOperation(id, expected)
			return
		} else if errors.Is(err, errAuditTerminalConflict) {
			log.Printf("[gateway] terminal audit conflict: audit=%d: %v", expected.AuditID, err)
			s.releaseFinishedOperation(id, expected)
			return
		} else {
			log.Printf("[gateway] retry terminal audit persistence failed: audit=%d: %v", expected.AuditID, err)
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

// Keep the original terminal evidence until storage recovers; never replace a
// known result with an inferred unknown outcome just because one write failed.
func (s *ClientService) finishExecutionAudit(id int64, finish AuditFinish) error {
	if finish.EndedAt.IsZero() {
		finish.EndedAt = time.Now().UTC()
	}
	s.activeOpsMu.Lock()
	for _, op := range s.activeOps {
		if op.AuditID == id {
			op.finish = &finish
			break
		}
	}
	s.activeOpsMu.Unlock()
	return s.store.FinishAudit(id, finish)
}

func (s *ClientService) persistOperationFinish(op *gatewayActiveOperation) error {
	if finish, ok := s.operationFinish(op); ok {
		return s.store.FinishAudit(op.AuditID, finish)
	}
	return s.store.FinalizeExecution(op.AuditID)
}

func (s *ClientService) operationFinish(op *gatewayActiveOperation) (AuditFinish, bool) {
	s.activeOpsMu.Lock()
	defer s.activeOpsMu.Unlock()
	current := s.activeOps[op.ID]
	if current != op || op.finish == nil {
		return AuditFinish{}, false
	}
	return *op.finish, true
}

func (s *ClientService) releaseFinishedOperation(id string, expected *gatewayActiveOperation) {
	s.activeOpsMu.Lock()
	if s.activeOps[id] != expected {
		s.activeOpsMu.Unlock()
		return
	}
	delete(s.activeOps, id)
	if len(s.activeOps) == 0 && s.idle != nil {
		close(s.idle)
		s.idle = make(chan struct{})
	}
	s.activeOpsMu.Unlock()
	expected.lease.Release()
}

func (s *ClientService) attachOperationLease(id string, lease *operationLease) {
	if lease == nil {
		return
	}
	s.activeOpsMu.Lock()
	op := s.activeOps[id]
	if op != nil {
		op.lease = lease
		s.activeOpsMu.Unlock()
		return
	}
	s.activeOpsMu.Unlock()
	lease.Release()
}

func (s *ClientService) listActiveOperations() []GatewayActiveOperation {
	now := time.Now().UTC()
	s.activeOpsMu.Lock()
	defer s.activeOpsMu.Unlock()
	ops := make([]GatewayActiveOperation, 0, len(s.activeOps))
	for _, op := range s.activeOps {
		item := op.GatewayActiveOperation
		item.AgeSeconds = int64(now.Sub(item.StartedAt).Seconds())
		item.BlockedBy = nil
		if item.State == "waiting" {
			for _, holder := range s.activeOps {
				if holder.ID != item.ID && operationsConflict(item, holder.GatewayActiveOperation) &&
					(holder.State == "running" || holder.State == "cancel_requested") {
					item.BlockedBy = append(item.BlockedBy, holder.ID)
				}
			}
			sort.Strings(item.BlockedBy)
		}
		ops = append(ops, item)
	}
	sort.Slice(ops, func(i, j int) bool {
		return ops[i].StartedAt.Before(ops[j].StartedAt)
	})
	return ops
}

func (s *ClientService) cancelActiveOperation(id string) bool {
	s.activeOpsMu.Lock()
	op := s.activeOps[id]
	if op != nil && op.State != "finalizing" {
		op.State = "cancel_requested"
	}
	s.activeOpsMu.Unlock()
	if op == nil {
		return false
	}
	op.cancel()
	return true
}

func (s *ClientService) markOperationRunning(id string) {
	s.activeOpsMu.Lock()
	defer s.activeOpsMu.Unlock()
	if op := s.activeOps[id]; op != nil && op.State != "cancel_requested" {
		op.State = "running"
	}
}

func (s *ClientService) hasCancellationBarrier(candidate GatewayActiveOperation) bool {
	s.activeOpsMu.Lock()
	defer s.activeOpsMu.Unlock()
	for _, holder := range s.activeOps {
		if holder.State != "cancel_requested" {
			continue
		}
		if operationsConflict(candidate, holder.GatewayActiveOperation) {
			return true
		}
	}
	return false
}

// activeOperationConflict keeps a session-owned operation fenced across Agent
// WebSocket generations. The connection-local lease table is recreated after
// reconnect, but the Gateway operation remains authoritative until terminal
// finalization.
func (s *ClientService) activeOperationConflict(candidate GatewayActiveOperation, current *GatewayAgent) string {
	s.activeOpsMu.Lock()
	defer s.activeOpsMu.Unlock()
	return s.activeOperationConflictLocked(candidate, current, "")
}

func (s *ClientService) activeOperationConflictLocked(candidate GatewayActiveOperation, current *GatewayAgent, skipID string) string {
	var oldest *gatewayActiveOperation
	holderID := ""
	for id, holder := range s.activeOps {
		// Same-connection work already has a single lease queue.
		if id == skipID || (current != nil && holder.agent == current) {
			continue
		}
		if operationsConflict(candidate, holder.GatewayActiveOperation) &&
			(oldest == nil || holder.StartedAt.Before(oldest.StartedAt) ||
				(holder.StartedAt.Equal(oldest.StartedAt) && id < holderID)) {
			oldest, holderID = holder, id
		}
	}
	return holderID
}

func operationsConflict(left, right GatewayActiveOperation) bool {
	if left.State == "finalizing" || right.State == "finalizing" {
		return false
	}
	if left.Cluster != right.Cluster || left.Instance != right.Instance {
		return false
	}
	if left.Action == ActionAgentUpgrade || right.Action == ActionAgentUpgrade ||
		strings.HasPrefix(left.Resource, "target:") || strings.HasPrefix(right.Resource, "target:") {
		return true
	}
	if isConcurrentQuery(left.Action, left.Resource) || isConcurrentQuery(right.Action, right.Resource) {
		return false
	}
	if left.Resource != "" && left.Resource == right.Resource && !strings.HasPrefix(left.Resource, "target-mutation:") {
		return true
	}
	if strings.HasPrefix(left.Resource, "target-mutation:") && strings.HasPrefix(right.Resource, "target-mutation:") {
		return false
	}
	if left.Session != "" && left.Session == right.Session {
		return true
	}
	leftWorkspace := sessionForResourceKey(left.Resource)
	rightWorkspace := sessionForResourceKey(right.Resource)
	return (leftWorkspace != "" && leftWorkspace == right.Session) ||
		(rightWorkspace != "" && rightWorkspace == left.Session)
}

type GatewayActiveOperation struct {
	ID                string        `json:"id"`
	AuditID           int64         `json:"audit_id,omitempty"`
	State             string        `json:"state"`
	Resource          string        `json:"resource,omitempty"`
	RequestedResource string        `json:"requested_resource,omitempty"`
	EffectiveLock     string        `json:"effective_lock,omitempty"`
	BlockedBy         []string      `json:"blocked_by,omitempty"`
	UserID            string        `json:"user_id"`
	TokenID           string        `json:"token_id,omitempty"`
	Cluster           string        `json:"cluster"`
	Instance          string        `json:"instance"`
	Action            GatewayAction `json:"action"`
	Session           string        `json:"session,omitempty"`
	CommandSummary    string        `json:"command_summary,omitempty"`
	Kind              string        `json:"kind"`
	StartedAt         time.Time     `json:"started_at"`
	AgeSeconds        int64         `json:"age_seconds"`
}

type gatewayActiveOperation struct {
	GatewayActiveOperation
	cancel context.CancelFunc
	agent  *GatewayAgent
	lease  *operationLease
	finish *AuditFinish
}

// operationLease binds a resource release to the operation that acquired it.
// The release is idempotent so normal completion and operation finalization
// can safely converge on one terminal cleanup path.
type operationLease struct {
	once    sync.Once
	release func()
}

func newOperationLease(release func()) *operationLease {
	return &operationLease{release: release}
}

func (l *operationLease) Release() {
	if l == nil || l.release == nil {
		return
	}
	l.once.Do(l.release)
}

// targetReconnectGrace returns how long a forwarded RPC may wait for a target
// that is reconnecting. Liveness probes stay immediate so `doops targets` and
// `doops targets remove` keep reporting an exact "target offline" answer.
func (h *ClientService) targetReconnectGrace(tool string) time.Duration {
	switch tool {
	case "doops_node_info", "doops_task_status":
		// Liveness probes must report current target state promptly.
		return 0
	case "doops_credential_materialize", "doops_credential_cleanup", "doops_credential_import":
		// Credential delivery and cleanup bind to one target and must fail (or
		// succeed) promptly instead of holding a request for the reconnect grace.
		return 0
	}
	return h.opts.TargetReconnectGrace
}
