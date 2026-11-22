package server

import (
	"database/sql"
	"errors"
	"net/http"
)

func credentialErrorCategory(err error) string {
	switch {
	case errors.Is(err, ErrCredentialNotFound), errors.Is(err, sql.ErrNoRows):
		return "credential_not_found"
	case errors.Is(err, ErrCredentialBundleNotFound):
		return "credential_bundle_not_found"
	case errors.Is(err, ErrCredentialAmbiguous):
		return "credential_reference_ambiguous"
	case errors.Is(err, ErrCredentialTargetUnenrolled):
		return "credential_target_unenrolled"
	case errors.Is(err, ErrCredentialVersionNotFound):
		return "credential_version_not_found"
	case errors.Is(err, ErrCredentialRevoked):
		return "credential_revoked"
	case errors.Is(err, ErrCredentialGrantDenied):
		category, _ := credentialGrantDenialCategory(err)
		return category
	case errors.Is(err, ErrCredentialForbidden):
		return "credential_forbidden"
	case errors.Is(err, ErrSecretKeyUnavailable):
		return "credential_key_unavailable"
	case errors.Is(err, ErrSecretKeyMismatch):
		// Startup refuses a mismatched key; this guards a key changed in a
		// long-running process before it could encrypt new rows with it.
		return "credential_key_mismatch"
	case errors.Is(err, ErrCredentialPayloadInvalid):
		return "credential_payload_invalid"
	case errors.Is(err, ErrCredentialAuditUnavailable):
		return "credential_audit_unavailable"
	default:
		return credentialOperationErrorCategory(err)
	}
}

func credentialHTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrCredentialNotFound), errors.Is(err, ErrCredentialVersionNotFound),
		errors.Is(err, ErrCredentialBundleNotFound), errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	case errors.Is(err, ErrCredentialAmbiguous), errors.Is(err, ErrCredentialTargetUnenrolled):
		return http.StatusConflict
	case errors.Is(err, ErrCredentialForbidden), errors.Is(err, ErrCredentialGrantDenied):
		return http.StatusForbidden
	case errors.Is(err, ErrSecretKeyUnavailable), errors.Is(err, ErrSecretKeyMismatch):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrCredentialAuditUnavailable):
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}
