package server

import (
	"errors"
	"testing"
)

func TestCredentialOperationErrorCategoryPreservesLifecycleReasons(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "missing active version", err: ErrCredentialVersionNotFound, want: "credential_version_not_found"},
		{name: "grant denied", err: ErrCredentialGrantDenied, want: "credential_grant_denied"},
		{name: "forbidden", err: ErrCredentialForbidden, want: "credential_forbidden"},
		{name: "revoked", err: ErrCredentialRevoked, want: "credential_revoked"},
		{name: "payload", err: ErrCredentialPayloadInvalid, want: "credential_payload_invalid"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := credentialOperationErrorCategory(testCase.err); got != testCase.want {
				t.Fatalf("credentialOperationErrorCategory(%v) = %q, want %q", testCase.err, got, testCase.want)
			}
		})
	}
	if got := credentialOperationErrorCategory(errors.New("opaque materialization failed")); got != "credential_operation_failed" {
		t.Fatalf("unexpected generic category: %q", got)
	}
}
