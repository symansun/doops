package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/user/doops/agent/internal/dispatcher"
)

type ClientService struct {
	hub      *GatewayHub
	store    *GatewayStore
	registry *AgentRegistry
	opts     GatewayHubOptions
	commands *dispatcher.Dispatcher

	opsMu   sync.Mutex
	active  int
	userOps map[string]int

	activeOpsMu sync.Mutex
	activeOps   map[string]*gatewayActiveOperation
}

func NewClientService(
	store *GatewayStore,
	registry *AgentRegistry,
	opts GatewayHubOptions,
) *ClientService {
	return &ClientService{
		store:     store,
		registry:  registry,
		opts:      opts,
		commands:  dispatcher.NewDispatcher(),
		userOps:   make(map[string]int),
		activeOps: make(map[string]*gatewayActiveOperation),
	}
}

func (s *ClientService) validateCommand(tool string, args json.RawMessage) error {
	if tool != "doops_shell" && tool != "doops_bg" {
		return nil
	}
	command := extractStringArg(args, "command")
	if tool == "doops_bg" && s.commands.IsHelmMutation(command) {
		return fmt.Errorf("background Helm mutation is prohibited; use foreground exec")
	}
	if s.commands.Classify(command) == dispatcher.PathBlocked {
		return fmt.Errorf("blocked dangerous command")
	}
	return nil
}

func (s *ClientService) acquireOperationSlot(userID string) (func(), error) {
	s.opsMu.Lock()
	defer s.opsMu.Unlock()
	if s.active >= s.opts.MaxConcurrentOperations {
		return nil, fmt.Errorf("global operation limit exceeded")
	}
	if s.userOps[userID] >= s.opts.MaxConcurrentPerUser {
		return nil, fmt.Errorf("user operation limit exceeded")
	}
	s.active++
	s.userOps[userID]++
	return func() {
		s.opsMu.Lock()
		defer s.opsMu.Unlock()
		s.active--
		s.userOps[userID]--
		if s.userOps[userID] <= 0 {
			delete(s.userOps, userID)
		}
	}, nil
}

func (s *ClientService) acquireGlobalOperationSlot() (func(), error) {
	s.opsMu.Lock()
	defer s.opsMu.Unlock()
	if s.active >= s.opts.MaxConcurrentOperations {
		return nil, fmt.Errorf("global operation limit exceeded")
	}
	s.active++
	return func() {
		s.opsMu.Lock()
		defer s.opsMu.Unlock()
		s.active--
	}, nil
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

func (s *ClientService) cancelActiveOperationForTarget(id, cluster, instance string) bool {
	s.activeOpsMu.Lock()
	op := s.activeOps[id]
	if op == nil || op.Cluster != cluster || op.Instance != instance {
		s.activeOpsMu.Unlock()
		return false
	}
	if op.State != "finalizing" {
		op.State = "cancel_requested"
	}
	cancel := op.cancel
	s.activeOpsMu.Unlock()
	cancel()
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
	if left.Session != "" && left.Session == right.Session {
		return true
	}
	leftWorkspace := sessionForResourceKey(left.Resource)
	rightWorkspace := sessionForResourceKey(right.Resource)
	return (leftWorkspace != "" && leftWorkspace == right.Session) ||
		(rightWorkspace != "" && rightWorkspace == left.Session)
}
