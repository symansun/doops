package server

import (
	"context"
	"errors"
	"sort"
)

var errCredentialMutationLockID = errors.New("credential_mutation_lock_invalid_id")

func (h *GatewayHub) credentialMutationLock(credentialID string) *contextMutex {
	if lock, ok := h.credentialMutationLocks.Load(credentialID); ok {
		return lock.(*contextMutex)
	}
	created := newContextMutex()
	actual, _ := h.credentialMutationLocks.LoadOrStore(credentialID, &created)
	return actual.(*contextMutex)
}

func (h *GatewayHub) acquireCredentialMutationLocks(ctx context.Context, credentialIDs []string) (func(), error) {
	ids := append([]string(nil), credentialIDs...)
	sort.Strings(ids)

	locks := make([]*contextMutex, 0, len(ids))
	previousID := ""
	for _, credentialID := range ids {
		if credentialID == "" {
			releaseCredentialMutationLocks(locks)
			return nil, errCredentialMutationLockID
		}
		if credentialID == previousID {
			continue
		}
		previousID = credentialID
		lock := h.credentialMutationLock(credentialID)
		if err := lock.Acquire(ctx); err != nil {
			releaseCredentialMutationLocks(locks)
			return nil, err
		}
		locks = append(locks, lock)
		if err := ctx.Err(); err != nil {
			releaseCredentialMutationLocks(locks)
			return nil, err
		}
	}
	return func() { releaseCredentialMutationLocks(locks) }, nil
}

func releaseCredentialMutationLocks(locks []*contextMutex) {
	for index := len(locks) - 1; index >= 0; index-- {
		locks[index].Release()
	}
}
