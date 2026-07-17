package server

import (
	"errors"
	"regexp"
	"strings"
)

var ErrSecretKeyUnavailable = errors.New("gateway secret key is unavailable")

var credentialInlineMaterialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bgh[pousr]_[A-Za-z0-9]{20,255}\b`),
	regexp.MustCompile(`(?i)\bgithub_pat_[A-Za-z0-9_]{20,255}\b`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,255}\b`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,255}\b`),
	regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{16,255}\b`),
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36,255}\b`),
}

func credentialSecretLikeKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "password") ||
		strings.Contains(key, "credential") ||
		strings.Contains(key, "api_key")
}

func credentialScalarContainsInlineMaterial(value string) bool {
	for _, pattern := range credentialInlineMaterialPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func validCredentialWorkspaceCommit(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
