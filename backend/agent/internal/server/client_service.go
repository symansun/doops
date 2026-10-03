package server

import (
	"context"
	"database/sql"
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
	id := "op-" + randomHex(12)
	op.ID = id
	op.State = "waiting"
	op.StartedAt = time.Now().UTC()
	s.activeOpsMu.Lock()
	s.activeOps[id] = &gatewayActiveOperation{
		GatewayActiveOperation: op,
		cancel:                 cancel,
	}
	s.activeOpsMu.Unlock()
	return id
}

func (s *ClientService) finishActiveOperation(id string) {
	if id == "" {
		return
	}
	s.activeOpsMu.Lock()
	var auditID int64
	var lease *operationLease
	if op := s.activeOps[id]; op != nil {
		auditID = op.AuditID
		lease = op.lease
	}
	delete(s.activeOps, id)
	s.activeOpsMu.Unlock()
	lease.Release()
	if auditID == 0 {
		return
	}
	if err := s.store.MarkExecutionUncertain(auditID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("[gateway] execution outcome could not be persisted: audit=%d: %v", auditID, err)
	}
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
	if op != nil {
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

func operationsConflict(left, right GatewayActiveOperation) bool {
	if left.Cluster != right.Cluster || left.Instance != right.Instance {
		return false
	}
	if isConcurrentQuery(left.Action, left.Resource) {
		return isExclusiveOperation(right.Action, right.Resource) && !strings.HasPrefix(right.Resource, "target-mutation:")
	}
	if isConcurrentQuery(right.Action, right.Resource) {
		return isExclusiveOperation(left.Action, left.Resource) && !strings.HasPrefix(left.Resource, "target-mutation:")
	}
	if isExclusiveOperation(left.Action, left.Resource) || isExclusiveOperation(right.Action, right.Resource) {
		return true
	}
	return left.Resource != "" && left.Resource == right.Resource
}
