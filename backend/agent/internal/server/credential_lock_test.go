package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCredentialMutationLocksDifferentIDsRunInParallel(t *testing.T) {
	hub := &GatewayHub{}
	releaseHeld, err := hub.acquireCredentialMutationLocks(context.Background(), []string{"cred_a"})
	if err != nil {
		t.Fatalf("hold credential lock: %v", err)
	}
	defer releaseHeld()

	result := make(chan error, 1)
	go func() {
		release, err := hub.acquireCredentialMutationLocks(context.Background(), []string{"cred_b"})
		if err == nil {
			release()
		}
		result <- err
	}()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("different credential lock failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("different credential lock did not run in parallel")
	}
}

func TestCredentialMutationLocksSameIDAreSerialized(t *testing.T) {
	hub := &GatewayHub{}
	releaseHeld, err := hub.acquireCredentialMutationLocks(context.Background(), []string{"cred_a"})
	if err != nil {
		t.Fatalf("hold credential lock: %v", err)
	}

	started := make(chan struct{})
	acquired := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		release, err := hub.acquireCredentialMutationLocks(context.Background(), []string{"cred_a"})
		if err == nil {
			close(acquired)
			release()
		}
		result <- err
	}()
	<-started

	select {
	case <-acquired:
		releaseHeld()
		t.Fatal("same credential lock was acquired concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	releaseHeld()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("serialized credential lock failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("same credential lock was not released")
	}
}

func TestCredentialMutationLocksReverseOrderDoesNotDeadlock(t *testing.T) {
	hub := &GatewayHub{}
	start := make(chan struct{})
	result := make(chan error, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for _, ids := range [][]string{{"cred_b", "cred_a", "cred_a"}, {"cred_a", "cred_b"}} {
		go func(ids []string) {
			<-start
			release, err := hub.acquireCredentialMutationLocks(ctx, ids)
			if err == nil {
				release()
			}
			result <- err
		}(ids)
	}
	close(start)

	for range 2 {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("reverse-order credential lock failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("reverse-order credential locks deadlocked")
		}
	}
}

func TestCredentialMutationLockCancellationLeavesNoResidue(t *testing.T) {
	hub := &GatewayHub{}
	releaseHeld, err := hub.acquireCredentialMutationLocks(context.Background(), []string{"cred_b"})
	if err != nil {
		t.Fatalf("hold credential lock: %v", err)
	}
	defer releaseHeld()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		release, err := hub.acquireCredentialMutationLocks(ctx, []string{"cred_a", "cred_b"})
		if err == nil {
			release()
		}
		result <- err
	}()
	<-started
	firstLock := hub.credentialMutationLock("cred_a")
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for len(firstLock.ready) != 0 {
		select {
		case <-deadline.C:
			t.Fatal("multi-credential waiter did not acquire its first lock")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled credential lock = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled credential lock did not return")
	}
	retryCtx, retryCancel := context.WithTimeout(context.Background(), time.Second)
	defer retryCancel()
	release, err := hub.acquireCredentialMutationLocks(retryCtx, []string{"cred_a"})
	if err != nil {
		t.Fatalf("credential lock acquired before cancellation was not released: %v", err)
	}
	release()
}
