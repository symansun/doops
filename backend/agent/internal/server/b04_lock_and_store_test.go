package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestB04RegisterDoesNotHoldGlobalLockDuringDBWait(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	registry, err := NewAgentRegistry(store)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	registry.allowOpenRegistration = true

	// Pre-register an unrelated target so Get has a live entry to return.
	other := newGatewayAgentSession("c", "b", "", "198.51.100.2:1", nil, nil)
	if err := registry.Register(other); err != nil {
		t.Fatalf("register other: %v", err)
	}

	// Hold the single SQLite write connection open with an uncommitted tx.
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	txOpen := true
	defer func() {
		if txOpen {
			_ = tx.Rollback()
		}
	}()

	regDone := make(chan error, 1)
	go func() {
		regDone <- registry.Register(newGatewayAgentSession("c", "a", "", "198.51.100.1:1", nil, nil))
	}()
	// Give Register time to reach MarkAgentOnline on the blocked write conn.
	time.Sleep(50 * time.Millisecond)

	getDone := make(chan *GatewayAgent, 1)
	go func() {
		getDone <- registry.Get("c", "b")
	}()
	select {
	case got := <-getDone:
		if got != other {
			t.Fatalf("registry.Get(c, b) = %v, want %v", got, other)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("registry.Get blocked behind a database wait inside Register")
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback tx: %v", err)
	}
	txOpen = false

	select {
	case err := <-regDone:
		if err != nil {
			t.Fatalf("register c/a after tx rollback: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("register did not complete after tx rollback")
	}
}

func TestB04BeginExecutionDoesNotHoldGlobalLocksDuringDBWait(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})
	defer hub.CloseTunnels()

	agentA := newGatewayAgentSession("c", "a", "", "198.51.100.1:1", nil, nil)
	agentB := newGatewayAgentSession("c", "b", "", "198.51.100.2:1", nil, nil)
	if err := hub.registry.Register(agentA); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := hub.registry.Register(agentB); err != nil {
		t.Fatalf("register b: %v", err)
	}

	auditID, err := store.StartAudit(AuditEvent{
		UserID:         "u1",
		Cluster:        "c",
		Instance:       "a",
		Action:         ActionExec,
		Session:        "s1",
		CommandSummary: "echo ok",
		StartedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("start audit: %v", err)
	}
	opID, _ := hub.client.registerActiveOperationForAgent(GatewayActiveOperation{
		AuditID:  auditID,
		Resource: "session-mutation:s1",
		UserID:   "u1",
		Cluster:  "c",
		Instance: "a",
		Action:   ActionExec,
		Session:  "s1",
		Kind:     "rpc",
	}, func() {}, agentA)
	defer hub.client.finishActiveOperation(opID)

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	txOpen := true
	defer func() {
		if txOpen {
			_ = tx.Rollback()
		}
	}()

	beginDone := make(chan error, 1)
	go func() {
		beginDone <- hub.client.beginExecution(agentA, opID, auditID)
	}()
	time.Sleep(50 * time.Millisecond)

	// Neither registry.mu nor activeOpsMu may be held while MarkExecutionDispatched waits.
	checkDone := make(chan struct{})
	go func() {
		_ = hub.registry.Get("c", "b")
		_ = hub.client.listActiveOperations()
		close(checkDone)
	}()
	select {
	case <-checkDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("registry.Get or listActiveOperations blocked behind beginExecution DB wait")
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback tx: %v", err)
	}
	txOpen = false

	select {
	case err := <-beginDone:
		if err != nil {
			t.Fatalf("beginExecution after tx rollback: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("beginExecution did not complete after tx rollback")
	}
}

func TestB04BeginExecutionDetectsReplacedAgentDuringDBWrite(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})
	defer hub.CloseTunnels()

	agent1 := newGatewayAgentSession("c", "a", "", "198.51.100.1:1", nil, nil)
	if err := hub.registry.Register(agent1); err != nil {
		t.Fatalf("register agent1: %v", err)
	}
	auditID, err := store.StartAudit(AuditEvent{
		UserID:    "u1",
		Cluster:   "c",
		Instance:  "a",
		Action:    ActionExec,
		Session:   "s1",
		StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("start audit: %v", err)
	}
	opID, _ := hub.client.registerActiveOperationForAgent(GatewayActiveOperation{
		AuditID:  auditID,
		Resource: "session-mutation:s1",
		UserID:   "u1",
		Cluster:  "c",
		Instance: "a",
		Action:   ActionExec,
		Session:  "s1",
		Kind:     "rpc",
	}, func() {}, agent1)
	defer hub.client.finishActiveOperation(opID)

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	beginDone := make(chan error, 1)
	go func() {
		beginDone <- hub.client.beginExecution(agent1, opID, auditID)
	}()
	time.Sleep(50 * time.Millisecond)

	// Unregister agent1 from memory while beginExecution is waiting on SQLite.
	hub.registry.mu.Lock()
	delete(hub.registry.sessions, agent1.Key)
	hub.registry.mu.Unlock()

	_ = tx.Rollback()

	select {
	case err := <-beginDone:
		if !errors.Is(err, errGatewayAgentDisconnected) {
			t.Fatalf("beginExecution error = %v, want %v", err, errGatewayAgentDisconnected)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("beginExecution did not return")
	}
}

func TestB04ReadPoolServesReadsWhileWriteTxIsOpen(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	u, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.GrantUser(u.ID, ScopeGrant{Cluster: "prod", Instance: "a1"}); err != nil {
		t.Fatalf("grant user: %v", err)
	}
	if _, err := store.StartAudit(AuditEvent{
		UserID:    u.ID,
		Cluster:   "prod",
		Instance:  "a1",
		Action:    ActionExec,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("start audit: %v", err)
	}

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET is_admin = 1 WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("uncommitted update: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		if !store.UserCanUseTarget(u.ID, "prod", "a1") {
			done <- fmt.Errorf("UserCanUseTarget returned false")
			return
		}
		// Must see the committed snapshot (not the uncommitted tx write).
		if store.UserIsPlatformAdmin(u.ID) {
			done <- fmt.Errorf("UserIsPlatformAdmin saw uncommitted write")
			return
		}
		if _, err := store.TargetHasActiveAgentEnrollment("prod", "a1"); err != nil {
			done <- fmt.Errorf("TargetHasActiveAgentEnrollment: %w", err)
			return
		}
		events, err := store.ListAudit(10)
		if err != nil || len(events) == 0 {
			done <- fmt.Errorf("ListAudit: err=%v len=%d", err, len(events))
			return
		}
		if _, err := store.ListAgentStatus(); err != nil {
			done <- fmt.Errorf("ListAgentStatus: %w", err)
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read-only pool queries blocked behind an uncommitted write transaction")
	}
}

func TestB04ConcurrentRegisterSameTargetSerializesGenerations(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	registry, err := NewAgentRegistry(store)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	registry.allowOpenRegistration = true

	const n = 10
	agents := make([]*GatewayAgent, n)
	for i := 0; i < n; i++ {
		agents[i] = newGatewayAgentSession("prod", "a1", "", fmt.Sprintf("198.51.100.%d:1", i+1), nil, nil)
	}

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(a *GatewayAgent) {
			defer wg.Done()
			if err := registry.Register(a); err != nil {
				errs <- err
			}
		}(agents[i])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent register error: %v", err)
	}

	seenGen := make(map[uint64]bool, n)
	var maxGen uint64
	var lastAgent *GatewayAgent
	for _, a := range agents {
		if seenGen[a.Generation] {
			t.Fatalf("duplicate generation %d", a.Generation)
		}
		seenGen[a.Generation] = true
		if a.Generation > maxGen {
			maxGen = a.Generation
			lastAgent = a
		}
	}
	if maxGen != n {
		t.Fatalf("max generation = %d, want %d", maxGen, n)
	}
	if cur := registry.Get("prod", "a1"); cur != lastAgent {
		t.Fatalf("active session generation = %d, want %d", cur.Generation, lastAgent.Generation)
	}
}

func TestB04FlushHeartbeatsContextBatchesUpdates(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	registry, err := NewAgentRegistry(store)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	registry.allowOpenRegistration = true

	const count = 25
	future := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Millisecond)
	for i := 0; i < count; i++ {
		a := newGatewayAgentSession("batch", fmt.Sprintf("node-%02d", i), "", "198.51.100.1:1", nil, nil)
		if err := registry.Register(a); err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
		a.touchMemory(future)
	}

	if err := registry.FlushHeartbeatsContext(context.Background()); err != nil {
		t.Fatalf("FlushHeartbeatsContext: %v", err)
	}

	statuses, err := store.ListAgentStatus()
	if err != nil {
		t.Fatalf("ListAgentStatus: %v", err)
	}
	if len(statuses) != count {
		t.Fatalf("status count = %d, want %d", len(statuses), count)
	}
	for _, st := range statuses {
		if !st.LastSeen.Equal(future) {
			t.Fatalf("%s/%s last_seen = %s, want %s", st.Cluster, st.Instance, st.LastSeen, future)
		}
	}
}
