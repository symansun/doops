package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSchedulerJobLeaseSerializesAndReclaims(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	claimed, err := store.ClaimSchedulerJob("job-test", "owner-a", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("first claim = %t, %v", claimed, err)
	}
	claimed, err = store.ClaimSchedulerJob("job-test", "owner-b", now.Add(30*time.Second), time.Minute)
	if err != nil || claimed {
		t.Fatalf("live lease was stolen: claimed=%t err=%v", claimed, err)
	}
	if err := store.ReleaseSchedulerJob("job-test", "owner-b"); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimSchedulerJob("job-test", "owner-b", now.Add(30*time.Second), time.Minute)
	if err != nil || claimed {
		t.Fatalf("wrong owner release removed live lease: claimed=%t err=%v", claimed, err)
	}
	claimed, err = store.ClaimSchedulerJob("job-test", "owner-b", now.Add(61*time.Second), time.Minute)
	if err != nil || !claimed {
		t.Fatalf("expired lease was not reclaimed: claimed=%t err=%v", claimed, err)
	}
}

func TestSchedulerJobLeaseRenewsOnlyForLiveOwner(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	claimed, err := store.ClaimSchedulerJob("job-renew", "owner-a", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim = %t, %v", claimed, err)
	}
	renewed, err := store.RenewSchedulerJob("job-renew", "owner-b", now.Add(30*time.Second), time.Minute)
	if err != nil || renewed {
		t.Fatalf("wrong owner renewed lease: renewed=%t err=%v", renewed, err)
	}
	renewed, err = store.RenewSchedulerJob("job-renew", "owner-a", now.Add(30*time.Second), time.Minute)
	if err != nil || !renewed {
		t.Fatalf("live owner failed to renew lease: renewed=%t err=%v", renewed, err)
	}
	claimed, err = store.ClaimSchedulerJob("job-renew", "owner-b", now.Add(89*time.Second), time.Minute)
	if err != nil || claimed {
		t.Fatalf("renewed lease was treated as expired: claimed=%t err=%v", claimed, err)
	}
}

func TestSchedulerRunNowReleasesLeaseAfterFailure(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	job, err := store.CreateSchedulerJob(SchedulerJob{
		Name:     "invalid scan",
		ScanMode: "unsupported",
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(nil, store, time.Hour)
	if _, err := scheduler.RunJobNow(context.Background(), job.ID); err == nil {
		t.Fatal("invalid scan mode unexpectedly succeeded")
	}
	claimed, err := store.ClaimSchedulerJob(job.ID, "after-failure", time.Now().UTC(), time.Minute)
	if err != nil || !claimed {
		t.Fatalf("failed run left a live lease: claimed=%t err=%v", claimed, err)
	}
}

func TestSchedulerRunNowRejectsLiveLease(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	job, err := store.CreateSchedulerJob(SchedulerJob{Name: "audit", ScanMode: "audit"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claimed, err := store.ClaimSchedulerJob(job.ID, "existing-run", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("seed claim = %t, %v", claimed, err)
	}
	scheduler := NewScheduler(nil, store, time.Hour)
	_, err = scheduler.RunJobNow(context.Background(), job.ID)
	if !errors.Is(err, ErrSchedulerJobRunning) {
		t.Fatalf("RunJobNow error = %v, want ErrSchedulerJobRunning", err)
	}
}

func TestSchedulerCancellationReturnsWithoutCreatingIssueAndReleasesLease(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	f.agent(t, "scheduler-cancel")
	job, err := f.store.CreateSchedulerJob(SchedulerJob{
		Name:         "cancelled scan",
		ClusterGlob:  "dev",
		InstanceGlob: "local",
		ScanMode:     "ask",
		RepoSlug:     "org/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := "cancelled-run"
	if claimed, err := f.store.ClaimSchedulerJob(job.ID, owner, time.Now().UTC(), time.Minute); err != nil || !claimed {
		t.Fatalf("claim = %t, %v", claimed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scheduler := NewScheduler(f.hub, f.store, time.Hour)
	if _, err := scheduler.runClaimedJob(ctx, job, owner); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run error = %v, want context.Canceled", err)
	}
	if claimed, err := f.store.ClaimSchedulerJob(job.ID, "replacement-run", time.Now().UTC(), time.Minute); err != nil || !claimed {
		t.Fatalf("cancelled run left lease held: claimed=%t err=%v", claimed, err)
	}
	issues, err := f.store.ListSchedulerIssues(job.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("cancelled run created synthetic issues: %#v", issues)
	}
}
