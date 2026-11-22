package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ErrAgentEnrollmentRequired rejects a session without an enrollment for a
// target whose enrollment is already confirmed.
var ErrAgentEnrollmentRequired = errors.New("agent enrollment required")

// explicitNoReconnectGrace disables the reconnect wait in GatewayHubOptions,
// whose zero value means "unset" and therefore keeps the 10s default.
const explicitNoReconnectGrace = -1 * time.Nanosecond

// ReconnectGraceOption converts a configured reconnect grace for
// GatewayHubOptions.TargetReconnectGrace. An explicit zero means "do not wait";
// unset stays zero so the hub applies its 10s default, and negative values
// describe that default explicitly. The min-int64 sentinel marks the explicit
// no-wait choice because GatewayHubOptions zero values mean "unset".
func ReconnectGraceOption(grace time.Duration) time.Duration {
	if grace > 0 {
		return grace
	}
	if grace < 0 {
		return math.MinInt64
	}
	return explicitNoReconnectGrace
}

// keyedMutex serializes work per target key without blocking unrelated targets.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedMutexEntry
}

type keyedMutexEntry struct {
	mu   sync.Mutex
	refs int
}

func (k *keyedMutex) Lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*keyedMutexEntry)
	}
	e := k.locks[key]
	if e == nil {
		e = &keyedMutexEntry{}
		k.locks[key] = e
	}
	e.refs++
	k.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

// AgentRegistry is the single owner of live agent sessions and their persisted
// connectivity state.
type AgentRegistry struct {
	// allowOpenRegistration admits a session without an enrollment token to a
	// target without a confirmed enrollment; see GatewayHubOptions.
	allowOpenRegistration bool
	store                 *GatewayStore

	registering keyedMutex

	mu               sync.RWMutex
	sessions         map[string]*GatewayAgent
	generations      map[string]uint64
	changed          map[string]chan struct{}
	retired          map[string][]string
	lastSampleAtUnix atomic.Int64
}

func NewAgentRegistry(store *GatewayStore) (*AgentRegistry, error) {
	if store == nil {
		return nil, fmt.Errorf("gateway store is required")
	}
	if err := store.MarkAllAgentsOffline(); err != nil {
		return nil, fmt.Errorf("reconcile gateway agent status: %w", err)
	}
	statuses, err := store.ListAgentStatus()
	if err != nil {
		return nil, fmt.Errorf("load gateway agent generations: %w", err)
	}
	registry := &AgentRegistry{
		store:       store,
		sessions:    make(map[string]*GatewayAgent),
		generations: make(map[string]uint64),
		changed:     make(map[string]chan struct{}),
		retired:     make(map[string][]string),
	}
	for _, status := range statuses {
		registry.generations[tunnelKey(status.Cluster, status.Instance)] = status.Generation
	}
	return registry, nil
}

func (r *AgentRegistry) Register(agent *GatewayAgent) error {
	return r.RegisterWithEnrollment(agent, "")
}

// RegisterWithEnrollment serializes registration per target key so database I/O
// (enrollment check/confirmation and online persistence) never holds the global
// registry lock r.mu. When enrollmentID is non-empty, ConfirmAgentEnrollment
// runs inside the same per-target critical section so an unenrolled session for
// the same target cannot slip between confirmation and session swap.
func (r *AgentRegistry) RegisterWithEnrollment(agent *GatewayAgent, enrollmentID string) error {
	key := agent.Key
	unlock := r.registering.Lock(key)
	defer unlock()

	// 1. Memory-only precheck under read lock.
	r.mu.RLock()
	superseded := runtimeIDRetired(r.retired[key], agent.RuntimeID) && r.sessions[key] != nil
	generation := r.generations[key] + 1
	r.mu.RUnlock()
	if superseded {
		return fmt.Errorf("agent runtime %q was superseded for %s", agent.RuntimeID, key)
	}

	// 2. Database access outside r.mu.
	if enrollmentID != "" {
		if err := r.store.ConfirmAgentEnrollment(enrollmentID); err != nil {
			return fmt.Errorf("confirm agent enrollment: %w", err)
		}
	} else if agent.TokenID == "" {
		// Checked again under the per-target lock: a session that passed the
		// handler's open-registration check must not replace an Agent whose
		// target confirmed its enrollment in the meantime, and no unenrolled
		// session replaces a live one while open registration is closed.
		locked, err := r.store.TargetHasActiveAgentEnrollment(agent.Cluster, agent.Instance)
		if err != nil {
			return fmt.Errorf("check agent enrollment: %w", err)
		}
		if locked || !r.allowOpenRegistration {
			return fmt.Errorf("%w for %s", ErrAgentEnrollmentRequired, key)
		}
	}
	agent.Generation = generation
	if err := r.store.MarkAgentOnline(AgentStatus{
		Cluster:       agent.Cluster,
		Instance:      agent.Instance,
		TokenID:       agent.TokenID,
		Remote:        agent.Remote,
		ReleaseCommit: agent.ReleaseCommit,
		Generation:    agent.Generation,
		ConnectedAt:   agent.ConnectedAt,
		LastSeen:      agent.LastSeen,
	}); err != nil {
		return fmt.Errorf("persist agent online: %w", err)
	}

	// 3. Memory swap only under write lock.
	r.mu.Lock()
	if runtimeIDRetired(r.retired[key], agent.RuntimeID) {
		r.retired[key] = removeRetiredRuntimeID(r.retired[key], agent.RuntimeID)
	}
	old := r.sessions[key]
	if old != nil && old.RuntimeID != "" && old.RuntimeID != agent.RuntimeID {
		r.retired[key] = appendRetiredRuntimeID(r.retired[key], old.RuntimeID)
	}
	r.generations[key] = generation
	r.sessions[key] = agent
	r.notifyLocked(key)
	r.mu.Unlock()
	r.recordLoadSampleMinInterval(0)

	if old != nil && old.conn != nil {
		_ = old.conn.Close()
	}
	return nil
}

func runtimeIDRetired(retired []string, runtimeID string) bool {
	if runtimeID == "" {
		return false
	}
	for _, candidate := range retired {
		if candidate == runtimeID {
			return true
		}
	}
	return false
}

func removeRetiredRuntimeID(retired []string, runtimeID string) []string {
	filtered := retired[:0]
	for _, candidate := range retired {
		if candidate != runtimeID {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func appendRetiredRuntimeID(retired []string, runtimeID string) []string {
	if runtimeID == "" || runtimeIDRetired(retired, runtimeID) {
		return retired
	}
	retired = append(retired, runtimeID)
	const retainedRuntimeLimit = 8
	if len(retired) > retainedRuntimeLimit {
		retired = append([]string(nil), retired[len(retired)-retainedRuntimeLimit:]...)
	}
	return retired
}

func (r *AgentRegistry) Unregister(agent *GatewayAgent) error {
	r.mu.Lock()
	current := r.sessions[agent.Key]
	if current != agent || current.Generation != agent.Generation {
		r.mu.Unlock()
		return nil
	}
	delete(r.sessions, agent.Key)
	r.notifyLocked(agent.Key)
	r.mu.Unlock()

	agent.stateMu.RLock()
	lastSeen := agent.LastSeen
	agent.stateMu.RUnlock()
	if err := r.store.MarkAgentOffline(agent.Cluster, agent.Instance, agent.Generation, lastSeen); err != nil {
		return fmt.Errorf("persist agent offline: %w", err)
	}
	r.recordLoadSampleMinInterval(0)
	return nil
}

func (r *AgentRegistry) TouchContext(ctx context.Context, agent *GatewayAgent, lastSeen time.Time) error {
	r.mu.RLock()
	current := r.sessions[agent.Key]
	if current != agent || current.Generation != agent.Generation {
		r.mu.RUnlock()
		return nil
	}
	r.mu.RUnlock()
	err := r.store.TouchAgentContext(ctx, agent.Cluster, agent.Instance, agent.Generation, lastSeen)
	r.mu.Lock()
	if current := r.sessions[agent.Key]; current == agent && current.Generation == agent.Generation {
		r.notifyLocked(agent.Key)
	}
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("persist agent heartbeat: %w", err)
	}
	r.recordLoadSampleMinInterval(60 * time.Second)
	return nil
}

func (r *AgentRegistry) recordLoadSampleMinInterval(minInterval time.Duration) {
	if r == nil || r.store == nil {
		return
	}
	now := time.Now().UTC()
	if minInterval > 0 {
		nowSec := now.Unix()
		prev := r.lastSampleAtUnix.Load()
		if prev > 0 && nowSec-prev < int64(minInterval/time.Second) {
			return
		}
		if !r.lastSampleAtUnix.CompareAndSwap(prev, nowSec) {
			return
		}
	} else {
		r.lastSampleAtUnix.Store(now.Unix())
	}
	targets := r.List()
	sample := PlatformLoadSample{AtMs: now.UnixMilli(), OnlineAgents: len(targets)}
	for _, t := range targets {
		if t.Busy {
			sample.BusyAgents++
		}
		sample.ActiveOps += t.ActiveOps
		sample.QueuedOps += t.QueuedOps
		sample.ActiveSessions += len(t.Sessions)
		sample.ResourceLocks += len(t.Resources)
	}
	_ = r.store.RecordPlatformLoadSample(sample)
}

func (r *AgentRegistry) Get(cluster, instance string) *GatewayAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[tunnelKey(cluster, instance)]
}

// FlushHeartbeatsContext persists the latest in-memory LastSeen for all live
// sessions in a single batched write transaction.
func (r *AgentRegistry) FlushHeartbeatsContext(ctx context.Context) error {
	r.mu.RLock()
	updates := make([]AgentHeartbeatUpdate, 0, len(r.sessions))
	for _, agent := range r.sessions {
		agent.stateMu.RLock()
		lastSeen := agent.LastSeen
		agent.stateMu.RUnlock()
		updates = append(updates, AgentHeartbeatUpdate{
			Cluster:    agent.Cluster,
			Instance:   agent.Instance,
			Generation: agent.Generation,
			LastSeen:   lastSeen,
		})
	}
	r.mu.RUnlock()
	if len(updates) == 0 {
		return nil
	}
	if err := r.store.TouchAgentsContext(ctx, updates); err != nil {
		return fmt.Errorf("persist batched agent heartbeats: %w", err)
	}
	r.recordLoadSampleMinInterval(60 * time.Second)
	return nil
}

// CloseAll closes every live Agent WebSocket connection (used during graceful
// shutdown after draining).
func (r *AgentRegistry) CloseAll() {
	r.mu.RLock()
	agents := make([]*GatewayAgent, 0, len(r.sessions))
	for _, agent := range r.sessions {
		agents = append(agents, agent)
	}
	r.mu.RUnlock()
	for _, agent := range agents {
		if agent.conn != nil {
			_ = agent.conn.Close()
		}
	}
}

func (r *AgentRegistry) Wait(ctx context.Context, cluster, instance string, grace time.Duration) *GatewayAgent {
	key := tunnelKey(cluster, instance)
	if grace <= 0 {
		return r.Get(cluster, instance)
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	for {
		r.mu.RLock()
		agent := r.sessions[key]
		ch := r.changed[key]
		r.mu.RUnlock()
		if agent != nil {
			return agent
		}
		if ch == nil {
			r.mu.Lock()
			if agent = r.sessions[key]; agent != nil {
				r.mu.Unlock()
				return agent
			}
			if ch = r.changed[key]; ch == nil {
				ch = make(chan struct{})
				r.changed[key] = ch
			}
			r.mu.Unlock()
		}

		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return nil
		case <-ch:
		}
	}
}

func (r *AgentRegistry) List() []GatewayTarget {
	r.mu.RLock()
	out := make([]GatewayTarget, 0, len(r.sessions))
	for _, session := range r.sessions {
		out = append(out, session.snapshot())
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (r *AgentRegistry) notifyLocked(key string) {
	if ch := r.changed[key]; ch != nil {
		close(ch)
	}
	r.changed[key] = make(chan struct{})
}
