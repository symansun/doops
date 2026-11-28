package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (a *GatewayAgent) acquireActionLease(ctx context.Context, action GatewayAction, resourceKey string, maxQueued int, wait time.Duration) (*operationLease, error) {
	return a.acquireActionLeaseForSession(ctx, action, resourceKey, "", maxQueued, wait)
}

// acquireProxyLease admits one proxied HTTP request. A proxied request is an
// independent upstream call, so the Gateway does not order it against other
// proxied requests and it holds no resource slot. It is counted as a target
// reader instead, which is what keeps two fences truthful: an Agent upgrade
// still waits for in-flight proxied traffic to drain (see
// sessionResourceHeldLocked for "target:"), and a proposed upgrade still
// rejects new proxied requests (targetExclusivePendingLocked).
func (a *GatewayAgent) acquireProxyLease() (*operationLease, error) {
	if err := a.acquireReadOnly(); err != nil {
		return nil, err
	}
	return newOperationLease(a.releaseReadOnly), nil
}

func exclusiveLockKey(resourceKey, session string) string {
	if strings.HasPrefix(resourceKey, "target-mutation:") && strings.TrimSpace(session) != "" {
		return "session-mutation:" + strings.TrimPrefix(resourceKey, "target-mutation:") + "/" + strings.TrimSpace(session)
	}
	return resourceKey
}

func sessionForResourceKey(resourceKey string) string {
	for _, prefix := range []string{"session-mutation:", "workspace:"} {
		if strings.HasPrefix(resourceKey, prefix) {
			value := strings.TrimPrefix(resourceKey, prefix)
			if prefix == "session-mutation:" {
				if index := strings.LastIndexByte(value, '/'); index >= 0 {
					return value[index+1:]
				}
			}
			return value
		}
	}
	return ""
}

func (a *GatewayAgent) sessionResourceHeldLocked(lockKey string) bool {
	if strings.HasPrefix(lockKey, "target:") {
		// A workspace slot can contain only waiters. Every granted slot counts as a reader.
		return a.writers > 0 || a.readers > 0
	}
	session := sessionForResourceKey(lockKey)
	if session == "" {
		return false
	}
	shellLike := strings.HasPrefix(lockKey, "session-mutation:")
	for key, count := range a.sessionWriters {
		if count == 0 {
			continue
		}
		if strings.HasPrefix(key, "target:") {
			return true
		}
		// Shell, SSH and apply calls in one session run concurrently; only
		// workspace resources and Agent upgrade exclude them.
		if shellLike && strings.HasPrefix(key, "session-mutation:") {
			continue
		}
		if sessionForResourceKey(key) == session {
			return true
		}
	}
	for key, slot := range a.resources {
		if slot != nil && len(slot.slot) == 0 && (sessionForResourceKey(key) == session || slot.session == session) {
			return true
		}
	}
	return false
}

func (a *GatewayAgent) conflictingResourceLocked(resourceKey, session string) string {
	for key, count := range a.sessionWriters {
		if count > 0 && strings.HasPrefix(key, "target:") {
			return key
		}
	}
	if strings.HasPrefix(resourceKey, "target:") {
		if a.writers > 0 {
			return resourceKey
		}
		return ""
	}
	workspace := sessionForResourceKey(resourceKey)
	for key, count := range a.sessionWriters {
		holder := sessionForResourceKey(key)
		if count > 0 && ((session != "" && holder == session) || (workspace != "" && holder == workspace)) {
			return key
		}
	}
	for key, slot := range a.resources {
		if key != resourceKey && slot != nil && len(slot.slot) == 0 &&
			((session != "" && (slot.session == session || sessionForResourceKey(key) == session)) ||
				(workspace != "" && slot.session == workspace)) {
			return key
		}
	}
	return ""
}

func (a *GatewayAgent) acquireActionLeaseForSession(ctx context.Context, action GatewayAction, resourceKey, session string, maxQueued int, wait time.Duration) (*operationLease, error) {
	if (strings.HasPrefix(resourceKey, "target-mutation:") || strings.HasPrefix(resourceKey, "nginx:")) && validateSession(strings.TrimSpace(session)) != nil {
		return nil, fmt.Errorf("a valid session is required for mutation")
	}
	if isExclusiveOperation(action, resourceKey) {
		lockKey := exclusiveLockKey(resourceKey, session)
		if err := a.acquireExclusive(ctx, lockKey, maxQueued, wait); err != nil {
			return nil, err
		}
		return newOperationLease(func() { a.releaseExclusive(lockKey) }), nil
	}
	if isConcurrentQuery(action, resourceKey) {
		if err := a.acquireReadOnly(); err != nil {
			return nil, err
		}
		return newOperationLease(a.releaseReadOnly), nil
	}
	if resourceKey != "" {
		if err := a.acquireResource(ctx, action, resourceKey, session, maxQueued, wait); err != nil {
			return nil, err
		}
		return newOperationLease(func() { a.releaseResource(resourceKey) }), nil
	}
	if isConcurrentReadOnlyAction(action) {
		if err := a.acquireReadOnly(); err != nil {
			return nil, err
		}
		return newOperationLease(a.releaseReadOnly), nil
	}
	lockKey := exclusiveLockKey(resourceKey, session)
	if err := a.acquireExclusive(ctx, lockKey, maxQueued, wait); err != nil {
		return nil, err
	}
	return newOperationLease(func() { a.releaseExclusive(lockKey) }), nil
}

func (a *GatewayAgent) acquireExclusive(ctx context.Context, lockKey string, maxQueued int, wait time.Duration) error {
	if strings.TrimSpace(lockKey) == "" {
		return fmt.Errorf("operation resource is required")
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	queued := false
	targetWaiting := false
	defer func() {
		if !queued {
			return
		}
		a.queueMu.Lock()
		if a.queued > 0 {
			a.queued--
		}
		a.queueMu.Unlock()
	}()
	defer func() {
		if !targetWaiting {
			return
		}
		a.opsMu.Lock()
		if a.upgradeWaiters > 0 {
			a.upgradeWaiters--
		}
		a.opsMu.Unlock()
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.opsMu.Lock()
		if !isTargetExclusiveLock(lockKey) && a.targetExclusivePendingLocked() {
			a.opsMu.Unlock()
			return fmt.Errorf("target busy")
		}
		admissible := !a.sessionResourceHeldLocked(lockKey)
		if admissible {
			a.writers++
			if a.sessionWriters == nil {
				a.sessionWriters = make(map[string]int)
			}
			a.sessionWriters[lockKey]++
			a.opsMu.Unlock()
			return nil
		}
		changed := a.stateChangedLocked()
		a.opsMu.Unlock()

		if !queued {
			if maxQueued <= 0 {
				return fmt.Errorf("target busy")
			}
			a.queueMu.Lock()
			if a.queued >= maxQueued {
				a.queueMu.Unlock()
				return fmt.Errorf("target queue full")
			}
			a.queued++
			a.queueMu.Unlock()
			queued = true
			if isTargetExclusiveLock(lockKey) {
				a.opsMu.Lock()
				a.upgradeWaiters++
				targetWaiting = true
				a.opsMu.Unlock()
			}
		}

		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		case <-a.closed:
			return fmt.Errorf("target offline")
		case <-timer.C:
			return fmt.Errorf("target queue timeout")
		}
	}
}

func (a *GatewayAgent) releaseExclusive(lockKey string) {
	a.opsMu.Lock()
	if a.sessionWriters[lockKey] == 0 {
		a.opsMu.Unlock()
		return
	}
	a.writers--
	if a.sessionWriters[lockKey]--; a.sessionWriters[lockKey] <= 0 {
		delete(a.sessionWriters, lockKey)
	}
	a.opsMu.Unlock()
	a.signalStateChange()
}

func (a *GatewayAgent) acquireReadOnly() error {
	a.opsMu.Lock()
	if a.targetExclusiveHeldLocked() || a.targetExclusivePendingLocked() {
		a.opsMu.Unlock()
		return fmt.Errorf("target busy")
	}
	a.readers++
	a.opsMu.Unlock()
	return nil
}

func isTargetExclusiveLock(lockKey string) bool {
	return strings.HasPrefix(lockKey, "target:")
}

// targetExclusivePendingLocked reports whether an Agent upgrade is waiting for
// the target lock. New read and mutation admissions are rejected meanwhile so
// the in-flight operations can drain instead of extending the wait forever.
func (a *GatewayAgent) targetExclusivePendingLocked() bool {
	return a.upgradeWaiters > 0
}

func (a *GatewayAgent) targetExclusiveHeldLocked() bool {
	for key, count := range a.sessionWriters {
		if count > 0 && isTargetExclusiveLock(key) {
			return true
		}
	}
	return false
}

func (a *GatewayAgent) releaseReadOnly() {
	a.opsMu.Lock()
	if a.readers > 0 {
		a.readers--
	}
	a.opsMu.Unlock()
	a.signalStateChange()
}

func (a *GatewayAgent) acquireResource(ctx context.Context, action GatewayAction, key, session string, maxQueued int, wait time.Duration) error {
	a.opsMu.Lock()
	defer a.opsMu.Unlock()
	if a.targetExclusivePendingLocked() {
		return fmt.Errorf("target busy")
	}
	slot := a.resources[key]
	if slot == nil {
		if a.resources == nil {
			a.resources = make(map[string]*agentResourceSlot)
		}
		slot = &agentResourceSlot{slot: make(chan struct{}, 1)}
		slot.slot <- struct{}{}
		a.resources[key] = slot
	}
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
			slot.queued--
		}
		if slot.queued == 0 && len(slot.slot) == 1 {
			delete(a.resources, key)
		}
	}()

	// One queue entry and deadline cover both the session mutation and the
	// workspace slot, even when another holder wins the race after a wakeup.
	waitOn := func(ready <-chan struct{}) error {
		if timer == nil {
			if maxQueued <= 0 {
				return fmt.Errorf("target busy")
			}
			if slot.queued >= maxQueued {
				return fmt.Errorf("target queue full")
			}
			slot.queued++
			timer = time.NewTimer(wait)
		}
		a.opsMu.Unlock()
		defer a.opsMu.Lock()
		select {
		case <-ready:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("target queue timeout")
		case <-a.closed:
			return fmt.Errorf("target offline")
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.targetExclusivePendingLocked() {
			return fmt.Errorf("target busy")
		}
		if blocker := a.conflictingResourceLocked(key, session); blocker != "" {
			if action != ActionAsk || !strings.HasPrefix(key, "workspace:") || !strings.HasPrefix(blocker, "session-mutation:") {
				return fmt.Errorf("target busy")
			}
			if err := waitOn(a.stateChangedLocked()); err != nil {
				return err
			}
			continue
		}
		select {
		case <-slot.slot:
		default:
			if err := waitOn(slot.slot); err != nil {
				return err
			}
			if ctx.Err() != nil || a.targetExclusivePendingLocked() || a.conflictingResourceLocked(key, session) != "" {
				slot.slot <- struct{}{}
				continue
			}
		}
		slot.session = session
		a.readers++
		return nil
	}
}

func (a *GatewayAgent) releaseResource(key string) {
	a.opsMu.Lock()
	slot := a.resources[key]
	if slot != nil {
		select {
		case slot.slot <- struct{}{}:
		default:
		}
		if slot.queued == 0 && len(slot.slot) == 1 {
			delete(a.resources, key)
		}
	}
	if a.readers > 0 {
		a.readers--
	}
	a.opsMu.Unlock()
	a.signalStateChange()
}

func (a *GatewayAgent) stateChangedLocked() chan struct{} {
	if a.stateChanged == nil {
		a.stateChanged = make(chan struct{})
	}
	return a.stateChanged
}

func (a *GatewayAgent) signalStateChange() {
	a.opsMu.Lock()
	previous := a.stateChangedLocked()
	a.stateChanged = make(chan struct{})
	a.opsMu.Unlock()
	close(previous)
}

func isConcurrentReadOnlyAction(action GatewayAction) bool {
	switch action {
	case ActionRead, ActionInfo, ActionCheck:
		return true
	default:
		return false
	}
}

func isConcurrentQuery(action GatewayAction, resourceKey string) bool {
	return isConcurrentReadOnlyAction(action) ||
		strings.HasPrefix(resourceKey, "query:")
}

func isExclusiveOperation(action GatewayAction, resourceKey string) bool {
	return action == ActionAgentUpgrade || strings.HasPrefix(resourceKey, "target:") || strings.HasPrefix(resourceKey, "target-mutation:") ||
		(resourceKey == "" && !isConcurrentReadOnlyAction(action))
}
