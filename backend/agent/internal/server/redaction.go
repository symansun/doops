package server

import (
	"regexp"
	"strings"
)

// sanitizeSecretText redacts and keeps only the last 1200 bytes, cut on a rune
// boundary. That is right for error text but drops the start of query results.
func sanitizeSecretText(s string) string {
	return utf8Tail(redactSecretText(s), 1200)
}

// redactSecretText redacts without truncating, for data that must stay complete.
func redactSecretText(s string) string {
	if s == "" {
		return ""
	}
	replacers := []*regexp.Regexp{
		regexp.MustCompile(`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`),
		regexp.MustCompile(`(?i)([?&](?:[A-Za-z0-9._-]*(?:token|password|secret|signature|credential|api[_-]?key)|sig|auth|authorization|cookie)=)[^&#\s]+`),
		regexp.MustCompile(`sk-[A-Za-z0-9_-]+`),
		regexp.MustCompile(`(?i)(api[_-]?key[=:])[^ ,}\n]+`),
		regexp.MustCompile(`(?i)(api[_-]?key )[A-Za-z0-9._~+/=-]{20,}`),
		regexp.MustCompile(`(?i)(Bearer )[A-Za-z0-9._-]+`),
		regexp.MustCompile(`(?i)(Basic )[A-Za-z0-9+/=._-]+`),
		regexp.MustCompile(`(?i)((?:authorization|cookie|password|token|secret)[_-]?[A-Za-z0-9_-]*[=:])[^ ,}\n]+`),
		// The space-separated form only counts as a credential when the value is
		// long enough to be one. "token too long" is prose, not a secret.
		regexp.MustCompile(`(?i)((?:authorization|cookie|password|token|secret)[_-]?[A-Za-z0-9_-]* )[A-Za-z0-9._~+/=-]{20,}`),
	}
	s = replacers[0].ReplaceAllString(s, "...REDACTED PRIVATE KEY...")
	for _, replacer := range replacers[1:] {
		s = replacer.ReplaceAllString(s, "${1}...REDACTED")
	}
	for _, pattern := range credentialInlineMaterialPatterns {
		s = pattern.ReplaceAllString(s, "...REDACTED")
	}
	return s
}

// sanitizeDoagentValue redacts and caps every string, for history and error data.
func sanitizeDoagentValue(value interface{}) interface{} {
	return mapDoagentValue(value, sanitizeSecretText)
}

// redactDoagentValue redacts every string without truncating it, for answers.
func redactDoagentValue(value interface{}) interface{} {
	return mapDoagentValue(value, redactSecretText)
}

func mapDoagentValue(value interface{}, text func(string) string) interface{} {
	switch typed := value.(type) {
	case string:
		return text(typed)
	case []interface{}:
		sanitized := make([]interface{}, len(typed))
		for index, item := range typed {
			sanitized[index] = mapDoagentValue(item, text)
		}
		return sanitized
	case map[string]interface{}:
		sanitized := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			if credentialSecretLikeKey(key) || strings.EqualFold(strings.TrimSpace(key), "passwd") {
				sanitized[key] = "...REDACTED"
			} else {
				sanitized[key] = mapDoagentValue(item, text)
			}
		}
		return sanitized
	default:
		return value
	}
}
