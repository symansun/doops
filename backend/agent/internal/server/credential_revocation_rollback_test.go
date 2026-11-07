package server

import (
	"context"
	"errors"
	"testing"
)

// A use that loses the race must not be able to put the target back after the
// revocation began: the rollback and recovery paths record a materialization
// intent too, and that intent is refused like any other. The review accepts
// that these paths fail; these tests lock that they fail closed instead of
// writing a context behind the cleanup list.

func TestCredentialRecoveryAfterARevocationIsRefused(t *testing.T) {
	for _, kind := range revocationRaceKinds {
		t.Run(kind, func(t *testing.T) {
			// Given an apply that rotated from an earlier version and has to
			// recover because the Agent's result was rejected
			f := newRevocationFixture(t)
			rejected := f.stagedVersion(t)
			// When the revocation begins before the recovery materializes
			f.beginRevocation(t, kind)
			// Then the recovery is refused and writes no new context
			err := f.hub.recoverRejectedCredentialMaterialization(
				context.Background(), f.request("api"), f.resource, rejected, f.version.ID, f.grant.ID,
				CredentialPlanReference{Name: f.resource.Name, Scope: revocationScope("api")},
			)
			if !errors.Is(err, ErrCredentialRevoked) {
				t.Fatalf("recovery during a %s revocation = %v, want ErrCredentialRevoked", kind, err)
			}
			if rows := f.statuses(t); len(rows) != 0 {
				t.Fatalf("refused recovery left rows: %v", rows)
			}
			// And the revocation still reaches its terminal state
			if err := f.finalizeRevocation(t, kind); err != nil {
				t.Fatalf("finalize: %v", err)
			}
			if state := f.revokedState(t, kind); state != CredentialStateRevoked {
				t.Fatalf("%s state = %s, want revoked", kind, state)
			}
			f.assertRevocationInvariant(t)
		})
	}
}

func TestCredentialRollbackAfterARevocationIsRefused(t *testing.T) {
	for _, kind := range revocationRaceKinds {
		t.Run(kind, func(t *testing.T) {
			// Given an apply that replaced an earlier version and has to roll back
			f := newRevocationFixture(t)
			rejected := f.stagedVersion(t)
			// When the revocation begins before the rollback materializes
			f.beginRevocation(t, kind)
			completed := completedCredentialMaterialization{
				Materialization: CredentialMaterialization{
					CredentialID: f.resource.ID, VersionID: rejected.ID,
					PreviousVersionID: f.version.ID, GrantID: f.grant.ID,
				},
				Item: authorizedCredentialMaterialization{
					Reference:  CredentialPlanReference{Name: f.resource.Name, Scope: revocationScope("api")},
					Authorized: AuthorizedCredentialUse{Resource: f.resource, Grant: f.grant, Version: rejected},
				},
				SessionID: revocationSession,
			}
			// Then the rollback is refused and writes no new context
			if err := f.hub.rollbackCredentialPrepare(
				context.Background(), []completedCredentialMaterialization{completed},
			); err == nil {
				t.Fatal("rollback during a revocation succeeded")
			}
			if rows := f.statuses(t); len(rows) != 0 {
				t.Fatalf("refused rollback left rows: %v", rows)
			}
			// And the revocation still reaches its terminal state
			if err := f.finalizeRevocation(t, kind); err != nil {
				t.Fatalf("finalize: %v", err)
			}
			if state := f.revokedState(t, kind); state != CredentialStateRevoked {
				t.Fatalf("%s state = %s, want revoked", kind, state)
			}
			f.assertRevocationInvariant(t)
		})
	}
}
